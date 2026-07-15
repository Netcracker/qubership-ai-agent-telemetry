# ai-agent-telemetry-configure

This package delivers the optional setup, repair, and verification skill for the
`ai-agent-telemetry` CLI. The standalone installers handle binary installation,
configuration, and global hook registration; this package provides agent-guided diagnosis.

Most users only need the standalone installer. Add this package when you want the agent to guide
repair or collector CA troubleshooting.

Supported agents: Codex, Claude Code, and Cursor. An OpenCode adapter is
follow-up work.

## Install

Install the APM CLI first ([uv](https://docs.astral.sh/uv/):
`uv tool install apm-cli`), then add the package one of two ways.

Via the APM command. `--target` is required — without it APM cannot pick a harness and
the install fails. It is one of `claude`, `codex`, `cursor`, or `all`; the example
targets Claude Code:

```sh
apm install --dev Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry-configure --target claude
```

Or add the dependency to your `apm.yml`, pinned to a tag from the
[Releases](https://github.com/Netcracker/qubership-ai-agent-telemetry/releases) page:

```yaml
devDependencies:
  apm:
    - Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry-configure
```

Then install for your agent:

```sh
apm install --target claude
```

`apm install` deploys the skill and the skill trigger. On Claude Code that is
enough. Codex and other agents that read `AGENTS.md` additionally need
`apm compile --target codex` to register the trigger.

Restart your agent and ask it to "set up skills telemetry". The bundled setup skill reads
per-machine configuration and global hook status, repairs missing setup, and verifies delivery.

## How it works

On each turn a CLI-managed global hook runs the binary by its bare name as
`ai-agent-telemetry ingest --agent=<agent>`. The CLI detects the skill from a native hook
event where available (Claude Code), or from the session transcript (Codex and Cursor).

The standalone installer puts the binary on `PATH`, saves the machine configuration, and registers
all supported hooks. The skill diagnoses and repairs any gaps reported by the CLI. `ingest` writes
normalized events to a machine-global outbox and opportunistically flushes them over OTLP/HTTPS.
There is no daemon.

## Configuration

The CLI reads its collector settings from the environment or the provisioned
`env` and `repo-allow` files under the config dir, delivered per machine out of band
(never git):

- `AI_AGENT_TELEMETRY_ENDPOINT` — the OTLP/HTTP collector URL, for example
  `https://collector.example/v1/logs`. Without it the flush is a no-op, so events
  stay buffered in the outbox.
- `AI_AGENT_TELEMETRY_TOKEN` — the optional bearer token, sent as
  `Authorization: Bearer`. Without it the request carries no auth header.
- `AI_AGENT_TELEMETRY_BUFFER_CAP` — positive local outbox capacity. The default is `100`.
- `AI_AGENT_TELEMETRY_FLUSH_TIMEOUT` — positive ordinary flush timeout in Go duration syntax.
  The default is `2s`.
- `repo-allow` — repository allowlist, one glob per line. `configure` writes
  `github.com/Netcracker/*` by default when the file is absent; pass repeatable
  `--repo-allow <pattern>` values to use a different scope.
- `AI_AGENT_TELEMETRY_REPO_ALLOW` — optional environment override for scripts and CI.

Persist delivery overrides through the CLI instead of editing `env` directly:

```bash
ai-agent-telemetry configure --buffer-cap=1000 --flush-timeout=30s
ai-agent-telemetry status --verbose
```

Process environment variables override saved values. Invalid runtime values emit a warning
and use the corresponding default; `configure` rejects invalid values before writing files.

A private CA is optional: place `ca.crt` in the config dir and the CLI appends it
to the system trust pool. The setup skill can guide this repair when `selftest`
reports a TLS error.

## Release

Binaries are built and published by the `Release` GitHub Actions workflow, not on
a local machine. Run it from the Actions tab (or with the GitHub CLI) and pass the
version; the corporate chain creates the tag, so do not push the tag by hand:

```bash
gh workflow run release.yaml --ref main -f version=vX.Y.Z
```

The workflow cross-compiles six targets (darwin, linux, and windows, each for
amd64 and arm64), writes `SHA256SUMS`, and attaches every artifact to a GitHub
Release. `install.sh` and `install.ps1` download `ai-agent-telemetry-<os>-<arch>`
from that release. Compatibility copies named `bootstrap.sh` and `bootstrap.ps1`
are also published for older docs and automation.
