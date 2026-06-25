# CLAUDE.md

**Read [README.md](README.md) first**, then this file. The README is the entry point — what
the project does, how telemetry is turned on, the architecture, the data, and the backend
requirements. Deeper docs are linked from its Documentation section. This file holds only
what an agent needs beyond the README: orientation, conventions, and open work.

## Orientation

Skill-usage telemetry for AI coding agents. A skill run is detected per harness, sent to a
shared OpenTelemetry collector, and packaged through APM so that installing the hooks into
a repository is the consent boundary.

- **Two packages.** The hooks that fire the CLI live in
  [`Netcracker/qubership-ai-agent-telemetry`](https://github.com/Netcracker/qubership-ai-agent-telemetry/tree/main/agent-packages/ai-agent-telemetry)
  as the `ai-agent-telemetry` package. The setup skill and bootstrap scripts live here as
  `ai-agent-telemetry-configure` (under `agent-packages/`). Consumers add the hooks package
  to their `apm.yml`; the configure package is a dev dependency for first-time setup.
- **Component:** the `ai-agent-telemetry` CLI — a small Go binary at the repository root
  (a flat `package main`, the "Basic command" layout from the Go module-layout guide). It
  detects the skill, buffers events to a local outbox, and flushes over OTLP/HTTPS. No
  daemon. See [docs/cli.md](docs/cli.md).
- **Invocation: hooks call the binary by its bare name on `PATH`.** The harness hooks and the
  `ai-agent-telemetry-configure` skill run `ai-agent-telemetry` directly — on a dev machine it lives at
  `~/.local/bin/ai-agent-telemetry`. A bare name is shell-agnostic, which is why it replaced the old
  per-turn `bootstrap.{sh,ps1}` hook wrapper (retired; see
  [docs/adr/0002-bare-binary-on-path.md](docs/adr/0002-bare-binary-on-path.md)). The `bootstrap.sh` and
  `bootstrap.ps1` scripts are **retained as the one-time installer**: published as release assets, they
  download the binary, verify its SHA-256 checksum, and place it on `PATH`. Check state with
  `ai-agent-telemetry status` / `version`, never by running a bootstrap script.
- **Detection:** a native hook event where the agent emits one (Claude Code), the session
  transcript otherwise (Codex, Cursor). See [docs/agent-integration.md](docs/agent-integration.md).
- **Harnesses:** Codex, Claude Code, and Cursor are shipped (v0.1.0). OpenCode is planned.
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
- **APM gotchas.** `apm install --target <codex|claude|cursor|all>` deploys the skill, hooks, and the
  trigger to `.claude/rules/` — for Claude Code that is enough, no `apm compile`. Codex and other
  `AGENTS.md` agents additionally need `apm compile` to register the trigger. Cursor needs `.cursor/`
  to exist before install. APM-generated artifacts
  (`apm_modules/`, `.agents/`, `.codex/`, `.claude/`, `.cursor/`, `apm.lock.yaml`) are
  gitignored; do not commit them.

## Git workflow

Solo repository, so the path scales to the change:

- **Minor** (docs, `.gitignore`, small fixes — nothing users see): `commit` → `push`
  straight to `main`. No branch, no PR.
- **Significant** (features, user-visible changes): branch → `commit` → `push` → PR
  (squash) → auto-merge once CI is green → run the `Release` workflow. The PR buys the
  CI gate and a transparent, revertible history; the `Release` workflow (workflow_dispatch,
  with a version) creates the tag and publishes the binaries.

Keep history linear (squash merges) and commit messages in Conventional Commits. `main`
has no strict branch protection on purpose: corporate release automation may push back to
it, which required reviews would block.

## Testing and cleanup

A test run is `apm install` (plus `apm compile` for Codex), exercising the hook, then removing the
generated files so the next run starts clean. They are all gitignored; preview them with
`git clean -xdn`.

- **Remove** (APM install artifacts and build output): `apm_modules/`, `.agents/`,
  `.codex/`, `.claude/`, `.cursor/`, `apm.lock.yaml`, `dist/`, the root
  `ai-agent-telemetry` binary, and `eval-workspace/`.
- **Keep:** the root `apm.yml` — gitignored and machine-specific, but the install needs it —
  and the per-machine config outside the repo (endpoint, CA, token, `machine.id` under the
  config dir).

Do not run `git clean -xdf` blindly: it would also delete the root `apm.yml` and any
untracked files not yet committed. Remove the listed paths explicitly.

## Open work

- **OpenCode adapter** — the fourth harness. A native `use_skill` tool call via the
  `.claude/skills/` compatibility extension, the same path as Claude Code.
- **Outbox housekeeping** — offset-file garbage collection is not implemented.
- **Automatic updates** — `update-check` runs only when the configure skill is
  invoked manually. There is no hook or scheduled trigger yet; users must run the
  skill to discover a newer binary.
- **Dashboards.** The OTLP `service.name` is `ai-agent-telemetry`; update any Grafana
  dashboards that still reference the old `skills-telemetry` or `qubership-skills-telemetry-sender` value.
