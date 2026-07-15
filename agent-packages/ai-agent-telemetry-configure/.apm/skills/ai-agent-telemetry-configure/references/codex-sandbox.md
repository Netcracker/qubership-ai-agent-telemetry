# Codex sandbox: let the hook out (execpolicy rule)

Reference for the "Codex sandbox rule" check in `SKILL.md`. Read this when the CLI-managed rule is
missing or inert after `ai-agent-telemetry hooks install --target=codex`.

## Why this is needed

The CLI-managed global Codex hook still runs in a **sandbox** that, by default, denies the binary the
two things telemetry needs: read access to the machine-level config outside the project
(`~/.config/ai-agent-telemetry/`) and network egress to the collector. The tell is a
**Codex-only** failure: inside Codex, `status` reports `endpoint: (unset)` / `not configured` and
`selftest` fails with `no endpoint`, while the *same* binary run from Claude Code or a plain shell
reports `configured` and delivers; `update-check` reporting `latest: unknown` is the same sandbox
blocking egress to GitHub. This is **not** a missing configuration — the config is on disk; the sandbox
just hides it. (It is also not the MSIX `%AppData%` issue, which the `~/.config` move already
fixed.)

The fix is a Codex **execution-policy rule** that lets those telemetry commands run outside the
sandbox. Keep it machine-level, like the rest of telemetry (`~/.local/bin`, `~/.config`): write it
to the **user layer** `~/.codex/rules/`, which Codex loads in every repo with no per-project trust
step.

The rule controls sandbox execution; it does not approve the hook on the user's behalf. Do not
clear or rewrite Codex trust hashes automatically. If Codex reports a changed command, ask the user
to inspect and approve `ai-agent-telemetry ingest --agent=codex`.

## Write the rule

`ai-agent-telemetry hooks install --target=codex` writes
`~/.codex/rules/ai-agent-telemetry.rules` (on Windows,
`%USERPROFILE%\.codex\rules\ai-agent-telemetry.rules`) with exactly the content below. It allows
only `ingest --agent=codex`, `status`, and `selftest` to leave the sandbox. `configure` stays
sandboxed because it writes configuration and reads a token interactively.

```python
# ai-agent-telemetry: allow the telemetry hook and diagnostics out of the Codex sandbox.
# Scoped to these subcommands only; configure stays sandboxed.
prefix_rule(
    pattern = [["ai-agent-telemetry", "ai-agent-telemetry.exe"], "ingest", "--agent=codex"],
    decision = "allow",
    justification = "Allow the trusted telemetry hook to read its machine config and send Codex skill usage events.",
    match = ["ai-agent-telemetry ingest --agent=codex", "ai-agent-telemetry.exe ingest --agent=codex"],
    not_match = ["ai-agent-telemetry status", "ai-agent-telemetry selftest", "ai-agent-telemetry configure",
                 "ai-agent-telemetry update-check", "ai-agent-telemetry ingest --agent=claude",
                 "ai-agent-telemetry ingest --agent=cursor"],
)
prefix_rule(
    pattern = [["ai-agent-telemetry", "ai-agent-telemetry.exe"], "status"],
    decision = "allow",
    justification = "Allow telemetry diagnostics to read configured state outside the sandbox.",
    match = ["ai-agent-telemetry status", "ai-agent-telemetry.exe status"],
    not_match = ["ai-agent-telemetry configure", "ai-agent-telemetry selftest",
                 "ai-agent-telemetry ingest --agent=codex", "ai-agent-telemetry update-check"],
)
prefix_rule(
    pattern = [["ai-agent-telemetry", "ai-agent-telemetry.exe"], "selftest"],
    decision = "allow",
    justification = "Allow telemetry diagnostics to send a marked probe event outside the sandbox.",
    match = ["ai-agent-telemetry selftest", "ai-agent-telemetry.exe selftest"],
    not_match = ["ai-agent-telemetry configure", "ai-agent-telemetry status",
                 "ai-agent-telemetry ingest --agent=codex", "ai-agent-telemetry update-check"],
)
```

Codex scans `rules/` at **startup**, so after writing the file Codex must be restarted (a *full*
restart — see "Calling the binary" in `SKILL.md`) before the rule takes effect.

## Check the rule is there and loads

Three checks, cheapest first — run them whenever Codex is a target, not only when something looks
broken, so a missing or inert rule is caught before you report success:

1. **Present** — `~/.codex/rules/ai-agent-telemetry.rules` exists and carries the three
   `prefix_rule`s above.
2. **Valid + allows** — inside Codex (where `codex` is on `PATH`), run:

   ```sh
   codex execpolicy check --rules ~/.codex/rules/ai-agent-telemetry.rules \
     ai-agent-telemetry ingest --agent=codex --pretty
   ```

   The command reports `decision: allow` and the matching rule. The `match` / `not_match` lines
   also self-test at load, so a mis-scoped pattern surfaces on Codex startup.
3. **Effective** — from inside Codex, after a restart, call the binary **by its bare name**:
   `ai-agent-telemetry status` shows the real `~/.config` path and `state: configured`, and
   `ai-agent-telemetry selftest` delivers.

## Test only with the documented forms (the false-negative trap)

execpolicy matches the literal `argv` tokens (prefix only — no substring or regex), and the rule
is keyed to the bare program name: `argv[0]` must be exactly `ai-agent-telemetry` or
`ai-agent-telemetry.exe`, followed by `status`, `selftest`, or `ingest --agent=codex`. The CLI also
rejects trailing arguments on the Codex ingest form before it reads configuration or the outbox.

Call the binary with an unmatched program name or subcommand and it runs **inside** the sandbox,
where it cannot read `~/.config`. It then reports a **false** `endpoint: (unset)` / `not configured`
/ send failure that says nothing about telemetry, only that you called it the unmatched way. This
is the trap that makes a working install look broken. The misleading forms are:

- **Full path or a `&` wrapper** — `& "…\.local\bin\ai-agent-telemetry.exe" status`. `argv[0]` is the
  path, not the bare name, so no rule matches. Use `ai-agent-telemetry status`.
- **A non-allowlisted subcommand** — `version`, `update-check`. The rule deliberately leaves these
  sandboxed, so in Codex `update-check` **always** reports `latest: unknown` — expected, not a
  network fault.
So inside Codex, verify only with the bare-name `status` / `selftest`, and never conclude telemetry
is broken from a full-path or non-allowlisted call. Only if the bare-name forms still report
`not configured` after a restart is the rule genuinely not taking effect — diagnose that below.

If `execpolicy check` says `allow` but the bare-name `status` still reports `not configured`, the
rule is not being loaded: confirm the file sits in the **user** layer `~/.codex/rules/` that Codex
scans. A per-repo `<repo>/.codex/rules/` copy loads only when the project `.codex/` layer is
trusted in Codex — which is exactly why this skill uses the user layer instead. Don't report Codex
telemetry working until check 3 holds.
