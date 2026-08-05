# Cline harness support implementation plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task by task.

**Goal:** Add Cline as a first-class telemetry harness across detection, global hook lifecycle, installer selection,
APM skill deployment, status, uninstall, documentation, and live verification in Cline CLI and the Cline VS Code
Extension.

**Architecture:** Cline clients share one global `PostToolUse` file under `~/Documents/Cline/Hooks`. The file invokes
`ai-agent-telemetry ingest --agent=cline`, suppresses CLI output, and always returns `{"cancel":false}` to Cline. A
bounded native-event adapter accepts both the VS Code/JetBrains `PostToolUse` envelope and the CLI `tool_result`
compatibility envelope. The lifecycle target remains `cline`, while APM maps it to `agent-skills` because APM has no
native Cline target and Cline discovers `.agents/skills`.

**Tech stack:** Go, Cobra, Cline file hooks, POSIX shell, Windows PowerShell, APM CLI, OpenTelemetry logs,
VictoriaLogs.

---

### Task 1: Add the bounded Cline event adapter

**Files:**

- Modify: `detect_test.go`
- Modify: `privacy_test.go`
- Modify: `detect.go`
- Modify: `event.go`

- [x] Add table-driven tests for the VS Code payload: `PostToolUse`, `use_skill`, `skill_name`, and `success=true`.
- [x] Add table-driven tests for the CLI payload: `tool_result`, `skills`, `skill`, and `success=true`.
- [x] Cover `skillName`, unsuccessful calls, unrelated hook and tool names, malformed input, missing fields,
      workspace-root selection, repository resolution, and invalid identifiers.
- [x] Add a privacy fixture proving that user, model, result, arbitrary parameters, workspace metadata, and raw remote
      fields cannot enter a serialized event.
- [x] Run the focused detector and privacy tests and confirm that they fail because Cline is unsupported.
- [x] Implement an allowlisted Cline payload type and route `agent=cline` through `detect`.
- [x] Resolve one unambiguous normalized repository across all non-empty `workspaceRoots` entries and create the
      existing `skill_executed` event shape; fail closed when multi-root attribution is ambiguous.
- [x] Permit `cline` in event validation without changing the schema.
- [x] Run the focused tests and confirm that they pass.

### Task 2: Manage the single global Cline hook safely

**Files:**

- Create: `hooks_cline.go`
- Create: `hooks_cline_test.go`
- Modify: `hooks.go`
- Modify: `hooks_remove.go`
- Modify: `hooks_test.go`

- [x] Add tests for the POSIX path `Documents/Cline/Hooks/PostToolUse`, executable mode, exact fail-open wrapper,
      idempotent installation, and installed status.
- [x] Add tests for the Windows path and exact `PostToolUse.ps1` wrapper through an injected platform helper.
- [x] Add tests proving that installation rejects and preserves an unknown existing file or symlink target.
- [x] Add tests proving that uninstall removes only exact owned content and preserves modified or unrelated files with
      a warning.
- [x] Add integration tests proving that failure for Cline does not prevent other selected hook targets from being
      installed or removed.
- [x] Run the focused hook tests and confirm that they fail before implementation.
- [x] Implement path selection, canonical content, exclusive creation or mode repair, status inspection, and guarded
      removal.
- [x] Add `cline` to the canonical hook target order without changing native Cursor handling.
- [x] Run all hook tests and confirm that they pass.

### Task 3: Add Cline to CLI and lifecycle selection

**Files:**

- Modify: `selection_test.go`
- Modify: `hooks_test.go`
- Modify: `cli_test.go`
- Modify: `main_test.go`
- Modify: `selection.go`
- Modify: `cli_commands.go`
- Modify: `main.go`

- [x] Update tests for default, `all`, subset, completion, help, configure, hook install, and hook uninstall selections.
- [x] Add an ingest CLI test for `--agent=cline`.
- [x] Run the focused selection and CLI tests and confirm that they fail before implementation.
- [x] Wire `cline` through the existing generic target-selection and command paths.
- [x] Keep the Codex-only restart notice unchanged.
- [x] Run the focused tests and confirm that they pass.

### Task 4: Map Cline to the APM `agent-skills` target

**Files:**

- Modify: `component_apm_test.go`
- Modify: `component_apm.go`

- [x] Add tests for `cline -> agent-skills`, mixed `codex,cline`, stable ordering, and duplicate target elimination.
- [x] Run the focused APM component tests and confirm that they fail before implementation.
- [x] Separate lifecycle harness names from APM deployment target names in `joinAPMTargets`.
- [x] Run the focused tests and confirm that they pass.

### Task 5: Update maintained documentation and package metadata

**Files:**

- Modify: `README.md`
- Modify: `docs/agent-integration.md`
- Modify: `docs/cli.md`
- Modify: `docs/lifecycle-installer.md`
- Modify: `agent-packages/ai-agent-telemetry/README.md`
- Modify: `agent-packages/ai-agent-telemetry/apm.yml`
- Modify: `agent-packages/ai-agent-telemetry-configure/README.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/.apm/instructions/ai-agent-telemetry-configure.instructions.md`

- [x] Document Cline as one harness spanning VS Code, JetBrains, compatible VS Code hosts, and CLI.
- [x] Document the global file paths, ownership conflict behavior, payload variants, exact collected fields, and
      unsupported event types.
- [x] Document lifecycle selection and the internal `agent-skills` APM mapping.
- [x] Keep the retained APM package accurate: it has no native Cline hook asset, while the lifecycle installer manages
      the global file hook.
- [x] Update package descriptions and setup guidance where the supported-harness list is normative.
- [x] Run Markdown lint or the repository's documentation validation.

### Task 6: Verify implementation and real Cline clients

**Files:**

- Modify only if evidence requires a fix: implementation and maintained documentation above.
- Preserve: `docs/superpowers/research/2026-08-05-cline-cli-hook-probe-result.md`

- [x] Run formatting, static analysis, unit tests, race tests, and repository validation targets.
- [x] Build a temporary development binary named `ai-agent-telemetry` outside the repository build output.
- [x] Back up any existing Cline global `PostToolUse` file, install the managed Cline hook with the development binary,
      and verify `status` reports `cline: installed`.
- [x] Create a temporary inert Cline probe skill with a unique name and checksum.
- [x] Run Cline CLI with the development binary first on `PATH`; invoke the probe skill exactly once and record the
      local outbox or hook evidence.
- [x] Use the installed Cline VS Code Extension to invoke the same probe skill exactly once and record equivalent
      evidence.
- [x] Open the authorized VictoriaLogs UI, filter for `agent=cline` and the unique probe skill, and verify both events
      have only the approved event attributes.
- [x] Remove the temporary skill, restore or guardedly remove the hook, remove temporary binaries, and verify no test
      artifact remains active.
- [x] Re-run the complete verification suite after any live-test fix.
