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

The hook calls `ingest`; the setup skill calls the rest, so you rarely run them by hand.
`ai-agent-telemetry <command>`:

| Command | Purpose |
| --- | --- |
| `configure` | Write the per-machine config: collector endpoint, repository allowlist (`--repo-allow=<patterns>`, default `github.com/Netcracker/*` when unset), optional CA certificate (`--ca=<path>`), and an optional token read without echo. Idempotent. |
| `status` | Read-only check: build version, config directory, endpoint, repository scope, whether a CA is present, outbox backlog, last flush attempt, and a configured verdict. Sends nothing. |
| `selftest` | Send one marked probe event and report whether the collector accepted it and it left the outbox. |
| `ingest` | The hook path: read an agent hook payload on stdin, detect skill use (on Codex the `SKILL.md` reads in the session rollout; on Claude Code the `Skill` tool name in the `PreToolUse` payload; on Cursor the `SKILL.md` reads in the `afterAgentResponse` transcript), queue the events, and flush opportunistically. Always exits 0 so it never fails an agent turn. |
| `flush` | Send queued events to the collector and delete each on success. |
| `update-check` | Compare the installed version against the latest GitHub release and print `installed:` / `latest:` / `update_available: yes\|no\|unknown`. Network, short timeout, always exits 0 — advisory only. |
| `version` | Print the build version. |

## Updating

`update-check` reports whether a newer release exists; it does not apply anything. To update,
re-run the latest installer with `--force` — it always pins the latest binary, so a forced
reinstall replaces the old one (after the same checksum verification as a first install):

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/bootstrap.sh | sh -s -- --force   # macOS/Linux
iex "& { $(irm https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/bootstrap.ps1) } --force"      # Windows
```

These are the building blocks for an update prompt; wiring a trigger (for example a periodic
check that offers the update) is not implemented yet.

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

Set `AI_AGENT_TELEMETRY_REPO_ALLOW` in the config file, or write it through
`configure --repo-allow=`, to collect only from organization repositories. When the
setting is absent, `configure` writes `github.com/Netcracker/*` by default:

```sh
ai-agent-telemetry configure --repo-allow='github.com/Netcracker/*,github.com/Qubership/*,gitlab.company.com/qubership/**'
```

Patterns are matched against normalized, lowercase git remote identities such as
`github.com/netcracker/repo` or `gitlab.company.com/qubership/platform/service`. `*`
matches one path segment; `**` matches nested GitLab groups. When the hook runs from a
fork, the CLI checks every git remote in the working tree, so `origin` can be a personal
fork while `upstream` points to an allowed organization repository. Telemetry records the
matching organization remote instead of the personal fork remote.

Policy precedence is deliberate: `AI_AGENT_TELEMETRY_DISABLED` stops collection first;
then local `git config --local ai-agent-telemetry.enabled true|false` opts one checkout
in or out; then `AI_AGENT_TELEMETRY_REPO_ALLOW` scopes the remaining events. If the
allowlist is absent because the config was written manually, the CLI records every
repository where the hook runs.

Use local git config for explicit per-repository overrides:

```sh
git config --local ai-agent-telemetry.enabled false
git config --local ai-agent-telemetry.enabled true
```

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
[the config-dir decision](superpowers/decisions/2026-06-23-config-cache-dir-xdg-msix.md).

| Location | Path | Holds |
| --- | --- | --- |
| **Binary** (on `PATH`) | `~/.local/bin/ai-agent-telemetry` (`.exe` on Windows) | the CLI itself, placed there by the setup skill so the hook resolves it by bare name |
| **Config** (durable) | `$XDG_CONFIG_HOME` else `~/.config/ai-agent-telemetry/` | `env` (endpoint, token, repository allowlist), `ca.crt` (optional private CA), `machine-id` (anonymous install UUID) |
| **Cache** (disposable) | `$XDG_CACHE_HOME` else `~/.cache/ai-agent-telemetry/` | `outbox/` (one JSON file per event, plus `.lastflush` and `.flush.lock`), `offsets/` (per-session transcript offsets) |

All three are the same path on every OS, including Windows (`%USERPROFILE%\.config\…`,
`%USERPROFILE%\.cache\…`). This is deliberate: `os.UserConfigDir()` returns `%AppData%` on
Windows, which MSIX **virtualizes** for a packaged harness (Claude Desktop), so a packaged
and a plain shell would resolve different config dirs and silently diverge. A home-relative
path outside `AppData` is never virtualized, so every harness shares one config — the same
reason `~/.local/bin\ai-agent-telemetry.exe` already works for all harnesses. Config holds
anything that must survive — losing it stops telemetry — so the token and endpoint never live
in the cache, which the OS may purge under disk pressure.
