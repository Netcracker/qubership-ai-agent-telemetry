# The ai-agent-telemetry CLI

The `ai-agent-telemetry` CLI is the local component that detects skill use, buffers
events to a local outbox, and forwards them to the collector over OTLP/HTTPS. It runs
from the agent hook on each turn — there is no daemon and no background process.

To install it, see [Installation in the README](../README.md#installation). This document
covers the command reference, how it works inside, and where it keeps its files.

## What it does

On each hook run the CLI:

1. reads the agent's hook payload from stdin and detects any skill that ran (see
   [Agent integration](agent-integration.md));
2. normalizes the detection into one OpenTelemetry log record per skill run (see
   [Data](../README.md#data));
3. writes each record to the on-disk outbox;
4. opportunistically flushes the outbox to the collector over OTLP/HTTPS.

## Subcommands

The hook calls `ingest`; the installer can call `configure` on first run, and the setup skill
uses the diagnostic commands, so you rarely run them by hand.
`ai-agent-telemetry <command>`:

| Command | Purpose |
| --- | --- |
| `configure` | Write the per-machine config: collector endpoint, repository allowlist (repeatable `--repo-allow <pattern>`, default `github.com/Netcracker/*` when unset), optional CA certificate (`--ca=<path>`), and an optional token read without echo. Idempotent. |
| `status` | Read-only check: build version, config directory, endpoint, repository scope, whether a CA is present, outbox backlog, last flush attempt, and a configured verdict. Sends nothing. |
| `selftest` | Send one marked probe event and report whether the collector accepted it and it left the outbox. |
| `ingest` | The hook path: read an agent hook payload on stdin, detect skill use (on Codex the `SKILL.md` reads in the session rollout; on Claude Code the `Skill` tool name in the `PreToolUse` payload; on Cursor the `SKILL.md` reads in the `afterAgentResponse` transcript), queue the events, and flush opportunistically. Always exits 0 so it never fails an agent turn. |
| `flush` | Send queued events to the collector and delete each on success. |
| `update-check` | Compare the installed version against the latest GitHub release and print `installed:` / `latest:` / `update_available: yes\|no\|unknown`. Network, short timeout, always exits 0 — advisory only. |
| `self-update` | Download the latest release asset for this OS and architecture, verify it against `SHA256SUMS`, and replace the running binary. |
| `version` | Print the build version. |

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

## Buffering and delivery

The CLI never blocks an agent turn on the network. `ingest` writes events to the outbox
and returns; delivery happens opportunistically.

- **Outbox.** One JSON file per buffered event in a per-machine outbox directory. A failed
  send leaves the file in place to retry on a later run.
- **Flush lock.** `flush` takes a non-blocking advisory lock (`.flush.lock`) on the
  outbox, so two concurrent runs never send the same event twice. A run that finds the
  lock held skips quietly.
- **Offset dedup.** On harnesses detected from the transcript (Codex, Cursor), the CLI
  stores a per-session byte offset into the transcript and reads only the lines written
  since the previous run. The key is namespaced per harness (`codex:<session>`), so
  different harnesses do not collide and one skill run counts once.

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
| **Config** (durable) | `$XDG_CONFIG_HOME` else `~/.config/ai-agent-telemetry/` | `env` (endpoint, token), `repo-allow` (repository allowlist), `ca.crt` (optional private CA), `machine-id` (anonymous install UUID) |
| **Cache** (disposable) | `$XDG_CACHE_HOME` else `~/.cache/ai-agent-telemetry/` | `outbox/` (one JSON file per event, plus `.lastflush` and `.flush.lock`), `offsets/` (per-session transcript offsets) |

All three are the same path on every OS, including Windows (`%USERPROFILE%\.config\…`,
`%USERPROFILE%\.cache\…`). This is deliberate: `os.UserConfigDir()` returns `%AppData%` on
Windows, which MSIX **virtualizes** for a packaged harness (Claude Desktop), so a packaged
and a plain shell would resolve different config dirs and silently diverge. A home-relative
path outside `AppData` is never virtualized, so every harness shares one config — the same
reason `~/.local/bin\ai-agent-telemetry.exe` already works for all harnesses. Config holds
anything that must survive — losing it stops telemetry — so the token and endpoint never live
in the cache, which the OS may purge under disk pressure.
