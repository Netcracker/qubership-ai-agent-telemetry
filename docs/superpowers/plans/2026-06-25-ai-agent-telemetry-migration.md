# ai-agent-telemetry Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename `skills-telemetry` to `ai-agent-telemetry`, centralize the hooks back into this repository, adopt
Netcracker corporate workflows, rename the `provision` command and skill to `configure`, and seed
`Netcracker/qubership-ai-agent-telemetry` with a single commit tagged `v0.1.0`.

**Architecture:** The tree stays a flat Go `package main` at the root plus two APM packages under `agent-packages/`.
The rename runs in passes that each keep `go test ./...` green: binary identity first, then the `provision` →
`configure` verb, then the APM packages, then workflows and docs. The corporate release chain uploads assets named
`ai-agent-telemetry-<os>-<arch>` so the configure skill's download logic stays compatible.

**Tech Stack:** Go (flat `package main`), APM packages, GitHub Actions forked from `Netcracker/.github/workflow-templates`,
`netcracker/qubership-workflow-hub` actions.

## Global Constraints

- **Name token:** `skills-telemetry` → `ai-agent-telemetry` everywhere. `qubership-` survives **only** in the repository
  slug `Netcracker/qubership-ai-agent-telemetry`.
- **Verb at end** for user-facing kebab-case names (skill, package, rule, instructions, assets): `ai-agent-telemetry-configure`.
  Go function names stay idiomatic verb-first (`parseConfigureFlags`).
- **Environment prefix:** `SKILLS_TELEMETRY_` → `AI_AGENT_TELEMETRY_`.
- **State vocabulary:** `provisioned` / `not provisioned` → `configured` / `not configured`.
- **English, American spelling** in every committed file (`-ize`, `color`, `behavior`, `license`, `analyze`).
- **`go test ./...` stays green** at the end of every Go task.
- **Workflows:** fork from `Netcracker/.github/workflow-templates`, follow `qubership-workflow-conventions` — SHA-pin
  every action with a trailing `# vX.Y.Z`, set `permissions:` at the job level from `contents: read`, apply zizmor
  rules. Never write an action identifier or SHA from memory; read the README and the pin table.
- **No git history** is carried over. The result is one initial commit tagged `v0.1.0`.
- **License:** Apache-2.0.

---

### Task 1: Migration branch and Go binary-identity rename

Rename every binary-identity reference in the Go tree, the Makefile, and `.gitignore`. Leave the `provision` command
untouched — Task 2 owns that. Keep tests green by editing assertions alongside the code.

**Files:**

- Modify: `go.mod:1`
- Modify: `config.go:14` (`pkgName`), `config.go:84-153` (env strings, comments)
- Modify: `flush.go:30` (`service.name`)
- Modify: `outbox.go:48-53` (comments only)
- Modify: `update.go:15` (`repoSlug`), `update.go:31` (User-Agent)
- Modify: `main.go:32` (usage string)
- Modify: `commands.go` (comments mentioning `skills-telemetry`)
- Modify: `Makefile:12,19`
- Modify: `.gitignore:22-23`
- Test: `config_test.go`, `commands_test.go`, `flush_test.go`, `main_test.go`, `transcript_codex_test.go`,
  `transcript_cursor_test.go`, `detect_test.go`

**Interfaces:**

- Consumes: nothing (first task).
- Produces: binary name `ai-agent-telemetry`; `pkgName == "ai-agent-telemetry"`; env vars `AI_AGENT_TELEMETRY_ENDPOINT`
  / `AI_AGENT_TELEMETRY_TOKEN`; `service.name == "ai-agent-telemetry"`; `repoSlug == "Netcracker/qubership-ai-agent-telemetry"`.

- [ ] **Step 1: Create the migration branch**

```bash
cd /Users/denifilatov/Repos/skills-telemetry
git checkout -b feat/rename-ai-agent-telemetry
```

- [ ] **Step 2: Update the env-var and service assertions in tests first (they now fail)**

Replace every `SKILLS_TELEMETRY_ENDPOINT` → `AI_AGENT_TELEMETRY_ENDPOINT` and `SKILLS_TELEMETRY_TOKEN` →
`AI_AGENT_TELEMETRY_TOKEN` in `config_test.go` and `commands_test.go`. Replace the literal config-dir name
`"skills-telemetry"` → `"ai-agent-telemetry"` in `config_test.go:65,91,105`. In `flush_test.go:21-22` change the
expected `service.name`:

```go
if got["service.name"] != "ai-agent-telemetry" {
    t.Fatalf("service.name = %q", got["service.name"])
}
```

In `update_test.go`, leave the version-compare cases unchanged (they use bare versions, not the name).

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL in the config, commands, and flush tests (assertions mismatch).

- [ ] **Step 4: Rename the binary identity in the implementation**

`go.mod:1`:

```go
module ai-agent-telemetry
```

`config.go:14`:

```go
const pkgName = "ai-agent-telemetry"
```

In `config.go`, replace the two env-var literals in `resolveEndpoint`, `resolveEndpointFrom`, `resolveToken`,
`resolveTokenFrom` and any comment:

```go
func resolveEndpoint(flag string) string {
	return resolveEndpointFrom(flag, os.Getenv("AI_AGENT_TELEMETRY_ENDPOINT"), pkgEnv()["AI_AGENT_TELEMETRY_ENDPOINT"])
}
```

```go
func resolveToken() string {
	return resolveTokenFrom(os.Getenv("AI_AGENT_TELEMETRY_TOKEN"), configBase())
}
```

```go
	if tok := loadEnvFile(filepath.Join(pkgDir, "env"))["AI_AGENT_TELEMETRY_TOKEN"]; tok != "" {
```

`flush.go:30`:

```go
		attribute.String("service.name", "ai-agent-telemetry"),
```

`update.go:15` and `update.go:31`:

```go
const repoSlug = "Netcracker/qubership-ai-agent-telemetry"
```

```go
	req.Header.Set("User-Agent", "ai-agent-telemetry/"+version)
```

`main.go:32`:

```go
		stdout("usage: ai-agent-telemetry <ingest|flush|status|selftest|provision|update-check|version>\n")
```

`Makefile:12` and `Makefile:19`:

```makefile
		out=$(DIST)/ai-agent-telemetry-$$os-$$arch$$ext; \
```

```makefile
	@cd $(DIST) && shasum -a 256 ai-agent-telemetry-* > SHA256SUMS && cat SHA256SUMS
```

`.gitignore:22-23`:

```text
/ai-agent-telemetry
/ai-agent-telemetry.exe
```

Then sweep remaining comment-only mentions of `skills-telemetry` in `config.go`, `outbox.go`, `commands.go`,
`flush.go`, and the transcript/detect test fixtures with a guarded replace:

```bash
grep -rl 'skills-telemetry' --include='*.go' . | xargs sed -i '' 's/skills-telemetry/ai-agent-telemetry/g'
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./...`
Expected: PASS. Then confirm the build name:

```bash
go build -o /tmp/aat . && /tmp/aat version
```

Expected: prints `dev`.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: rename binary identity skills-telemetry -> ai-agent-telemetry"
```

---

### Task 2: provision → configure in the CLI

Rename the `provision` subcommand, its flag parser, the `Provisioned` status field, and the state vocabulary. The
`provision` word stays only as a trigger synonym in the skill (Task 3), never in the Go code.

**Files:**

- Modify: `main.go:32` (usage), `main.go:46-64` (case), `main.go:139-150` (`parseProvisionFlags`)
- Modify: `commands.go:19` (`applyProvision`), `commands.go:126-185` (`statusReport`, `gatherStatus`, `formatStatus`)
- Test: `commands_test.go:61,78` (`applyProvision`), `commands_test.go:158-203` (`Provisioned`, state strings),
  `main_test.go` (comments only)

**Interfaces:**

- Consumes: binary `ai-agent-telemetry` from Task 1.
- Produces: subcommand `ai-agent-telemetry configure`; `parseConfigureFlags(args) (endpoint, ca string)`;
  `applyConfigure(configDir, endpoint, caPath, token string) error`; `statusReport.Configured bool`; status hint
  `state: configured` / `not configured — run \`ai-agent-telemetry configure\``.

- [ ] **Step 1: Update tests to the new names (they now fail)**

In `commands_test.go`, rename `applyProvision` → `applyConfigure` at lines 61 and 78, rename the `Provisioned` field to
`Configured` at lines 174, 192, 201, and update the state-string assertion at line 202:

```go
	if !strings.Contains(strings.ToLower(out), "not configured") {
		t.Fatalf("output should flag the not-configured state, got:\n%s", out)
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./...`
Expected: FAIL — `applyConfigure` and `statusReport.Configured` are undefined.

- [ ] **Step 3: Rename the command, flags, and state in the implementation**

`main.go:32` usage:

```go
		stdout("usage: ai-agent-telemetry <ingest|flush|status|selftest|configure|update-check|version>\n")
```

`main.go:46-64` — replace the `case "provision"` block:

```go
	case "configure":
		endpoint, caPath := parseConfigureFlags(args[1:])
		cfg := pkgConfigDir()
		if cfg == "" {
			fmt.Fprintln(os.Stderr, "configure: no user config directory available")
			return 1
		}
		token := readSecret("Collector token (leave blank to skip): ")
		if err := applyConfigure(cfg, endpoint, caPath, token); err != nil {
			fmt.Fprintln(os.Stderr, "configure:", err)
			return 1
		}
		s, err := DefaultOutbox()
		if err != nil {
			fmt.Fprintln(os.Stderr, "outbox:", err)
			return 1
		}
		stdout(formatStatus(gatherStatus(s, cfg, resolveEndpoint(""))))
		return 0
```

`main.go:139-150` — rename the parser:

```go
// parseConfigureFlags reads the configure flags: --endpoint= and --ca=.
func parseConfigureFlags(args []string) (endpoint, ca string) {
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--endpoint="):
			endpoint = strings.TrimPrefix(a, "--endpoint=")
		case strings.HasPrefix(a, "--ca="):
			ca = strings.TrimPrefix(a, "--ca=")
		}
	}
	return endpoint, ca
}
```

`commands.go:19` — rename `applyProvision` → `applyConfigure` (signature unchanged) and update its doc comment.

`commands.go:126-185` — rename the field and the vocabulary:

```go
type statusReport struct {
	Version    string
	ConfigDir  string
	Endpoint   string
	Configured bool
	CAFound    bool
	Buffered   int
	LastFlush  string
}
```

In `gatherStatus`, set `Configured: endpoint != ""`. In `formatStatus`:

```go
	if r.Configured {
		fmt.Fprint(&b, "state: configured\n")
	} else {
		fmt.Fprint(&b, "state: not configured — run `ai-agent-telemetry configure` to set the endpoint\n")
	}
```

Update the surrounding comments: `statusReport` is "the read-only diagnosis the configure skill reads"; "A machine is
configured once it has an endpoint to send to."

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./...`
Expected: PASS. Then smoke-test the command name:

```bash
go build -o /tmp/aat . && /tmp/aat status | grep state
```

Expected: prints `state: not configured — run \`ai-agent-telemetry configure\` ...` (on an unconfigured machine).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: rename provision command and state to configure"
```

---

### Task 3: Rename the configure package

Move the package directory, the skill directory, and the instructions file; update `apm.yml`, the README, the bootstrap
scripts, and all skill prose to the new binary name, the new command, and the new skill name. Do **not** add the
legacy-cleanup references yet — Task 4 adds those after this bulk rename so they are not clobbered.

**Files:**

- Rename: `agent-packages/skills-telemetry-configure/` → `agent-packages/ai-agent-telemetry-configure/`
- Rename: `.../.apm/skills/provision-skills-telemetry/` → `.../.apm/skills/ai-agent-telemetry-configure/`
- Rename: `.../.apm/instructions/provision-skills-telemetry.instructions.md` →
  `.../.apm/instructions/ai-agent-telemetry-configure.instructions.md`
- Modify: `apm.yml` (both copies), `README.md`, `.apm/hooks/scripts/bootstrap.sh`, `.apm/hooks/scripts/bootstrap.ps1`,
  `.../skills/ai-agent-telemetry-configure/SKILL.md`, `.../references/codex-sandbox.md`, `.../references/deployment.md`
- Modify (repo rule): `.claude/rules/provision-skills-telemetry.md` is APM-generated and gitignored; the **source** rule
  is shipped by the package, so no tracked rule file changes here.

**Interfaces:**

- Consumes: binary name and `configure` command from Tasks 1–2.
- Produces: package `ai-agent-telemetry-configure`; skill `ai-agent-telemetry-configure`.

- [ ] **Step 1: Move the directories and the instructions file**

```bash
cd /Users/denifilatov/Repos/skills-telemetry
git mv agent-packages/skills-telemetry-configure agent-packages/ai-agent-telemetry-configure
cd agent-packages/ai-agent-telemetry-configure
git mv .apm/skills/provision-skills-telemetry .apm/skills/ai-agent-telemetry-configure
git mv .apm/instructions/provision-skills-telemetry.instructions.md \
       .apm/instructions/ai-agent-telemetry-configure.instructions.md
cd /Users/denifilatov/Repos/skills-telemetry
```

- [ ] **Step 2: Rewrite both `apm.yml` files**

`agent-packages/ai-agent-telemetry-configure/apm.yml` (and the identical sibling, if `find` shows two):

```yaml
name: ai-agent-telemetry-configure
version: 2.0.0
description: Configure skill and bootstrap scripts for the ai-agent-telemetry CLI (endpoint, token, CA, selftest).
author: Denis Filatov
```

- [ ] **Step 3: Bulk-rename tokens inside the package, then fix the skill name and command by hand**

```bash
cd agent-packages/ai-agent-telemetry-configure
grep -rl 'skills-telemetry\|SKILLS_TELEMETRY\|provision' . | xargs sed -i '' \
  -e 's/SKILLS_TELEMETRY/AI_AGENT_TELEMETRY/g' \
  -e 's/skills-telemetry/ai-agent-telemetry/g'
cd /Users/denifilatov/Repos/skills-telemetry
```

Then in `SKILL.md`, `.instructions.md`, and `README.md` make the non-mechanical edits:

- Skill frontmatter `name:` → `ai-agent-telemetry-configure`.
- Every `ai-agent-telemetry provision` command string → `ai-agent-telemetry configure`.
- Keep `provision`, `onboard`, and `set up` in the skill `description:` as **trigger phrases** (e.g. "provision,
  onboard, check, or fix"), so the skill still fires on "provision telemetry".
- State words in any sample `status` output → `configured` / `not configured`.

- [ ] **Step 4: Verify the package installs and compiles**

```bash
cd /Users/denifilatov/Repos/skills-telemetry
apm install --dev ./agent-packages/ai-agent-telemetry-configure --target claude 2>&1 | tail -5
apm compile 2>&1 | tail -5
ls .claude/skills/ai-agent-telemetry-configure/SKILL.md
```

Expected: install and compile succeed; the skill file exists under its new name.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: rename skills-telemetry-configure package to ai-agent-telemetry-configure"
```

---

### Task 4: Legacy-cleanup step in the configure skill

Add a first-run cleanup section to the configure skill. It detects and removes old `skills-telemetry` state. These are
the only places the old names survive on purpose; the final scan (Task 9) must treat them as intentional.

**Files:**

- Modify: `agent-packages/ai-agent-telemetry-configure/.apm/skills/ai-agent-telemetry-configure/SKILL.md`

**Interfaces:**

- Consumes: the renamed skill from Task 3.
- Produces: a documented, idempotent cleanup procedure.

- [ ] **Step 1: Add the cleanup section to SKILL.md**

Insert a section near the top of the configure procedure:

````markdown
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
````

- [ ] **Step 2: Recompile and eyeball the rendered skill**

```bash
cd /Users/denifilatov/Repos/skills-telemetry
apm compile 2>&1 | tail -3
grep -n 'Remove legacy' .claude/skills/ai-agent-telemetry-configure/SKILL.md
```

Expected: the new section is present in the compiled skill.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "feat(configure): add first-run legacy skills-telemetry cleanup"
```

---

### Task 5: Return the hooks package

Recreate the hooks package from qubership-ai-packages PR #31, renamed to `ai-agent-telemetry`, with the command pointed
at the new binary. Add the marketplace entry.

**Files:**

- Create: `agent-packages/ai-agent-telemetry/apm.yml`
- Create: `agent-packages/ai-agent-telemetry/README.md`
- Create: `agent-packages/ai-agent-telemetry/.apm/hooks/skill-call-claude-hooks.json`
- Create: `agent-packages/ai-agent-telemetry/.apm/hooks/skill-call-codex-hooks.json`
- Create: `agent-packages/ai-agent-telemetry/.apm/hooks/skill-call-cursor-hooks.json`

**Interfaces:**

- Consumes: binary name `ai-agent-telemetry` from Task 1.
- Produces: consumer package `ai-agent-telemetry` whose hooks run `ai-agent-telemetry ingest --agent=<harness>`.

- [ ] **Step 1: Create `apm.yml`**

```yaml
name: ai-agent-telemetry
version: 1.0.0
description: Hooks that call the ai-agent-telemetry CLI after each skill invocation (Claude Code, Codex, Cursor).
author: Netcracker
```

- [ ] **Step 2: Create the three hook files**

`skill-call-claude-hooks.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Skill",
        "hooks": [
          {
            "type": "command",
            "command": "ai-agent-telemetry ingest --agent=claude",
            "timeout": 30,
            "statusMessage": "Recording skill telemetry"
          }
        ]
      }
    ]
  }
}
```

`skill-call-codex-hooks.json`:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "ai-agent-telemetry ingest --agent=codex",
            "timeout": 30,
            "statusMessage": "Recording skill telemetry"
          }
        ]
      }
    ]
  }
}
```

`skill-call-cursor-hooks.json`:

```json
{
  "version": 1,
  "hooks": {
    "afterAgentResponse": [
      { "command": "ai-agent-telemetry ingest --agent=cursor" }
    ]
  }
}
```

- [ ] **Step 3: Create the README**

Adapt the PR #31 README: title `# ai-agent-telemetry`, the three-harness table, and prerequisites pointing at
`~/.local/bin/ai-agent-telemetry`, the dev package `ai-agent-telemetry-configure`, and the `ai-agent-telemetry-configure`
skill. Install snippet:

```sh
apm install Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry
```

- [ ] **Step 4: Verify the hooks compile to the new command**

```bash
cd /Users/denifilatov/Repos/skills-telemetry
apm install ./agent-packages/ai-agent-telemetry --target claude 2>&1 | tail -5
apm compile 2>&1 | tail -5
grep -r 'ai-agent-telemetry ingest' .claude/ .codex/ .cursor/ 2>/dev/null | head
```

Expected: at least one compiled hook runs `ai-agent-telemetry ingest --agent=...`.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: add ai-agent-telemetry hooks package"
```

---

### Task 6: Corporate gate workflows

Replace the custom `ci.yml` with corporate templates for CI, PR hygiene, quality, license, and security. Follow the
qubership skills for every file — do not hand-write SHAs.

**Files:**

- Delete: `.github/workflows/ci.yml`
- Create: `.github/workflows/go-build.yaml`, `pr-conventional-commits.yaml`, `pr-lint-title.yaml`,
  `automatic-pr-labeler.yaml`, `cla.yaml`, `super-linter.yaml`, `check-license.yaml`, `link-checker.yaml`,
  `profanity-filter.yaml`, `ossf-scorecard.yaml`, `dependency-review.yaml`
- Create (if a template needs it): `.qubership/` config files per the template's README

**Interfaces:**

- Consumes: the Go module from Task 1.
- Produces: the corporate gate workflows.

- [ ] **Step 1: Load the workflow skills and confirm each template exists**

Read `qubership-workflow-conventions`, then `qubership-templates-guide`. For each file in the list, confirm it is in the
catalog:

```bash
gh api repos/Netcracker/.github/contents/workflow-templates --jq '.[].name' | sort
```

Expected: each template filename above is present. If one is missing, pick the nearest alternative and note the change.

- [ ] **Step 2: Fork each template and adapt**

For each template: fetch it once, then adapt per `qubership-workflow-conventions` (SHA-pin from the pin table in
`qubership-actions-guide`, job-level `permissions`, zizmor rules). Adaptations: set `name:`/`run-name:` to a repo label;
in `go-build.yaml` keep build, vet, test, and gofmt; in `cla.yaml` set the document path; leave job structure and
`permissions` blocks intact.

- [ ] **Step 3: Delete the old CI workflow**

```bash
git rm .github/workflows/ci.yml
```

- [ ] **Step 4: Lint the workflows**

```bash
actionlint .github/workflows/*.yaml
zizmor .github/workflows/*.yaml
```

Expected: no errors. If `actionlint`/`zizmor` are not installed, note it and have the user run them, or rely on the
`super-linter` job after push.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "ci: adopt corporate gate workflows, drop custom ci.yml"
```

---

### Task 7: Corporate release chain

Replace the custom `release.yml` with the corporate tag → release → assets chain. The Go cross-compile matrix stays as
the producer; assets keep the `ai-agent-telemetry-<os>-<arch>` names the configure skill downloads.

**Files:**

- Delete: `.github/workflows/release.yml`
- Create: `.github/workflows/release.yaml`
- Create: `.github/release-drafter-config.yml`

**Interfaces:**

- Consumes: the Makefile build matrix (asset names) from Task 1.
- Produces: a release workflow that tags `vX.Y.Z`, drafts a release, and uploads `ai-agent-telemetry-<os>-<arch>` assets.

- [ ] **Step 1: Read the release domain guide**

Read `qubership-actions-guide` → `release.md`. Use the pattern "Tag + GitHub Release + assets from build artifacts":
producer job (Go matrix) → `upload-artifact`; release job → `tag-action` (check) → `tag-action` (create,
`create-release: true`) → `release-drafter` → `assets-action`. Read each action's README for exact input names; pin
every action by SHA from the pin table.

- [ ] **Step 2: Author the release workflow**

Producer job: the cross-compile matrix `darwin/{arm64,amd64} linux/{arm64,amd64} windows/amd64`, building
`ai-agent-telemetry-<os>-<arch>` with `-ldflags "-s -w -X main.version=<tag>"`, then `upload-artifact`. Release job:
`download-artifact` → `assets-action` with `item-path` pointing at the `ai-agent-telemetry-*` files. Trigger on `push`
tags `v*` plus `workflow_dispatch` with a `version` input. Set `permissions: contents: write` only on the jobs that
tag/release/upload.

- [ ] **Step 3: Create the release-drafter config**

Write `.github/release-drafter-config.yml` using the default template from `release.md` (categories, version-resolver,
`name-template: 'v$RESOLVED_VERSION'`).

- [ ] **Step 4: Delete the old release workflow and lint**

```bash
git rm .github/workflows/release.yml
actionlint .github/workflows/release.yaml
zizmor .github/workflows/release.yaml
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "ci: adopt corporate release chain with renamed assets"
```

---

### Task 8: Documentation and ancillary refresh

Rename the remaining prose: README, CLAUDE.md, the `docs/` tree (excluding `docs/superpowers/`), and
`telemetry-backend/`. Reflect the new name, the `configure` command, the new repo slug, and the corporate workflows.

**Files:**

- Modify: `README.md`, `CLAUDE.md`, `docs/cli.md`, `docs/agent-integration.md`, `docs/adr/0001-*.md`,
  `docs/adr/0002-*.md`, `docs/adr/0003-*.md`, `docs/adr/0004-*.md`, `telemetry-backend/README.md`,
  `telemetry-backend/docker-compose.yml`
- Do **not** modify: `docs/superpowers/**` (dated archive)

**Interfaces:**

- Consumes: every rename from Tasks 1–7.
- Produces: documentation consistent with the new name and commands.

- [ ] **Step 1: Bulk-rename tokens outside the archive**

```bash
cd /Users/denifilatov/Repos/skills-telemetry
git ls-files 'README.md' 'CLAUDE.md' 'docs/*.md' 'docs/adr/*.md' 'telemetry-backend/*' \
  | xargs sed -i '' \
  -e 's/SKILLS_TELEMETRY/AI_AGENT_TELEMETRY/g' \
  -e 's/qubership-skills-telemetry-sender/ai-agent-telemetry/g' \
  -e 's/skills-telemetry/ai-agent-telemetry/g'
```

- [ ] **Step 2: Fix the non-mechanical references by hand**

- Replace any `ai-agent-telemetry provision` → `ai-agent-telemetry configure`, and `provision-ai-agent-telemetry` skill
  references → `ai-agent-telemetry-configure`.
- Replace the old release/CI workflow descriptions with the corporate ones (Tasks 6–7).
- Set the repository slug to `Netcracker/qubership-ai-agent-telemetry` wherever a clone URL or release source appears.
- In `CLAUDE.md`, update the "Dashboards" open-work item to state the `service.name` is now `ai-agent-telemetry`.

- [ ] **Step 3: Verify markdown and links**

```bash
markdownlint-cli2 --fix 'README.md' 'CLAUDE.md' 'docs/**/*.md' 2>&1 | tail -5
```

Expected: clean, or only non-fixable wraps to adjust by hand. Skip `docs/superpowers/`.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "docs: rename skills-telemetry to ai-agent-telemetry across docs"
```

---

### Task 9: Final verification scan

Run the two scans from the spec and analyze the results. This is the gate before seeding the new repo.

**Files:** none (read-only analysis).

**Interfaces:**

- Consumes: the whole renamed tree.
- Produces: a pass/fail report distinguishing forgotten leftovers from intentional legacy references.

- [ ] **Step 1: Scan for the `configure` verb and assert consistency**

```bash
cd /Users/denifilatov/Repos/skills-telemetry
echo "--- skill/package/rule/instructions names ---"
git ls-files | grep -i 'configure\|provision'
echo "--- CLI subcommand present, provision absent in Go ---"
grep -rn 'case "configure"\|case "provision"\|parseConfigure\|parseProvision\|applyConfigure\|applyProvision' --include='*.go' .
echo "--- state vocabulary ---"
grep -rn 'state: configured\|not configured\|provisioned' --include='*.go' .
```

Expected: kebab-case names end in `configure`; Go has `case "configure"`, `parseConfigureFlags`, `applyConfigure` and
**no** `provision` identifiers; state prints `configured` / `not configured`. The only allowed `provision` hits are
trigger synonyms in the skill `description:`.

- [ ] **Step 2: Scan for the `skills-telemetry` family and classify every hit**

```bash
git ls-files | grep -v '^docs/superpowers/' \
  | xargs grep -nEi 'skills-telemetry|SKILLS_TELEMETRY|qubership-skills|denifilatoff/skills' 2>/dev/null
echo "--- go.mod and repoSlug ---"
head -1 go.mod; grep -n 'repoSlug' update.go
```

Expected: the only matches are inside the configure skill's legacy-cleanup section (intentional). Everything else is
zero. `go.mod` is `module ai-agent-telemetry`; `repoSlug` is `Netcracker/qubership-ai-agent-telemetry`. Any other hit is
a forgotten half-rename — fix it, then re-run both scans.

- [ ] **Step 3: Final green build and test**

```bash
go test ./... && go build -o /tmp/aat . && /tmp/aat version
```

Expected: tests pass; binary builds.

- [ ] **Step 4: Commit any fixes**

```bash
git add -A
git commit -m "chore: resolve rename leftovers from final scan" || echo "nothing to fix"
```

---

### Task 10: Seed the target repository

Create the single initial commit with the Apache-2.0 license, push to `Netcracker/qubership-ai-agent-telemetry`, and tag
`v0.1.0`. No history is carried over.

**Files:**

- Create: `LICENSE` (Apache-2.0, if not already present in the worktree)

**Interfaces:**

- Consumes: the fully renamed, scanned tree.
- Produces: `Netcracker/qubership-ai-agent-telemetry@main` with one commit and tag `v0.1.0`.

- [ ] **Step 1: Confirm push access and the existing license**

```bash
gh api repos/Netcracker/qubership-ai-agent-telemetry --jq '{push: .permissions.push, license: .license.spdx_id, default_branch}'
```

Expected: `push: true`, `license: Apache-2.0`. Fetch the repo's `LICENSE` into the worktree so the seed commit keeps it
verbatim:

```bash
gh api repos/Netcracker/qubership-ai-agent-telemetry/contents/LICENSE --jq '.content' | base64 -d > LICENSE
```

- [ ] **Step 2: Build the single initial commit on an orphan branch**

```bash
cd /Users/denifilatov/Repos/skills-telemetry
git checkout --orphan seed-v0.1.0
git add -A
git commit -m "chore: initial commit of ai-agent-telemetry v0.1.0"
```

- [ ] **Step 3: Push to the new remote, then tag**

Confirm with the user before pushing — this is the irreversible, outward-facing step.

```bash
git remote add target https://github.com/Netcracker/qubership-ai-agent-telemetry.git
git push --force target seed-v0.1.0:main
git tag v0.1.0
git push target v0.1.0
```

- [ ] **Step 4: Verify the release pipeline and the tree**

```bash
gh run list --repo Netcracker/qubership-ai-agent-telemetry --limit 5
gh release view v0.1.0 --repo Netcracker/qubership-ai-agent-telemetry --json assets --jq '.assets[].name'
```

Expected: the release workflow runs on the tag and publishes `ai-agent-telemetry-<os>-<arch>` assets.

---

## Self-Review

**Spec coverage:**

- Repo move + seed (spec §Goal, §Execution) → Task 10.
- Rename map (spec §Rename map) → Tasks 1, 3, 8; verified in Task 9.
- Two packages, hooks returned (spec §Package structure) → Tasks 3, 5.
- Corporate workflows + release (spec §Workflows) → Tasks 6, 7.
- provision → configure (spec §provision → configure) → Task 2 (Go), Task 3 (skill prose).
- Legacy cleanup (spec §Legacy cleanup) → Task 4.
- Final verification scan (spec §Final verification scan) → Task 9.
- License Apache-2.0, no history (spec §Decisions) → Task 10.
- Risks: `service.name` follow-up noted in Task 8 Step 2; backend rename in Task 8; old repo untouched.

**Open items carried from the spec, to confirm at execution:** tag form `v0.1.0` (used in Task 10); whether
`super-linter` and `profanity-filter` ship in the first cut (Task 6) — both are in the list but can be dropped if noisy.

**Placeholder scan:** workflow YAML in Tasks 6–7 is intentionally produced by following the qubership skills rather than
pre-baked, because `qubership-workflow-conventions` forbids writing action SHAs from memory. Every Go, JSON, Makefile,
and config edit shows the actual content.

**Type consistency:** `parseConfigureFlags`, `applyConfigure`, and `statusReport.Configured` are defined in Task 2 and
referenced consistently in Tasks 2 and 9. Env vars `AI_AGENT_TELEMETRY_ENDPOINT` / `_TOKEN`, `pkgName`, `service.name`,
and `repoSlug` are defined in Task 1 and checked in Task 9.
