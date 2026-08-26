---
name: ai-agent-telemetry-configure
description: Set up, test, troubleshoot, repair, and verify AI agent telemetry.
---

# Configure AI agent telemetry

This machine reports skill executions, command invocations, and MCP tool
executions through `ai-agent-telemetry`. Each harness exposes a documented
subset of those events. The binary needs per-machine configuration that the
public package cannot carry: a collector endpoint, sometimes a CA certificate,
and sometimes a token. Get that configuration in place, prove delivery, verify
a real harness event, and then stop.

You orchestrate; the binary does the sensitive work. It owns the config files (atomic writes,
permissions, idempotency) and reads the token without echo. Discover and ask; let the binary
write. Never put the token in your own output.

## What "working" means

- `ai-agent-telemetry status` — read-only state: binary version, config dir, endpoint, CA,
  repository scope, spool backlog, last flush attempt, and global hook registration. Add
  `--verbose` to inspect effective buffer capacity and flush timeout.
- `ai-agent-telemetry selftest` — sends one real, marked probe event and reports whether the
  collector accepted it and the event left the spool.
- Config lives under the config dir that `status` prints: `env` (endpoint, token, buffer
  capacity, and flush timeout),
  `repo-allow` (repository allowlist), and an optional `ca.crt`. These are the binary's
  to write — don't hand-edit them.

Event coverage is harness-specific:

| Harness | Skill execution | Command invocation | MCP tool execution |
| --- | --- | --- | --- |
| Claude Code | Supported | Supported | Supported |
| Cline | Supported | Not supported | Not supported |
| Codex | Supported | Not supported | Supported |
| Cursor | Supported | Not supported | Supported |

Ordinary built-in tools are not collected. Only Claude Code emits
`command_invoked`. Do not report a missing event type as a failure when the
harness does not expose it.

## Calling the binary

The hooks invoke the binary by its bare name (`ai-agent-telemetry ingest …`), so the binary
lives on `PATH` at `~/.local/bin/ai-agent-telemetry` (`~/.local/bin\ai-agent-telemetry.exe` on
Windows) — the uniform install location on every OS. The installer in
`references/deployment.md` puts it there and adds `~/.local/bin` to `PATH`.

**Call the binary by its bare name first — `ai-agent-telemetry <cmd>` — and escalate only by what
fails.** Bare name is the one form that works everywhere that matters: it is the only command
shape the Codex execpolicy sandbox lets out (a path as `argv[0]` stays sandboxed), and it
resolves on Claude Code, Cline, and Cursor whenever `~/.local/bin` is already on `PATH` — the normal
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
- **Cline in VS Code** — fully quit every VS Code window and reopen VS Code. Reloading the window
  is not enough when the extension host inherited an old `PATH`.
- **Terminal / CLI** — end the session and **close the terminal tab or window**, then open a new
  one. Reopening in the same tab can keep the stale environment; a fresh tab is the safe move.

How to confirm the restart actually took: after it, `ai-agent-telemetry` resolves by bare name
(`Get-Command ai-agent-telemetry` / `command -v ai-agent-telemetry` succeeds). A fresh skill run
must then advance `last_flush_attempt` or increase `buffered` without a delivery error. If the bare
name still does not resolve, the process was not truly recreated — repeat the full quit or
close-the-tab step.

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

1. Ensure the binary is installed by using the installer in
   `references/deployment.md`, then run `status --verbose` by bare name.
2. Fix each configuration or delivery gap that status reports.
3. Run `selftest`. Re-run `status --verbose` and `selftest` after each fix until
   the collector accepts the probe and removes it from the outbox.
4. Repair and verify native global hooks. If Codex is a target, verify its
   CLI-managed execution-policy rule. Require a full harness restart.
5. Verify one real harness event by following `Verify delivery`.
6. Report the outcome without exposing configuration secrets.
7. Stop after reporting configuration and delivery results. Do not run `update` as part of this
   workflow; updating is a separate, explicit, mutating operation.

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
  has it. Run `ai-agent-telemetry configure --endpoint=<url>`. When no repository allowlist
  file exists yet, this also writes the default `github.com/Netcracker/*,*netcracker*/**`
  scope to `repo-allow`.
- **Repository scope wrong** — `status` prints `repo_scope:`. The default is
  `github.com/Netcracker/*,*netcracker*/**`. The host pattern avoids naming a specific corporate
  host, but it can match an unrelated host with the same substring. If the user needs a different
  scope, run `ai-agent-telemetry configure --repo-allow '<pattern>'`; repeat the flag for more
  than one pattern. An explicit scope replaces both defaults.
- **CA needed** (`selftest` fails with a certificate / TLS error) — only self-signed or
  non-trusted-CA deployments need this; a publicly trusted or MDM-distributed CA needs nothing.
  Obtain the `.crt` (`references/deployment.md` covers a local cluster and a corporate PKI) and
  run `ai-agent-telemetry configure --ca=<path>`; the binary copies it to `ca.crt`.
- **Token required** (collector returns 401 / 403) — have the user type it into the binary's
  no-echo prompt: run `ai-agent-telemetry configure` and let them enter the token when asked.
  Don't ask the user to paste the token to you, and don't type it yourself — anything in this
  conversation becomes part of the model's context and would leak the secret.
- **Outbox capacity or flush timeout needs tuning** — run
  `ai-agent-telemetry configure --buffer-cap=<events> --flush-timeout=<duration>` with positive
  values. The defaults are `100` and `2s`. Confirm effective values with
  `ai-agent-telemetry status --verbose`.

## Updating

Updating is separate from configuration and verification. Never run an update automatically at
the end of this skill. When the user explicitly asks to update the installation, use the unified
lifecycle command:

```sh
ai-agent-telemetry update
```

This updates the managed CLI and selected native telemetry hooks while preserving saved telemetry state. `update`
performs verified release downloads and mutates the installation, so run it only for an explicit update request. In
Codex, the execution-policy rule deliberately leaves `update`
sandboxed. Run it in a regular terminal or request permission to leave the sandbox. On Windows,
the lifecycle hands control to the verified new image before replacing the managed executable.

### Legacy APM migration during an explicit update

Only handle this branch when the explicitly requested `ai-agent-telemetry update` stops with
`legacy telemetry APM migration failed`. If it says `apm was not found on PATH`, stop. Explain that
APM must be installed or restored on `PATH`; do not install or repair it automatically. First confirm
the executable is available in the current shell:

- POSIX or WSL: `command -v apm`
- PowerShell: `Get-Command apm`

After `apm` is available, remove only the exact legacy telemetry dependency, retry the one update,
then verify the native hooks:

```sh
apm uninstall -g Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry
ai-agent-telemetry update
ai-agent-telemetry status --verbose
```

Do not run `ai-agent-telemetry hooks install` manually. Do not remove unrelated global packages or
edit a project-local manifest.

## Failure → fix

| `status` / `selftest` shows | Cause | Fix |
| --- | --- | --- |
| binary not found | not installed yet | run the installer one-liner (also puts `~/.local/bin` on `PATH`) |
| binary present but stale (`version` wrong) | the managed CLI needs replacement | run `ai-agent-telemetry update` after an explicit update request (see "Updating") |
| binary present but won't run | the installed CLI cannot replace itself | run the release bootstrap in [references/deployment.md](references/deployment.md) |
| bare name not found on a real skill run | `PATH` not refreshed yet | restart the agent so the hook resolves the binary — a *full* restart (quit the app / close the terminal tab), not a new chat (see "Calling the binary") |
| endpoint empty | not configured | `configure --endpoint` |
| TLS verification failed | CA missing or wrong | `configure --ca` |
| connection refused / timeout | network or VPN | confirm the user can reach the collector host |
| 401 / 403 | token missing or rejected | `configure`, enter the token at the no-echo prompt |
| spool growing, flush failing | one of the above | fix the reported cause, then `selftest` |
| `selftest` passes but real skill runs send nothing | the global hook is missing, invalid, or not reloaded | run `hooks install`, inspect `status --verbose`, and fully restart the harness |
| **Cline only:** hook is `outdated` | its bytes exactly match a supported legacy template embedded in the CLI | run the exact legacy replacement procedure under "Confirm the global hooks" |
| **Cline only:** hook is `invalid` and the detail reports only a wrong mode | the current managed template has incorrect POSIX permissions | run `hooks install --target=cline`, then require `cline: installed` |
| **Cline only:** any other `invalid` state or install conflict | the `PostToolUse` entry is modified, unrelated, or not a regular file | preserve it; do not uninstall, overwrite, or infer ownership from script text |
| **Cursor only:** hook is missing or invalid | `.cursor/hooks.json` is absent, malformed, or structurally incompatible | run `hooks install --target=cursor`; repair malformed JSON manually only after reviewing the reported path |
| **Codex UI shows an old hook command** | the global file is stale or Codex has not reviewed the changed command | run `hooks install --target=codex`, inspect the displayed command, approve it if prompted, and fully restart Codex |
| **Codex only:** `status` / `selftest` report `endpoint: (unset)` / `not configured` | Codex sandbox hides `~/.config` and blocks egress — not a missing configuration | run `hooks install --target=codex`, then restart Codex (see [references/codex-sandbox.md](references/codex-sandbox.md)) |
| **Codex false negative:** same `not configured` symptom, but you called the binary by full path or wrapper | that invocation does not match the execpolicy rule, so it ran sandboxed | re-test with `ai-agent-telemetry status` / `ai-agent-telemetry selftest`; don't diagnose from the unmatched call |

`selftest` prints the raw send error (for example an `x509` / `tls` message or an HTTP status);
map it to a cause above. `status` shows the spool backlog and the configured/not verdict but
does not itself test the network.

## Confirm the global hooks

`selftest` proves delivery but does not prove that a harness registered or loaded its hook. Repair
the CLI-managed global hooks, then read their detailed state:

```sh
ai-agent-telemetry hooks install
ai-agent-telemetry status --verbose
```

`hooks install` is noninteractive and does not read or change the endpoint, token, CA, or
repository policy. `status --verbose` reports `installed`, `missing`, `outdated`, or `invalid`, plus the native
path and diagnostic when available:

| Harness | Active hook file |
| --- | --- |
| Claude Code | `~/.claude/settings.json` |
| Cline on macOS and Linux | `~/Documents/Cline/Hooks/PostToolUse` |
| Cline on Windows | `~/Documents/Cline/Hooks/PostToolUse.ps1` |
| Codex | `~/.codex/hooks.json` |
| Cursor | `~/.cursor/hooks.json` |

- Claude Code requires `PreToolUse`/`Skill`, `UserPromptExpansion`, and
  `PostToolUse` plus `PostToolUseFailure`/`mcp__.*`.
- Cline requires its single global `PostToolUse` file. The CLI owns only its
  exact managed content. It reports an exact supported legacy template as
  `outdated`, but preserves modified or unrelated files and symlinks as
  conflicts. Do not merge or replace them automatically.
- Codex requires `Stop` and `PostToolUse`/`mcp__.*`.
- Cursor requires `afterAgentResponse`, `afterMCPExecution`, and a numeric
  top-level `version`.

These registrations collect only the event subset listed in the capability
matrix. The Codex target also requires
`~/.codex/rules/ai-agent-telemetry.rules`.

A malformed or structurally incompatible file is left byte-for-byte unchanged. Review the path
and error before repairing user-owned JSON, then rerun the two commands above.

### Cline legacy hook replacement

Do not ask the user to identify a legacy template and do not inspect shell or PowerShell syntax to make this decision.
The CLI embeds the supported legacy templates and reports `cline: outdated` only after an exact byte comparison. When
`status --verbose` reports that state, run:

```sh
ai-agent-telemetry hooks uninstall --target=cline
ai-agent-telemetry hooks install --target=cline
ai-agent-telemetry status --verbose
```

Require the final Cline state to be `installed`. The first command is safe in this branch because the CLI repeats its
exact ownership check before deletion. If `invalid` reports only `mode is ..., want ...`, run
`ai-agent-telemetry hooks install --target=cline`; the CLI reached that diagnostic only after matching the current
managed bytes and can repair the mode safely. For every other `invalid` state, generic install conflict, or preserved
uninstall, stop without editing or deleting the entry. Direct the user to the
[Cline conflict guide](https://github.com/Netcracker/qubership-ai-agent-telemetry/blob/main/docs/manual-uninstall.md).
Never use `rm`, `Remove-Item`, a content search, or visual similarity as a substitute for the CLI's `outdated` state.

### Codex changed-command review

The CLI owns registration, not Codex's private trust decisions. When a command changes, do not
delete or rewrite trust hashes automatically. Install the canonical global hook, then ask the user
to inspect and approve the exact command if Codex prompts:

```sh
ai-agent-telemetry hooks install --target=codex
ai-agent-telemetry status --verbose
```

The approved command is `ai-agent-telemetry ingest --agent=codex`. Fully restart Codex after the
review. A new chat is not enough.

## Codex sandbox rule (check)

**Only when Codex is one of the targets on this machine** — skip it otherwise.

`hooks install` manages the execution-policy rule together with the Codex hook. Run:

```sh
ai-agent-telemetry hooks install --target=codex
ai-agent-telemetry status --verbose
```

After restarting Codex, run `ai-agent-telemetry status` and `ai-agent-telemetry selftest` by bare
name. If either command reports missing configuration only inside Codex, follow
[the sandbox troubleshooting reference](references/codex-sandbox.md).

For a direct syntax check, pass the command as separate arguments:

```sh
codex execpolicy check --rules ~/.codex/rules/ai-agent-telemetry.rules \
  ai-agent-telemetry ingest --agent=codex --pretty
```

## Verify delivery

### Level 1: installation and transport

Run `status --verbose`, then `selftest`. Require all of these results:

- the CLI reports `state: configured`;
- the current harness hook reports `installed`;
- diagnostics contain no delivery error;
- the collector accepts the probe and the probe leaves the outbox.

A nonzero `buffered` value is not automatically a failure. Treat a growing
buffer together with a delivery error as a failure. Fix that error before
continuing.

### Level 2: real harness event

The current `ai-agent-telemetry-configure` invocation is the skill test event.
Record `buffered`, `last_flush_attempt`, and delivery diagnostics after the
level 1 selftest.

Claude Code emits the skill event before this skill runs. Cline emits it from
`PostToolUse` immediately after `use_skill`, before the skill instructions run.
The level 1 `selftest` flushes either queued event. Without telemetry-store read
access, the available evidence for Claude Code and Cline is the installed native
hook plus passing transport verification. Offer an optional server-side query
for proof of the individual skill event.

Codex and Cursor run their skill-detection hook after the response, so ask the
user to send one more telemetry-check message after this response. On that
next turn, run `status --verbose` again. Accept either outcome:

- `last_flush_attempt` advanced, no new delivery error appeared, and the
  buffer did not grow because of a failed send; or
- `buffered` increased above the post-level-1 baseline with no delivery error,
  which proves that batching queued an event. Run `selftest` to force the full
  outbox to flush, then require `buffered` to return to the recorded baseline
  (normally zero) with no delivery error.

If `last_flush_attempt` did not advance and `buffered` did not grow, no harness
event is observable. Troubleshoot the native hook and full harness restart.

If the user already has read access to the telemetry store, offer a
server-side query as additional evidence. Do not request store credentials in
the conversation. Store access is optional.

For Cline, set the VictoriaLogs time range to start just before this invocation,
then run this exact query:

```logsql
service.name:="ai-agent-telemetry" agent:="cline" _msg:="skill_executed" skill.name:="ai-agent-telemetry-configure"
```

Individual-event proof requires a matching record in that current-run time
range. Do not count older matches as proof of the current run.

Test MCP telemetry only with a read-only tool that is already configured and
appropriate for the user's request. Never mutate external state solely to
create a telemetry event. Test `command_invoked` only in Claude Code and only
with an available harmless slash command.

Do not report success until level 1 passes and the native hook is installed.
For a requested Codex or Cursor real-event test, do not report that part as
complete until one follow-up outcome passes. For Claude Code or Cline, do not
claim individual-event proof without a successful store query.
