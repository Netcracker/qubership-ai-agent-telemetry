---
name: ai-agent-telemetry-configure
description: Set up, repair, and verify skill-usage telemetry on a machine. Use right after installing the ai-agent-telemetry package, when skill events are not reaching the collector, when telemetry "stopped working", or whenever the user asks to provision, onboard, check, or fix skills telemetry — even phrased loosely as "is my telemetry working?" or "set up skills telemetry".
---

# Configure AI agent telemetry

This machine reports skill-usage telemetry through a small binary, `ai-agent-telemetry`.
The binary needs per-machine config the public package cannot carry: a collector endpoint,
sometimes a CA certificate, sometimes a token. Your job is to get that config in place and
prove events actually reach the collector — then stop.

You orchestrate; the binary does the sensitive work. It owns the config files (atomic writes,
permissions, idempotency) and reads the token without echo. Discover and ask; let the binary
write. Never put the token in your own output.

## What "working" means

- `ai-agent-telemetry status` — read-only state: binary version, config dir, endpoint, whether a
  CA file is present, spool backlog, last flush attempt, and a configured/not verdict.
- `ai-agent-telemetry selftest` — sends one real, marked probe event and reports whether the
  collector accepted it and the event left the spool.
- Config lives under the config dir that `status` prints: `env` (endpoint, token) and an
  optional `ca.crt`. These are the binary's to write — don't hand-edit them.

## Calling the binary

The hooks invoke the binary by its bare name (`ai-agent-telemetry ingest …`), so the binary
lives on `PATH` at `~/.local/bin/ai-agent-telemetry` (`~/.local/bin\ai-agent-telemetry.exe` on
Windows) — the uniform install location on every OS. The installer in
`references/deployment.md` puts it there and adds `~/.local/bin` to `PATH`.

**Call the binary by its bare name first — `ai-agent-telemetry <cmd>` — and escalate only by what
fails.** Bare name is the one form that works everywhere that matters: it is the only command
shape the Codex execpolicy sandbox lets out (a path as `argv[0]` stays sandboxed), and it
resolves on Claude Code and Cursor whenever `~/.local/bin` is already on `PATH` — the normal
steady state. Lead with it; reach for a full path only after bare name fails *and* you have ruled
out the sandbox.

When bare name fails, read *why* before escalating — the two failures take opposite fixes:

- **`command not found` / the bare name does not resolve.** `~/.local/bin` is not on this
  process's `PATH`; no sandbox is involved (a sandbox runs the binary, it does not hide the
  command from the shell). This is the fresh-install case — the installer wrote `PATH` to the
  persistent user environment, but the already-running agent still carries the old one. **Only
  here is a full-path call correct:** `~/.local/bin/ai-agent-telemetry <cmd>` (the `.exe` on
  Windows), which always works once the installer has placed the binary. You must still tell the
  user to restart so real skill runs — which fire the bare-name hook — resolve it.
- **The bare name runs, but the result looks wrong** — `endpoint: (unset)` / `not configured`
  against a config you know is good, a denied config-dir read, or a send that fails for no
  network reason. That is the **sandbox**, not a missing configuration. **Do not switch to a full
  path** — it puts the path in `argv[0]`, misses the execpolicy rule, and stays sandboxed, turning
  a diagnosis into a guaranteed false negative. Instead get the binary *out* of the sandbox:
  ensure the execpolicy rule is present and loads (Codex — see "Codex sandbox rule (check)"). The
  rule must let the binary read `~/.config/ai-agent-telemetry/` and
  `~/.cache/ai-agent-telemetry/`, run `~/.local/bin/ai-agent-telemetry`, and reach the
  collector endpoint over the network. Then retry — still by bare name.

The corollary for hooks: **the hook fires the bare name, so it only resolves after the agent
restarts** — until then a real skill run finds no `ai-agent-telemetry` on `PATH`. That is expected;
prove delivery yourself with bare-name `selftest` (or the full-path fallback while the name does
not yet resolve) and tell the user to restart so the hook arms.

**What "restart the agent" means — be explicit with the user.** A soft reset is not enough: a
new conversation, a new chat, or clearing the session reuses the same OS process, which still
carries the old `PATH`, so the hook keeps failing to find the bare name. The agent's *process*
must be recreated so it reads the refreshed `PATH` (the installer wrote it to the persistent
user environment, but only a brand-new process inherits it). Tell the user, in these words:

- **Claude Desktop / GUI app** — fully quit the application (not just close the window or open a
  new chat) and reopen it. On Windows, quit from the tray if it keeps running in the background.
- **Terminal / CLI** — end the session and **close the terminal tab or window**, then open a new
  one. Reopening in the same tab can keep the stale environment; a fresh tab is the safe move.

How to confirm the restart actually took: after it, `ai-agent-telemetry` resolves by bare name
(`Get-Command ai-agent-telemetry` / `command -v ai-agent-telemetry` succeeds) **and** a fresh skill
run advances `last_flush_attempt` in `status`. If the bare name still does not resolve, the
process was not truly recreated — repeat the full quit / close-the-tab step.

If `~/.local/bin/ai-agent-telemetry` is absent, run the installer (`references/deployment.md`).
Read every `ai-agent-telemetry <cmd>` below as a bare-name call, falling back to the full path only
in the `command not found` case above (see "Codex sandbox rule (check)" for why a full-path call
misleads on Codex).

**Locating and checking the binary.** Everything lives at fixed, OS-uniform paths, so diagnosis
never has to guess:

- **Binary** — `~/.local/bin/ai-agent-telemetry` (`.exe` on Windows). Confirm it exists and runs:
  `~/.local/bin/ai-agent-telemetry version` (POSIX) or `& "$env:USERPROFILE\.local\bin\ai-agent-telemetry.exe" version`.
- **On `PATH`?** — the hook needs the bare name to resolve. Check with `command -v ai-agent-telemetry`
  (POSIX) or `Get-Command ai-agent-telemetry` (PowerShell). If that fails but the full-path call
  works, `~/.local/bin` is not on this process's `PATH` yet — the install added it, but the agent
  must restart to pick it up.
- **Config** (endpoint, token, `ca.crt`) — under the `config_dir` that `status` prints. This is
  a uniform XDG path on every OS: `$XDG_CONFIG_HOME` else `~/.config/ai-agent-telemetry/`
  (`%USERPROFILE%\.config\ai-agent-telemetry\` on Windows). Always read the live path from
  `status` rather than assuming it. The outbox/offset spool sits under the cache dir
  (`~/.cache/ai-agent-telemetry/`); `status` reports its backlog as `buffered`, so you
  rarely open it by hand.

## First run: remove legacy skills-telemetry state

Before configuring, check for and remove leftovers from the old `skills-telemetry` name. This is idempotent —
re-running it is safe. Report what you find and what you remove; remove nothing silently.

```bash
# Old binary
rm -f ~/.local/bin/skills-telemetry ~/.local/bin/skills-telemetry.exe
# Old config and cache dirs
rm -rf ~/.config/skills-telemetry ~/.cache/skills-telemetry
# Old env vars in shell profiles — report matches, then remove the lines after confirming
grep -rnE 'SKILLS_TELEMETRY_(ENDPOINT|TOKEN)' ~/.zshrc ~/.bashrc ~/.profile 2>/dev/null
```

Only after the legacy state is gone, continue with the configure steps below.

## Workflow

Read state first, close only the gaps it shows, then prove delivery.

1. Ensure the binary is installed: run the installer one-liner (`references/deployment.md`). It
   is idempotent — it downloads the binary to `~/.local/bin` only when missing and ensures
   `~/.local/bin` is on `PATH`. Then run `status` by bare name (see "Calling the binary").
2. Fix each gap `status` reports (next section).
3. Run `selftest`. Re-run `status` / `selftest` after each fix until it passes.
4. Confirm the hook is wired for every installed harness (see "Confirm the hook is wired"). **If
   Codex is a target, also ensure the sandbox execpolicy rule is in place and loads** (see "Codex
   sandbox rule (check)") — without it Codex silently sends nothing. Then tell the user to
   restart the agent so the bare-name hook resolves on real skill runs — and be explicit that this
   means fully quitting the app or closing the terminal tab, not just a new chat (see "Calling the
   binary" for the exact wording and how to confirm it took).
5. Report the outcome (see "Verify delivery").
6. Check for a newer version and offer it (see "Updating"). Do this at the end of every run —
   provisioning, repair, or a plain status check — so the user hears about updates without asking.

## Importing a ready config file

If the user provides a ready `env` file (it carries `AI_AGENT_TELEMETRY_ENDPOINT` and
`AI_AGENT_TELEMETRY_TOKEN`), copy it into place instead of provisioning field by field:

1. Read the config dir from `status` (the `config_dir:` line).
2. Copy the file there as `env`, verbatim:
   `mkdir -p <config_dir> && cp <given-file> <config_dir>/env`.
3. Run `selftest` to confirm delivery.

Do not open, read, print, or echo the file — it may hold a token, and anything in this
conversation enters the model's context. A copy moves the bytes without reading them. The
CLI mints the anonymous `machine-id` itself on first send, so the two properties are
enough.

## Closing gaps

- **Endpoint missing** — ask the user for the collector URL; their onboarding portal or admin
  has it. Run `ai-agent-telemetry configure --endpoint=<url>`.
- **CA needed** (`selftest` fails with a certificate / TLS error) — only self-signed or
  non-trusted-CA deployments need this; a publicly trusted or MDM-distributed CA needs nothing.
  Obtain the `.crt` (`references/deployment.md` covers a local cluster and a corporate PKI) and
  run `ai-agent-telemetry configure --ca=<path>`; the binary copies it to `ca.crt`.
- **Token required** (collector returns 401 / 403) — have the user type it into the binary's
  no-echo prompt: run `ai-agent-telemetry configure` and let them enter the token when asked.
  Don't ask the user to paste the token to you, and don't type it yourself — anything in this
  conversation becomes part of the model's context and would leak the secret.

## Updating

Run `ai-agent-telemetry update-check` at the end of every run — provisioning, repair, or a plain
status check — not only when the user asks. It prints `installed:`, `latest:`, and
`update_available: yes|no|unknown` (network, advisory, always exits 0).

- `update_available: no` — say nothing beyond the normal outcome; the machine is current.
- `update_available: unknown` — the check could not reach GitHub. Don't nag; mention it only if
  the user is already asking about versions.
- `update_available: yes` — tell the user the installed and latest versions and **ask whether to
  update**. Don't update without consent.

On a yes, apply it by re-running the latest installer with `--force` (it pins the latest binary
and reinstalls after the same checksum check):

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/bootstrap.sh | sh -s -- --force   # macOS/Linux
iex "& { $(irm https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/bootstrap.ps1) } --force"      # Windows
```

Then re-run `update-check` to confirm `installed:` matches `latest:`. No agent restart is
needed — the installer replaces the binary in place at `~/.local/bin/ai-agent-telemetry`, and
the bare name already resolves from a previous install, so the hook picks up the new version
immediately.

In a sandboxed environment (Codex) the command reports `latest: unknown` because the execpolicy
allowlist excludes `update-check` by design. Don't treat that as "no update" — ask the user to
escalate out of the sandbox or run the command in a regular terminal.

This is the skill-driven check: it surfaces updates whenever the skill happens to run. Triggering
the skill *automatically* on a cadence (for example a periodic "new version available?" nudge
every few sessions) is separate and not wired yet.

## Failure → fix

| `status` / `selftest` shows | Cause | Fix |
|---|---|---|
| binary not found | not installed yet | run the installer one-liner (also puts `~/.local/bin` on `PATH`) |
| binary present but stale or broken (`version` wrong, won't run) | the installer only downloads when the file is missing | re-run the installer with `--force` to fetch a fresh copy (see "Updating") |
| bare name not found on a real skill run | `PATH` not refreshed yet | restart the agent so the hook resolves the binary — a *full* restart (quit the app / close the terminal tab), not a new chat (see "Calling the binary") |
| endpoint empty | not configured | `configure --endpoint` |
| TLS verification failed | CA missing or wrong | `configure --ca` |
| connection refused / timeout | network or VPN | confirm the user can reach the collector host |
| 401 / 403 | token missing or rejected | `configure`, enter the token at the no-echo prompt |
| spool growing, flush failing | one of the above | fix the reported cause, then `selftest` |
| `selftest` passes but real skill runs send nothing | the harness hook is not wired (never installed) | confirm and repair the hook (see "Confirm the hook is wired") |
| **Cursor only:** hook is wired but Cursor silently ignores it | `.cursor/hooks.json` is missing the top-level `"version": 1` (apm < 0.21.0 did not add it automatically) | add `"version": 1` at the top level of `.cursor/hooks.json` next to the `"hooks"` key |
| **Codex UI shows an old hook command** (for example `.codex\hooks\...\bootstrap.bat ingest --agent=codex`) while the package source expects `ai-agent-telemetry ingest --agent=codex` | stale installed hook or stale Codex hook trust state; often the UI is showing another checkout's `.codex/hooks.json`, not the current worktree | inspect the active hook path and repair it, then clear stale `hooks.state` entries and fully restart Codex (see "Codex stale hook UI / trust cache") |
| **Codex only:** `status` / `selftest` report `endpoint: (unset)` / `not configured` (or real Codex skill runs send nothing) while Claude Code or a plain shell work, and `update-check` says `latest: unknown` | Codex sandbox hides `~/.config` and blocks egress — not a missing configuration | write the Codex execpolicy rule, then restart Codex (see "Codex sandbox rule (check)" → [references/codex-sandbox.md](references/codex-sandbox.md)) |
| **Codex false negative:** same `not configured` / `endpoint: (unset)` symptom, but you called the binary by **full path**, via a `&` wrapper, or with a non-allowlisted subcommand (`version`, `update-check`) | that invocation does not match the execpolicy rule, so it ran sandboxed — the rule itself may be perfectly fine | re-test with the bare-name allowlisted form `ai-agent-telemetry status` / `ai-agent-telemetry selftest`; don't diagnose from the unmatched call (see "Codex sandbox rule (check)") |

`selftest` prints the raw send error (for example an `x509` / `tls` message or an HTTP status);
map it to a cause above. `status` shows the spool backlog and the configured/not verdict but
does not itself test the network.

## Confirm the hook is wired

`selftest` proves the binary can reach the collector, but it calls the binary directly — it
does not prove the harness fires the hook on real skill runs. A green `selftest` with an
unwired hook looks done yet captures nothing. After `selftest` passes, confirm the telemetry
hook is registered for every harness the package is installed for in this repository.

`apm install --target <harness>` writes the hook into that harness's own config under the
repository root (`apm compile` only regenerates the instruction layer — `AGENTS.md`,
`CLAUDE.md`, and friends — and never touches the hook file). The hook calls the binary by its
bare name, so for every config directory that exists the active hook file must contain the
command `ai-agent-telemetry ingest --agent=<harness>`:

| Harness | Active hook file | Must contain |
|---|---|---|
| Claude Code | `.claude/settings.json` | a `PreToolUse` hook matched on `Skill` running `ai-agent-telemetry ingest --agent=claude` |
| Codex | `.codex/hooks.json` | a `Stop` hook running `ai-agent-telemetry ingest --agent=codex` |
| Cursor | `.cursor/hooks.json` | an `afterAgentResponse` hook running `ai-agent-telemetry ingest --agent=cursor`, plus a numeric top-level `version` |

A directory present but missing the command means the hook never installed: re-run
`apm install --target <harness>` then `apm compile`, then re-check.

### Codex stale hook UI / trust cache

Codex Desktop's Hooks settings can make a stale hook look like an internal harness cache. Treat
it as file state until proven otherwise. The UI groups hooks by repository root and reads that
root's active `.codex/hooks.json`; in a worktree session this may be the main checkout, not the
current worktree. Codex also persists hook enable/trust metadata in
`~/.codex/config.toml` under `[hooks.state]`, keyed by the absolute hook file path and hook index.
Those entries do not define the command, but after a hook shape changes they can keep stale
trust state around and should be cleared for the changed hook path.

When the UI shows an old command, especially a removed bootstrap wrapper such as
`.codex\hooks\ai-agent-telemetry-configure\scripts\bootstrap.bat ingest --agent=codex`, investigate
in this order:

1. Read the Codex project/trust state and hook state:
   - `~/.codex/config.toml`
   - look for `[projects.'...']` and `[hooks.state.'<absolute .codex/hooks.json>:stop:...']`
2. Open the exact hook file named in `hooks.state` and the Hooks UI path. On Windows this is
   usually one of:
   - `<repo>\.codex\hooks.json`
   - `<repo>\.claude\worktrees\<name>\.codex\hooks.json`
3. Compare that active file with the package hook source:
   - the hook source in the `ai-agent-telemetry` package (Netcracker/qubership-ai-agent-telemetry)
   - both must contain only `ai-agent-telemetry ingest --agent=codex` for the command; there should
     be no `commandWindows`, no `.codex/hooks/.../bootstrap.bat`, and no `sh ./scripts/bootstrap.sh`
     in the Codex hook.
4. Search broadly before declaring it fixed:
   - `rg -n "bootstrap\.bat|bootstrap\.sh|commandWindows|ai-agent-telemetry ingest --agent=codex" . ~/.codex`
   - On Windows, include the main checkout explicitly if the current session is in a worktree.
5. Repair both the package source and the installed active hook. Prefer `apm install --target codex`
   from the intended repo root when available; otherwise make the installed `.codex/hooks.json`
   match the current package source exactly.
6. Clear only the stale hook-state blocks for that absolute hook path from `~/.codex/config.toml`.
   Leave unrelated hook state alone. After this, Codex may ask the user to trust/enable the hook
   again; that is expected because the command hash changed.
7. Fully restart Codex Desktop. A new chat is not enough; the process must be recreated so the UI
   and hook runner reload file state.

After repair, prove the command shape directly:

```sh
ai-agent-telemetry status
ai-agent-telemetry selftest
ai-agent-telemetry ingest --agent=codex
```

Then re-open Hooks settings and confirm the displayed command is
`ai-agent-telemetry ingest --agent=codex`. If the UI still shows the old command, it is almost
always reading a different `.codex/hooks.json`; go back to the absolute path shown by the UI or
`~/.codex/config.toml` and patch that file, not the current worktree by assumption.

Harness-specific trap:

- **Claude Code** also writes the command into `.claude/apm-hooks.json`, but that file is
  APM's provenance ledger, not a trigger — only `.claude/settings.json` arms the hook. Check
  `settings.json`; a match in `apm-hooks.json` alone is a false positive.

## Codex sandbox rule (check)

**Only when Codex is one of the targets on this machine** — skip it otherwise.

Codex sandboxes the hook, so the binary cannot read `~/.config` or reach the network unless an
execpolicy rule (`~/.codex/rules/ai-agent-telemetry.rules`) lets it out. The tell is a **Codex-only**
failure: inside Codex `status` reports `endpoint: (unset)` / `not configured` while the same
binary run from Claude Code or a plain shell is `configured` — not a missing configuration, the
sandbox just hides the config. Verify the rule, cheapest check first:

1. **Present** — `~/.codex/rules/ai-agent-telemetry.rules` exists (on Windows
   `%USERPROFILE%\.codex\rules\ai-agent-telemetry.rules`).
2. **Valid + allows** — inside Codex: `codex execpolicy check --rules
   ~/.codex/rules/ai-agent-telemetry.rules "ai-agent-telemetry ingest --agent=codex" --pretty` reports
   `decision: allow`.
3. **Effective** — from inside Codex (after a restart), **call the binary by its bare name**:
   `ai-agent-telemetry status` is `provisioned` and `ai-agent-telemetry selftest` delivers.

**Verify only with the bare-name allowlisted forms.** The rule escapes the sandbox for one precise
shape — bare `ai-agent-telemetry` / `ai-agent-telemetry.exe` followed by `status`, `selftest`, or
`ingest --agent=codex`. A full-path call, a `&` wrapper, or a non-allowlisted subcommand
(`version`, `update-check`) stays sandboxed and reports a **false** `not configured`; in Codex
`update-check` therefore always reports `latest: unknown`, which is expected. The full enumeration
and why is in [references/codex-sandbox.md](references/codex-sandbox.md). If the bare-name forms
still report `not configured` after a restart, the rule is not loading.

If the rule is missing or a check fails, write/repair it — full rationale, the exact rule
content, and the load-vs-trust troubleshooting are in
[references/codex-sandbox.md](references/codex-sandbox.md). Don't report Codex telemetry working
until check 3 holds.

## Verify delivery

`selftest` sends a real event as a test. Two outcomes count as success:

- The collector accepted it (HTTP 200) and it left the spool — the pipeline works end to end up
  to ingest. This is the guarantee you can always make.
- If the user has read access to the store (VictoriaLogs or similar), offer the query that
  confirms the probe landed (`references/deployment.md`). Most users won't have it — don't block
  on it.

If the probe stays in the spool, delivery failed: treat it as a gap and diagnose from `status`.

Don't report success without a passing `selftest` and a wired hook. A written config that
can't reach the collector looks done but sends nothing; a green `selftest` with an unwired
hook captures nothing on real skill runs. Both must hold. **For Codex, a third must hold:** the
sandbox execpolicy rule is present and loads (see "Codex sandbox rule (check)") — otherwise
the sandbox blocks the binary and Codex sends nothing no matter how the hook is wired.
