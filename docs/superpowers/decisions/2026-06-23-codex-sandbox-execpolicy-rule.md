# Codex sandboxes the telemetry hook — allow it with a machine-level execpolicy rule

**Date:** 2026-06-23
**Status:** accepted (mechanism and location) — end-to-end confirmation from inside Codex
still pending (`codex execpolicy check` + a trusted `.codex/` layer; see **Verify**).
**Refs:** [Codex execution policy](https://developers.openai.com/codex/exec-policy),
[Codex config reference](https://developers.openai.com/codex/config-reference). Closes the open
thread in [2026-06-22-windows-hook-shell-and-paths.md](2026-06-22-windows-hook-shell-and-paths.md)
(points 7–8: "the tool sandbox resolved the config path but reported endpoint `(unset)`;
cross-harness config visibility remains open").

## The problem

The on-`PATH` binary is correctly provisioned — but **only some harnesses see it that way**.
Run from Claude Code on the dev machine, `skills-telemetry status` reports
`config_dir: C:\Users\denif\.config\qubership-skills-telemetry`, a set endpoint, and
`state: provisioned`; `selftest` delivers. Run the *same* binary from Codex, `status` reports
`endpoint: (unset)` / `state: not provisioned`, `selftest` fails with
`no endpoint: machine is not provisioned`, and `update-check` reports `latest: unknown` because
GitHub is unreachable.

The config on disk is intact and identical for both — so this is not a missing provision and not
the [MSIX `%AppData%` virtualization](2026-06-23-config-cache-dir-xdg-msix.md) we already fixed
(the XDG move took that off the table). The discriminator is **Codex's sandbox**: by default
Codex runs hook commands and tool calls in an isolated environment that denies the binary the two
things it needs —

1. **read access to the machine-level config** outside the project (`~/.config/qubership-skills-telemetry/env`), and
2. **network egress** to the collector (and to GitHub for the advisory update check).

Either an env remap (a different `HOME`/`USERPROFILE`/`XDG_CONFIG_HOME` inside the sandbox) or
plain filesystem/network isolation produces the same symptom: the binary resolves an empty config
view, so it reports `not provisioned`. Both are lifted the same way — by letting the specific
telemetry commands run **outside** the sandbox.

## The mechanism — Codex execution-policy rules

Codex reads `.rules` files written in Starlark and evaluates each command against them
([exec-policy docs](https://developers.openai.com/codex/exec-policy)):

- `prefix_rule(pattern, decision, justification, match, not_match)`. `pattern` is a list whose
  elements are literals or a union of literals matched per argument position.
- `decision = "allow"` means **"run the command outside the sandbox without prompting"** — exactly
  the escape hatch this needs. (`prompt` asks; `forbidden` blocks. The most restrictive matching
  rule wins.)
- `match` / `not_match` are **load-time self-tests**, not matching logic — example command lines
  Codex validates when it loads the rules, to catch a mis-scoped pattern before it takes effect.
- **Load locations:** `rules/` under every active config layer — the user layer `~/.codex/rules/`
  (always loaded) and team/system layers, plus **project-local `<repo>/.codex/rules/`, which loads
  only when the project `.codex/` layer is trusted**.

## Why not ship it through APM

APM cannot carry this rule. Its package primitives are instructions, skills, prompts, agents,
chatmodes, context, and hooks — **there is no `rules` primitive and no generic "drop an arbitrary
file into a harness config dir" mechanism**. The `.codex/` tree is build-output generated *only*
from primitives; the hooks primitive emits `.codex/hooks.json` (tool-use / stop events), not an
execution policy. APM also writes primitives **into the repo's** harness dirs (`<repo>/.codex/…`),
never into the user-level `~/.codex/`. So a machine-level rule is structurally out of APM's reach,
and the only APM-shaped route (a post-install lifecycle hook copying a bundled `.rules` file) is
undocumented and would still land in the trust-gated per-repo location.

## Decision

Treat the Codex sandbox rule as **machine-level provisioning, owned by the
`provision-skills-telemetry` skill**, not as package content. The skill writes
`~/.codex/rules/skills-telemetry.rules` (user layer).

This matches the rest of the telemetry architecture, which is deliberately machine-level and
uniform: the binary at `~/.local/bin`, config at `~/.config`, spool at `~/.cache`. The user layer
loads **always, with no per-project trust step**, so the hook works in every repo the package is
installed into — the same robustness goal as the XDG path move.

The rule is **narrowly scoped (least privilege)**: it allows only the three commands that genuinely
need to leave the sandbox and excludes the rest. In particular `provision` is **not** allowed out —
it writes config and reads a token at a no-echo prompt, and must not run unattended outside the
sandbox.

```python
# ~/.codex/rules/skills-telemetry.rules
prefix_rule(
    pattern = [["skills-telemetry", "skills-telemetry.exe"], "ingest", "--agent=codex"],
    decision = "allow",
    justification = "Allow the trusted telemetry hook to read its machine config and send Codex skill usage events.",
    match = ["skills-telemetry ingest --agent=codex", "skills-telemetry.exe ingest --agent=codex"],
    not_match = ["skills-telemetry status", "skills-telemetry selftest", "skills-telemetry provision",
                 "skills-telemetry update-check", "skills-telemetry ingest --agent=claude",
                 "skills-telemetry ingest --agent=cursor"],
)
# status and selftest get the same machine-config / egress view for diagnostics; provision stays sandboxed.
```

`status` (read-only, never prints the token) and `selftest` (the intended end-to-end probe) get
their own `allow` rules with matching `not_match` guards.

## Alternatives rejected

- **Per-repo `<repo>/.codex/rules/`** (where the file currently sits). Matches the project's
  "installing the package into a repo is the consent boundary" model, but loads **only when the
  project `.codex/` layer is trusted** — a non-obvious manual step, and the rule is silently inert
  until it is taken. Rejected for fragility; the machine-level user layer needs no trust gate.
- **APM post-install lifecycle hook** copying a bundled `.rules` file. "Pure package", but relies
  on an undocumented APM file-copy path and still targets the trust-gated per-repo location. Not
  worth the fragility.
- **Document-only, place it by hand.** Lowest magic, but leaves every machine a manual step that
  the symptom (silent "not provisioned" in Codex) does not advertise. Provisioning should do it.

## Consequences

- **Escape hatch is broader than per-repo consent.** A user-layer rule lets the telemetry binary
  leave the Codex sandbox in *every* Codex session on the machine, not just in repos where the
  package is installed. Accepted because the rule is tightly pattern-scoped to three
  `skills-telemetry` subcommands and excludes `provision`; the binary is the trusted telemetry
  component, and machine-level is already the telemetry config model.
- **`provision-skills-telemetry` gains a step** (and a row in its failure table): when Codex is a
  target, write/refresh `~/.codex/rules/skills-telemetry.rules` and tell the user to confirm the
  rule loads. The skill currently does **not** do this yet — follow-up work.
- **Verification is documented, not assumed** (see below) — the rule can be present and correct yet
  inert, so the skill must prove it loaded rather than report success on file existence.

## Verify

Inside Codex (the binary, and `codex`, must be on `PATH` there):

```sh
codex execpolicy check --rules ~/.codex/rules/skills-telemetry.rules \
  "skills-telemetry ingest --agent=codex" --pretty   # expect decision: allow + the matching rule
```

Then confirm end to end: from Codex, `skills-telemetry status` shows the real `~/.config` path and
`state: provisioned`, and `selftest` delivers. If `execpolicy check` says `allow` but Codex still
reports `not provisioned`, the rule is not being loaded — for the per-repo location that means the
project `.codex/` layer is not trusted; for the user layer, check the file path and that
`~/.codex/rules/` is the layer Codex scans on this install.
