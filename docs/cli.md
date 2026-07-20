# The ai-agent-telemetry CLI

The `ai-agent-telemetry` CLI is the local component that detects telemetry events, buffers
events to a local outbox, and forwards them to the collector over OTLP/HTTPS. It runs
from the agent hook on each turn — there is no daemon and no background process.

To install it, see [Installation in the README](../README.md#installation). This document
covers the command reference, how it works inside, and where it keeps its files.

For the usual setup, run the platform installer and enter collector details only if prompted.
It registers Claude Code, Codex, and Cursor automatically. The commands below are primarily for
customization, diagnostics, and repair.

## What it does

On each hook run the CLI:

1. reads the agent's hook payload from stdin and routes it by `hook_event_name` (see
   [Agent integration](agent-integration.md));
2. normalizes supported skill, command, and MCP signals into typed events (see
   [Data](../README.md#data));
3. writes each record to the on-disk outbox;
4. opportunistically flushes the outbox to the collector over OTLP/HTTPS.

## Subcommands

The hook calls `ingest`. The installer configures a new machine and refreshes hooks during
upgrades, so these commands rarely need to be run by hand.
`ai-agent-telemetry <command>`:

| Command | Purpose |
| --- | --- |
| `configure` | Write the per-machine endpoint, repository policy, optional CA, and optional token. Install all global hooks by default; use `--hooks=all`, `--hooks=none`, or `--hooks=<list>`. |
| `hooks install` | Install or repair global hooks and required harness policy files without changing collector configuration. Use `--target=<list>` to select harnesses. |
| `status` | Read-only check of configuration, delivery backlog, and each global hook. Sends nothing. Use `--verbose` for native paths and parse errors. |
| `selftest` | Send one marked probe event and report whether the collector accepted it and it left the outbox. |
| `ingest` | Read a harness hook payload, route it by `hook_event_name`, validate and queue supported events, then flush opportunistically. Always exits 0 so it never fails an agent turn. |
| `flush` | Send queued events to the collector and delete each on success. |
| `update-check` | Compare the installed version against the latest GitHub release and print `installed:` / `latest:` / `update_available: yes\|no\|unknown`. Network, short timeout, always exits 0 — advisory only. |
| `self-update` | Download the latest release asset for this OS and architecture, verify it against `SHA256SUMS`, and replace the running binary. |
| `version` | Print the build version. |

Use the built-in help to see a command's current syntax and options:

```sh
ai-agent-telemetry --help
ai-agent-telemetry help configure
ai-agent-telemetry hooks --help
```

Explicit help prints the requested information and exits without running the command.

When buffered events remain after a failed delivery attempt, `status` points to
`status --verbose`. The verbose output includes the effective buffer capacity, flush
timeout, and last recorded delivery error.

## Global hooks

The CLI manages one user-level native file per harness on every supported operating system:

| Harness | File | Registration |
| --- | --- | --- |
| Claude Code | `~/.claude/settings.json` | `PreToolUse`/`Skill`, `UserPromptExpansion`, `PostToolUse`/`mcp__.*`, `PostToolUseFailure`/`mcp__.*` |
| Codex | `~/.codex/hooks.json`, `~/.codex/rules/ai-agent-telemetry.rules` | `Stop`, `PostToolUse`/`mcp__.*`, and the execution policy |
| Cursor | `~/.cursor/hooks.json` | `afterAgentResponse`, `afterMCPExecution`, and numeric top-level `version` |

Normal installation registers all three hooks; no separate hook command is required. For a custom
target list, `configure` accepts `--hooks=all`, `--hooks=none`, or a comma-separated subset. The
repair command uses `--target` instead:

```sh
ai-agent-telemetry configure --hooks=claude,codex
ai-agent-telemetry hooks install --target=claude,codex
```

Before either command installs a nonempty set of CLI-managed hooks, the CLI reads `~/.apm/apm.yml`. If the manifest
contains the exact legacy telemetry hook package dependency, the CLI asks APM to remove that global dependency. It
does not edit repository-local APM manifests or remove the retained compatibility package from a project.

Cleanup is best effort. If the global manifest cannot be read or parsed, APM is unavailable, or the uninstall command
fails, the CLI writes a warning to `stderr` and continues canonicalizing every requested native hook. A cleanup warning
does not affect the exit code: the command succeeds when configuration and hook installation succeed.

Hook updates preserve unrelated top-level fields, events, matcher groups, handlers, and unknown
extension fields. They canonicalize only recognized telemetry entries and remove duplicate owned
entries. Repeating an installation produces no further JSON changes.

If a file contains malformed JSON or an incompatible native structure, the CLI leaves it
byte-for-byte unchanged and reports that target as failed. It continues with the other selected
targets and returns a nonzero exit code after reporting every failure.

`configure` writes machine configuration before it attempts cleanup and hook installation. A cleanup warning does not
roll back the configuration. If a later hook installation fails, the configuration remains written and `configure`
returns exit code `1`.

`status` reports `installed`, `missing`, or `invalid` for each harness. It verifies registration
and required policy files, not execution or trust. `selftest` verifies collector delivery, not hook
registration.

If installation or hook refresh changed the Codex hook definition and hash, fully restart Codex. The CLI does not edit
Codex's private trust state, so inspect and approve exactly `ai-agent-telemetry ingest --agent=codex` if prompted.

## Event routing and validation

After selecting the adapter with `--agent`, `ingest` routes only by `hook_event_name`. Unsupported hooks, malformed
JSON, missing required fields, invalid identifiers, and unsupported enum values produce no event. A legacy Claude Code
payload without `hook_event_name` is treated as `PreToolUse` only when `tool_name` is `Skill`. The CLI does not infer
other event types from payload fields.

Hook ingestion is fail-open: it reports local enqueue or flush errors to stderr but always exits 0. An invalid event
therefore cannot fail an agent turn or enter the outbox. Collector failures leave valid buffered files for retry.

The external identifier profiles and excluded content fields are listed in [Data](../README.md#data). Adapters retain
only the typed allowlist for each event and omit unavailable optional values. They do not trim, truncate, rewrite, or
infer identifiers.

## Updating

`update-check` reports whether a newer release exists; it does not apply anything. To update,
run:

```sh
ai-agent-telemetry self-update
```

`self-update` fetches the release asset that matches the current `GOOS/GOARCH`, verifies the
download, and replaces the executable returned by `os.Executable()`. On Windows the replacement
finishes after the command exits because Windows does not allow overwriting a running `.exe`.

You can also force a reinstall through the installer:

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.sh | sh -s -- --force
iex "& { $(irm https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1) } -Force"
```

Re-running the installer updates the binary and refreshes the global hooks without asking for
collector settings again. The skip-config options leave both machine settings and hooks unchanged.

## Buffering and delivery

The CLI never blocks an agent turn on the network. `ingest` writes events to the outbox
and returns; delivery happens opportunistically.

- **Outbox.** One JSON file per buffered event in a per-machine outbox directory. A failed
  send leaves the file in place to retry on a later run. The last delivery error is kept
  beside the outbox and appears in `status --verbose`.
- **Delivery identity.** Each new outbox entry stores a time-sortable UUID v7. Every retry exports
  the same value as `event.id`.
- **Flush lock.** `flush` takes a non-blocking advisory lock (`.flush.lock`) on the
  outbox, so two concurrent runs never send the same event twice. A run that finds the
  lock held skips quietly.
- **Offset dedup.** On harnesses detected from the transcript (Codex, Cursor), the CLI
  stores a per-session byte offset into the transcript and reads only the lines written
  since the previous run. The key is namespaced per harness (`codex:<session>`), so
  different harnesses do not collide and one skill run counts once.

New outbox writers use schema version `1`, a typed top-level envelope, and one direct event-specific `payload` object:

```json
{
  "schema_version": 1,
  "event_name": "command_invoked",
  "event_id": "019f6aec-41fb-7abc-8def-0123456789ab",
  "agent": "claude",
  "session_id": "session-123",
  "repo_remote": "github.com/netcracker/project",
  "ts": "2026-07-16T12:34:56.123456789Z",
  "payload": {
    "command_name": "review-pr",
    "command_source": "plugin",
    "expansion_type": "slash_command"
  }
}
```

The envelope rejects unknown fields, duplicate fields, explicit `null` values, unknown versions or event names, and a
payload that does not match `event_name`. The optional `event_id` remains readable for buffered version 1 entries
created before delivery IDs were introduced. Optional `repo_remote`, MCP server, and MCP duration fields are omitted
when unavailable.

Readers remain compatible with the unversioned skill shape containing top-level `agent`, `session_id`, optional
`repo_remote`, `skill`, and `ts`. They map it to `skill_executed` in memory and apply the version 1 validation rules.
Writers never emit the legacy shape, and no eager migration rewrites existing files. In a mixed batch, invalid or
unreadable files remain buffered while valid legacy and version 1 files continue to flush in filename order.

Older entries without an ID receive a stable UUID v7 fallback. It combines the persisted event timestamp with a random
portion derived from the opaque outbox filename. A malformed stored ID is replaced by the same mechanism rather than
exported. VictoriaLogs does not deduplicate automatically by `event.id`; backend processing or analytics queries must
use it to collapse repeat deliveries.

`selftest` uses a reserved internal exception: `agent=selftest`, event `skill_executed`, skill `__selftest__`, a
generated UUID v4 session, and no repository value. No harness adapter can create this pair. The probe bypasses
repository policy because it tests machine delivery, and legacy readers recognize the same exact reserved pair.

The outbox retains at most 100 events by default, and an ordinary flush has a 2-second
timeout. Configure persistent overrides with positive values:

```sh
ai-agent-telemetry configure --buffer-cap=1000 --flush-timeout=30s
```

The CLI stores these settings as `AI_AGENT_TELEMETRY_BUFFER_CAP` and
`AI_AGENT_TELEMETRY_FLUSH_TIMEOUT` in the machine `env` file. A process environment
variable overrides the saved value. An invalid runtime value produces a warning and uses
the corresponding default instead; `configure` rejects the same invalid value without
changing machine configuration. The timeout accepts Go duration syntax such as `500ms`,
`30s`, or `1m`.

Use temporary process overrides for scripts and CI:

```sh
AI_AGENT_TELEMETRY_BUFFER_CAP=1000 \
AI_AGENT_TELEMETRY_FLUSH_TIMEOUT=30s \
ai-agent-telemetry flush
```

Run `ai-agent-telemetry status --verbose` to inspect the effective values. Compact status
output omits them.

## Repository scope

Set repository scope in `repo-allow`, or write it through repeatable `configure --repo-allow`,
to collect only from organization repositories. When the file is absent, the built-in
`github.com/Netcracker/*` default applies; `configure` writes that default to `repo-allow`:

```sh
ai-agent-telemetry configure \
  --repo-allow 'github.com/Netcracker/*' \
  --repo-allow 'github.com/Qubership/*' \
  --repo-allow 'gitlab.company.com/qubership/**'
```

The comma-separated form is also supported. Use the environment variable as an override for
scripts and CI:

```sh
AI_AGENT_TELEMETRY_REPO_ALLOW=github.com/Netcracker/*,github.com/Qubership/*
ai-agent-telemetry configure --repo-allow='github.com/Netcracker/*,github.com/Qubership/*'
```

Patterns are matched against normalized, lowercase git remote identities such as
`github.com/netcracker/repo` or `gitlab.company.com/qubership/platform/service`. `*`
matches one path segment; `**` matches nested GitLab groups. When the hook runs from a
fork, the CLI checks every git remote in the working tree, so `origin` can be a personal
fork while `upstream` points to an allowed organization repository. Telemetry records the
matching organization remote instead of the personal fork remote.

Policy precedence is deliberate: `AI_AGENT_TELEMETRY_DISABLED` stops collection first;
then the `AI_AGENT_TELEMETRY_REPO_ALLOW` environment variable overrides the configured
scope; then `repo-allow` scopes the remaining events. If no repository policy is configured,
the built-in `github.com/Netcracker/*` default applies.

## Transport and security

Delivery is OTLP/HTTP, over HTTPS only. The CLI never falls back to plaintext and never
skips certificate verification; a TLS failure keeps the event in the outbox rather than
downgrading. When a private CA is provisioned, the CLI appends it to the system trust
pool — trust stays additive — so a self-signed collector works without replacing the
system roots. A per-machine access token is optional: when provisioned, the CLI sends it
as an `Authorization: Bearer` header; without one, the request carries no auth header.

## File layout

The CLI splits durable state from disposable state. Both roots are **uniform XDG-style
paths on every OS** — the same philosophy as the binary's `~/.local/bin` — rather than the
per-OS `os.UserConfigDir()` / `os.UserCacheDir()` locations. The reasoning is in
[the config-dir decision](adr/0003-config-cache-dirs-xdg.md).

| Location | Path | Holds |
| --- | --- | --- |
| **Binary** (on `PATH`) | `~/.local/bin/ai-agent-telemetry` (`.exe` on Windows) | the CLI itself, placed there by the installer so the hook resolves it by bare name |
| **Config** (durable) | `$XDG_CONFIG_HOME` else `~/.config/ai-agent-telemetry/` | `env` (endpoint, token, and delivery settings), `repo-allow` (repository allowlist), `ca.crt` (optional private CA), `machine-id` (anonymous install UUID) |
| **Cache** (disposable) | `$XDG_CACHE_HOME` else `~/.cache/ai-agent-telemetry/` | `outbox/` (one JSON file per event, plus `.lastflush`, `.last_delivery_error`, and `.flush.lock`), `offsets/` (per-session transcript offsets) |
| **Claude Code hook** | `~/.claude/settings.json` | Global `PreToolUse`/`Skill`, `UserPromptExpansion`, `PostToolUse`/`mcp__.*`, and `PostToolUseFailure`/`mcp__.*` registrations merged with unrelated settings |
| **Codex hook** | `~/.codex/hooks.json` | Global `Stop` and `PostToolUse`/`mcp__.*` registrations merged with unrelated hooks |
| **Cursor hook** | `~/.cursor/hooks.json` | Global `afterAgentResponse` and `afterMCPExecution` registrations, plus numeric `version` |

All three are the same path on every OS, including Windows (`%USERPROFILE%\.config\…`,
`%USERPROFILE%\.cache\…`). This is deliberate: `os.UserConfigDir()` returns `%AppData%` on
Windows, which MSIX **virtualizes** for a packaged harness (Claude Desktop), so a packaged
and a plain shell would resolve different config dirs and silently diverge. A home-relative
path outside `AppData` is never virtualized, so every harness shares one config — the same
reason `~/.local/bin\ai-agent-telemetry.exe` already works for all harnesses. Config holds
anything that must survive — losing it stops telemetry — so the token and endpoint never live
in the cache, which the OS may purge under disk pressure.
