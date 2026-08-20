# AGENTS.md

**Read [README.md](README.md) first**, then this file. The README is the entry point — what
the project does, how telemetry is turned on, the architecture, the data, and the backend
requirements. Deeper docs are linked from its Documentation section. This file holds only
what an agent needs beyond the README: orientation, conventions, and open work.

## Orientation

Skill-usage telemetry for AI coding agents. A skill run is detected per harness and sent to a
shared OpenTelemetry collector. The lifecycle installer manages machine-wide hooks, while the
repository allowlist limits collection to approved repositories.

- **Two packages.** A retained compatibility package with APM-managed hooks lives in
  [`Netcracker/qubership-ai-agent-telemetry`](https://github.com/Netcracker/qubership-ai-agent-telemetry/tree/main/agent-packages/ai-agent-telemetry)
  as `ai-agent-telemetry`. New installations use lifecycle-managed global hooks instead. The
  optional `ai-agent-telemetry-configure` package provides an agent-guided setup, repair, and
  verification skill.
- **Component:** the `ai-agent-telemetry` CLI — a small Go binary at the repository root
  (a flat `package main`, the "Basic command" layout from the Go module-layout guide). It
  detects the skill, buffers events to a local outbox, and flushes over OTLP/HTTPS. No
  daemon. See [docs/cli.md](docs/cli.md).
- **Invocation: hooks call the binary by its bare name on `PATH`.** The harness hooks and the
  `ai-agent-telemetry-configure` skill run `ai-agent-telemetry` directly — on a dev machine it lives at
  `~/.local/bin/ai-agent-telemetry`. A bare name is shell-agnostic, which is why it replaced the old
  per-turn `bootstrap.{sh,ps1}` hook wrapper (retired; see
  [docs/adr/0002-bare-binary-on-path.md](docs/adr/0002-bare-binary-on-path.md)). The
  `install.sh` and `install.ps1` scripts are the one-time installers: published as release
  assets, they download the binary, verify its SHA-256 checksum, place it on `PATH`, and run
  `ai-agent-telemetry configure` when no endpoint is configured yet. Compatibility copies named
  `bootstrap.sh` and `bootstrap.ps1` may also be published for older docs and automation. Check
  state with `ai-agent-telemetry status` / `version`, never by running an installer script.
- **Detection:** a native hook event where the harness exposes one (Claude Code and Cline),
  and session-transcript parsing otherwise (Codex and Cursor). See
  [docs/agent-integration.md](docs/agent-integration.md).
- **Harnesses:** Claude Code, Cline, Codex, and Cursor are shipped. OpenCode is planned.
- **Config & cache paths: uniform XDG, not `os.UserConfigDir()`.** Durable config lives at
  `$XDG_CONFIG_HOME` else `~/.config/ai-agent-telemetry/` and the spool at
  `$XDG_CACHE_HOME` else `~/.cache/ai-agent-telemetry/` — the same path on every OS,
  mirroring the binary's `~/.local/bin`. This is deliberate: `os.UserConfigDir()` is
  `%AppData%` on Windows, which MSIX virtualizes for a packaged harness (Claude Desktop), so a
  packaged and a plain shell diverged onto different config dirs. A home-relative path outside
  `AppData` is never virtualized. Resolved in [config.go](config.go) (`configBase`) and
  [outbox.go](outbox.go) (`cacheBase`); rationale in
  [docs/adr/0003-config-cache-dirs-xdg.md](docs/adr/0003-config-cache-dirs-xdg.md).
- **Out of scope:** the collector, gateway, and storage (VictoriaMetrics, VictoriaLogs,
  Grafana) are infrastructure.
- **Decisions:** the main forks and why each was taken are in
  [docs/adr/](docs/adr/); historical records sit under `docs/superpowers/`.

## Conventions

- **English only.** Every committed file — Markdown, code, comments, commit messages,
  identifiers — is English. Translate anything else before committing.
- **Docs vs history.** Current, maintained documentation lives in `docs/` and the README.
  `docs/superpowers/` is a working archive — dated specs, plans, decisions, and research
  that are snapshots and are not kept up to date. When something changes, update `docs/`;
  never edit a dated `docs/superpowers/` file to match.
- **Naming.** The component is the "ai-agent-telemetry CLI". The response-text "marker" is
  retired terminology — never reintroduce "breadcrumb".
- **Present design forks via AskUserQuestion**, recommendation first, and expect the
  recommendation to be challenged.
- **APM gotchas.** Use `claude`, `codex`, or `cursor` for the corresponding native APM target. Use
  `agent-skills` for Cline because Cline discovers `.agents/skills` and APM has no native `cline`
  target. Cursor needs `.cursor/` to exist before installation. APM-generated artifacts (`apm_modules/`,
  `.agents/`, `.codex/`, `.claude/`, `.cursor/`, and `apm.lock.yaml`) are gitignored; do not commit them.

## Git workflow

`main` is protected by the "Protect main branch" ruleset — the Netcracker standard, matching
`qubership-logging-operator` and `qubership-workflow-hub`. Force-pushes and branch deletion are
blocked, and every change lands through a pull request with one approval and resolved review
threads. CI status checks are not required to merge. Repository admins can bypass for maintenance,
but the default path is a PR.

- **Every change** (docs included): branch → `commit` → `push` → open a PR → review and approve →
  squash-merge.
- **Release:** after the change is on `main`, run the `Release` workflow (workflow_dispatch, with a
  version). It creates the tag and publishes the binaries — never push a tag by hand.

Keep history linear (squash merges) and commit messages in Conventional Commits.

## Testing and cleanup

A test run is `apm install`, exercising the hook, then removing the generated files so the next run starts clean.
They are all gitignored; preview them with `git clean -xdn`.

- **Remove** (APM install artifacts and build output): `apm_modules/`, `.agents/`,
  `.codex/`, `.claude/`, `.cursor/`, `apm.lock.yaml`, `dist/`, the root
  `ai-agent-telemetry` binary, and `eval-workspace/`.
- **Keep:** the root `apm.yml` — gitignored and machine-specific, but the install needs it —
  and the per-machine config outside the repo (endpoint, CA, token, `machine.id` under the
  config dir).

Do not run `git clean -xdf` blindly: it would also delete the root `apm.yml` and any
untracked files not yet committed. Remove the listed paths explicitly.

## Open work

- **OpenCode adapter:** the fifth harness. A native `use_skill` tool call via the
  `.claude/skills/` compatibility extension, the same path as Claude Code.
- **Outbox housekeeping** — offset-file garbage collection is not implemented.
- **Automatic updates** — `self-update` is explicit. There is no hook or scheduled trigger
  yet; users must run `ai-agent-telemetry update-check` or the configure skill to discover a
  newer binary.
- **Dashboards.** The OTLP `service.name` is `ai-agent-telemetry`; update any Grafana
  dashboards that still reference the old `skills-telemetry` or `qubership-skills-telemetry-sender` value.
