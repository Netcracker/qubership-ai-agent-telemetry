# Migration design: skills-telemetry → ai-agent-telemetry

Move the project to `Netcracker/qubership-ai-agent-telemetry`, rename it from `skills-telemetry` to
`ai-agent-telemetry`, centralize the hooks back into this repository, adopt Netcracker corporate workflows, and rename
the `provision` command and skill to `configure`. History is not carried over; the result is a fresh tree tagged
`v0.1.0`.

Status: approved design, ready for an implementation plan.

## Goal and scope

The current repository is a flat Go `package main` plus an `agent-packages/` APM tree. It distributes a CLI binary that
detects skill runs and ships them over OTLP. This migration produces an equivalent tree under a new name and a new home,
with the end-to-end telemetry (binary, hooks, configure skill) centralized in one repository.

In scope:

- Rename every `skills-telemetry` reference to `ai-agent-telemetry`. Keep the `qubership-` prefix only in the GitHub
  repository slug.
- Bring the hooks package back from [qubership-ai-packages PR #31](https://github.com/Netcracker/qubership-ai-packages/pull/31)
  and rename it.
- Replace the custom CI and release workflows with a curated set of Netcracker corporate workflows.
- Rename the `provision` command and skill to `configure` (the `aws configure` analogy), verb-at-end for all
  user-facing kebab-case names.
- Add a legacy-cleanup step to the configure skill instead of a migration.
- Seed the target repository with a single initial commit and tag `v0.1.0`.

Out of scope:

- The old repository. It is archived read-only by the owner, not deleted, and not modified here.
- The collector, gateway, and Grafana. The `service.name` change is recorded as follow-up work but applied in
  infrastructure, not in this repository.
- Carrying or parking git history. No `legacy/main` branch, no graft ref.

## Decisions

These forks were closed during brainstorming:

- **Name token:** `ai-agent-telemetry` everywhere. `qubership-` survives only in `Netcracker/qubership-ai-agent-telemetry`.
- **License:** Apache-2.0, which the target repository already carries and which matches the Netcracker convention.
- **History:** not carried over and not parked. The source repository is archived, so the history remains reachable
  there if ever needed; grafting it onto the new tree later is possible but deliberately not prepared.
- **Packages:** two APM packages — `ai-agent-telemetry` (hooks, consumer dependency) and `ai-agent-telemetry-configure`
  (configure skill, dev dependency).
- **Workflows:** a curated corporate set, forked from the `Netcracker/.github/workflow-templates` catalog per the
  `qubership-workflow-conventions` rules, not copied raw from another repository.
- **Release:** fully corporate. The binary release runs through `tag-action`, `release-drafter`, and `assets-action`;
  only the Go cross-compile matrix stays custom because no Qubership action cross-compiles Go.
- **Command and skill verb:** `provision` → `configure`, verb-at-end for kebab-case names.

## Rename map

The `qubership-` prefix appears only in the repository slug. Every other surface drops it and renames the token.

| Surface | Before | After |
| --- | --- | --- |
| Binary and PATH | `skills-telemetry`, `~/.local/bin/skills-telemetry` | `ai-agent-telemetry`, `~/.local/bin/ai-agent-telemetry` |
| Go module (`go.mod`) | `skills-telemetry` | `ai-agent-telemetry` |
| OTLP `service.name` | `skills-telemetry` | `ai-agent-telemetry` |
| Config directory | `~/.config/skills-telemetry/` | `~/.config/ai-agent-telemetry/` |
| Cache directory | `~/.cache/skills-telemetry/` | `~/.cache/ai-agent-telemetry/` |
| Environment prefix | `SKILLS_TELEMETRY_ENDPOINT`, `SKILLS_TELEMETRY_TOKEN` | `AI_AGENT_TELEMETRY_ENDPOINT`, `AI_AGENT_TELEMETRY_TOKEN` |
| Hooks package | `skills-telemetry` | `ai-agent-telemetry` |
| Configure package | `skills-telemetry-configure` | `ai-agent-telemetry-configure` |
| Skill | `provision-skills-telemetry` | `ai-agent-telemetry-configure` |
| Instructions file | `provision-skills-telemetry.instructions.md` | `ai-agent-telemetry-configure.instructions.md` |
| Rule file | `provision-skills-telemetry.md` | `ai-agent-telemetry-configure.md` |
| Release assets | `skills-telemetry-<os>-<arch>` | `ai-agent-telemetry-<os>-<arch>` |
| Repository slug | — | `Netcracker/qubership-ai-agent-telemetry` |

Scope of the textual change: roughly 214 occurrences of `skills-telemetry`, 44 of `SKILLS_TELEMETRY`, and 18 of
`provision-*` across about 30 tracked files, excluding the `docs/superpowers/` archive.

## Package structure

After the hooks return, the repository holds two APM packages under `agent-packages/`:

- `ai-agent-telemetry/` — the hooks from PR #31: `skill-call-{claude,codex,cursor}-hooks.json` renamed to the
  `ai-agent-telemetry` variant, plus `apm.yml`, `README`, and the `.claude-plugin/marketplace.json` entry. This is the
  consumer dependency a repository adds to start reporting telemetry.
- `ai-agent-telemetry-configure/` — the configure skill and bootstrap scripts. This is the dev dependency used for
  first-time provisioning.

Splitting them keeps the consumer and the dev install paths separate, the same boundary the project already draws.

## Workflows

Fork from the `Netcracker/.github/workflow-templates` catalog and adapt each file per `qubership-workflow-conventions`:
SHA-pin every action with a trailing `# vX.Y.Z`, set `permissions:` at the job level starting from `contents: read`, and
apply the zizmor rules. Use `qubership-templates-guide` to locate each template and `qubership-actions-guide` for the
action identifiers and the pin table.

Curated set:

- **CI:** `go-build.yaml` — replaces the current `ci.yml` (build, vet, test, gofmt).
- **PR hygiene:** `pr-conventional-commits.yaml`, `pr-lint-title.yaml`, `automatic-pr-labeler.yaml`, `cla.yaml`.
- **Quality and license:** `super-linter.yaml`, `check-license.yaml`, `link-checker.yaml`, `profanity-filter.yaml`.
- **Security:** `ossf-scorecard.yaml`, `dependency-review.yaml`.
- **Release:** replaces the custom `release.yml` with the corporate chain `tag-action` (check, then create with
  release) → `release-drafter` → `assets-action`. The producer job is the Go cross-compile matrix; assets are named
  `ai-agent-telemetry-<os>-<arch>` so the configure skill's download logic stays compatible. Requires
  `.github/release-drafter-config.yml`.

Skipped as irrelevant to a Go binary: Docker, Helm, Maven, npm, Python, chart, SBOM, and GHCR-cleanup templates.

Two items to confirm during the plan:

- Tag form `v0.1.0` versus `0.1.0`. The recommendation is `v0.1.0`, to match the release trigger and the configure
  skill, which parses `v*`.
- Whether `super-linter` and `profanity-filter` ship in the first cut or follow once the tree is stable; both can be
  noisy on a fresh repository.

## provision → configure rename

This rename is scoped as its own step inside the migration branch, not a blind `provision → configure` substitution and
not a separate task after assembly. Folding it into the same pass avoids editing the same six files twice (the skill,
instructions, rule, README, `main.go`, `commands.go`) and avoids shipping `provision` for even one commit on a
fresh-start repository.

`provision` lives on three levels:

1. **CLI subcommand** — `main.go` and `commands.go`. `skills-telemetry provision [--endpoint= --ca=]` writes the
   endpoint and CA to config. This is the direct `aws configure` analog. Becomes `ai-agent-telemetry configure`;
   `parseProvisionFlags` becomes `parseConfigureFlags`.
2. **Skill, rule, and instructions** — `provision-skills-telemetry` becomes `ai-agent-telemetry-configure`.
3. **State vocabulary** — `status` output and config comments. `provisioned` / `not provisioned` becomes `configured` /
   `not configured`, following the `aws configure` register.

Three judgment calls, not mechanical substitutions:

- **State vocabulary** is semantic output, not an identifier. Rename `provisioned` → `configured` and the status hint
  to `not configured — run \`ai-agent-telemetry configure\``.
- **Trigger synonyms stay.** The skill description keeps `provision`, `onboard`, and `set up` as trigger phrases so the
  skill still fires on "provision telemetry", even though the canonical command is `configure`.
- **Go function names stay idiomatic.** Verb-first for Go functions (`parseConfigureFlags`). The verb-at-end rule
  applies only to user-facing kebab-case names — skill, package, rule, instructions, assets.

Resulting names: command `ai-agent-telemetry configure`, skill `ai-agent-telemetry-configure` in package
`ai-agent-telemetry-configure` (skill name equal to package name is intentional and normal in APM), state `configured` /
`not configured`.

## Legacy cleanup instead of migration

Telemetry runs on one or two machines, so a migration is unnecessary. Instead, the configure skill gains a
first-run cleanup step that detects and removes legacy state:

- `~/.local/bin/skills-telemetry`
- `~/.config/skills-telemetry/`
- `~/.cache/skills-telemetry/`
- `SKILLS_TELEMETRY_*` environment variables in shell profiles (warn, then clean)
- stale APM artifacts under the old package names

The step is idempotent and prints what it found and what it removed. These legacy references inside the configure skill
are intentional — they are the only place the old names survive on purpose.

## Execution order

1. Branch in the current working copy. Run the bulk rename across code, `go.mod`, the environment prefix, docs, and the
   package directories.
2. Bring the hooks back from PR #31 and rename them to `ai-agent-telemetry`.
3. Apply the provision → configure rename as its own scoped step.
4. Add the corporate workflows and `.github/release-drafter-config.yml`.
5. Update the configure skill with the cleanup step, and refresh `README.md`, `CLAUDE.md`, and the ADRs.
6. Run `go test ./...` green and review by hand.
7. Final verification scan (next section).
8. Create the single initial commit with the Apache-2.0 `LICENSE`, push to the new remote, and tag `v0.1.0`.

## Final verification scan

Run as the last step, after the whole tree is assembled:

1. **Scan for `configure`.** Confirm verb-at-end holds everywhere (skill, package, rule, instructions), the CLI
   subcommand `configure` is present, no `provision` survives as a command or skill name, and the state prints as
   `configured` / `not configured`.
2. **Scan for `skills-telemetry`** and its family (`SKILLS_TELEMETRY`, `qubership-skills`, the module path, the
   environment prefix, `provision`). Confirm zero leftovers, with one exception: the configure skill's legacy-cleanup
   step intentionally names the old paths. The analysis must distinguish a forgotten half-rename from a deliberate
   legacy reference in the cleanup logic.

## Risks and follow-up

- **Collector and Grafana.** `service.name` changes again, from `skills-telemetry` to `ai-agent-telemetry`. Update the
  Grafana key. This is infrastructure, outside this repository, tracked as open work.
- **Backend.** `telemetry-backend/` moves as-is; only its references are renamed.
- **Old repository.** Archived by the owner, never modified here.
