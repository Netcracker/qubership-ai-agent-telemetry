# ai-agent-telemetry-configure

This package delivers the setup skill and bootstrap scripts for the
`ai-agent-telemetry` CLI. The setup skill provisions per-machine config and
verifies delivery.

Supported agents: Codex, Claude Code, and Cursor. An OpenCode adapter is
follow-up work.

## Install

Install the APM CLI first ([uv](https://docs.astral.sh/uv/):
`uv tool install apm-cli`), then add the package one of two ways.

Via the APM command:

```sh
apm install --dev Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry-configure
```

Or add the dependency to your `apm.yml`, pinned to a tag from the
[Releases](https://github.com/Netcracker/qubership-ai-agent-telemetry/releases) page:

```yaml
devDependencies:
  apm:
    - Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry-configure
```

Then install for your agent — `--target` is one of `codex`, `claude`, `cursor`, or
`all`:

```sh
apm install --target all
```

`apm install` deploys the skill, the hooks, and the skill trigger. On Claude Code that
is enough. Codex and other agents that read `AGENTS.md` additionally need
`apm compile --target codex` to register the trigger.

Restart your agent and ask it to "set up skills telemetry". The bundled setup
skill writes the per-machine config and verifies delivery. Installing is the
consent boundary — nothing is sent until you run the setup skill.

## How it works

On each turn the agent fires the hook the package registered, and the hook runs
the CLI by its bare name as `ai-agent-telemetry ingest --agent=<agent>`. The CLI
detects the skill from the agent's payload — a native hook event where the agent
emits one (Claude Code), the session transcript where it does not (Codex, Cursor).

The hook resolves the binary from `PATH`, so it must be installed there first. The
installer (`bootstrap.sh` on macOS/Linux, `bootstrap.ps1` on Windows) fetches the
pinned `ai-agent-telemetry` Go binary into `~/.local/bin` and adds that directory to
`PATH` — the one-time step the setup skill runs. `ingest` reads the hook payload,
normalizes the event, and writes it to a machine-global outbox. The same run
opportunistically flushes buffered events to the collector over OTLP/HTTPS — there
is no daemon.

## Configuration

The CLI reads its collector settings from the environment or the provisioned
`env` file under the config dir, delivered per machine out of band (never git):

- `AI_AGENT_TELEMETRY_ENDPOINT` — the OTLP/HTTP collector URL, for example
  `https://collector.example/v1/logs`. Without it the flush is a no-op, so events
  stay buffered in the outbox.
- `AI_AGENT_TELEMETRY_TOKEN` — the optional bearer token, sent as
  `Authorization: Bearer`. Without it the request carries no auth header.

A private CA is optional: place `ca.crt` in the config dir and the CLI appends it
to the system trust pool. The setup skill writes all of this for you.

## Release

Binaries are built and published by the `Release` GitHub Actions workflow, not on
a local machine. Run it from the Actions tab (or with the GitHub CLI) and pass the
version; the corporate chain creates the tag, so do not push the tag by hand:

```bash
gh workflow run release.yaml -f version=vX.Y.Z
```

The workflow cross-compiles six targets (darwin, linux, and windows, each for
amd64 and arm64), writes `SHA256SUMS`, and attaches every artifact to a GitHub
Release. `bootstrap.sh` and `bootstrap.ps1` download
`ai-agent-telemetry-<os>-<arch>` from that release; the workflow stamps
`BINARY_VERSION` in both scripts to match the tag.
