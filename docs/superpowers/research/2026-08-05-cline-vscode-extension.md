# Cline VS Code Extension telemetry research

Research snapshot from August 5, 2026. This document records the inputs needed to add the Cline VS Code Extension as
a supported harness in `ai-agent-telemetry`. It covers skill discovery, hook execution, session storage, native
OpenTelemetry, installer constraints, and the backend changes proposed in pull request 26.

The recommended collection path is a global `PostToolUse` file hook that calls the `ai-agent-telemetry` CLI. Cline's
native metrics are a separate, supplementary signal. Native Cline logs are not suitable for skill collection under the
current privacy contract.

## Evidence and versions

The source review uses the following revisions:

- Cline VS Code Extension 4.1.3, which is installed on the research machine.
- Cline commit `d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a`, which prepares VS Code Extension 4.1.4.
- `ai-agent-telemetry` main commit `88e7b41da3de9c6625d8cb278488084e0cf8173d`.
- `ai-agent-telemetry` pull request 26 head `de75b951e6829bc948dd81d4fa7ca53ff696d1bd`.

The hook, skill, and telemetry files reviewed for Cline 4.1.3 and 4.1.4 are equivalent for this integration. The only
observed difference in the selected runtime files is an unrelated plan-mode command guard.

The research used three evidence classes:

1. Version-pinned Cline source code and tests.
2. Version-pinned `ai-agent-telemetry` pull request contents.
3. A read-only inspection of file names, JSON shapes, and SQLite schema on the research machine.

No Cline hook, setting, session, or telemetry exporter was changed during this research.

## Decision summary

Use Cline's `PostToolUse` file hook to detect successful skill execution. Accept both `skills` and `use_skill` as tool
names, then read the skill name from `skill`, `skill_name`, or `skillName`. Emit the existing `skill_executed` envelope
with `agent` set to `cline`.

Do not parse Cline transcripts in the normal collection path. Cline maintains legacy VS Code task files and a newer
shared session store. Neither format provides a better contract than the native hook payload.

Do not enable native Cline logs for skill detection. Cline emits `task.skill_used` as an OTLP log with user and
organization attributes. Pull request 26 applies its privacy processors only to metrics.

Use the APM `agent-skills` target for Cline skill deployment. APM 0.13.0 does not define a `cline` target, while Cline
scans both project and global `.agents/skills` directories.

Create the implementation branch from an updated `main` after pull request 26 merges. If that pull request remains
blocked, split client integration from backend alignment and start the client integration from the latest `main`.

## Cline extension identity

The VS Code Marketplace extension identifier is `saoudrizwan.claude-dev`. The installed extension uses this identifier
even though the product name is Cline.

The extension uses two storage generations:

- VS Code extension global storage for legacy tasks.
- Shared Cline storage under `~/.cline/data` for SDK sessions and other cross-client state.

The implementation must not infer the harness from a storage directory alone. The hook registration supplies the
trusted harness identity through `ai-agent-telemetry ingest --agent=cline`.

## Skill discovery and activation

Cline scans these project directories in order:

1. `.clinerules/skills`
2. `.cline/skills`
3. `.claude/skills`
4. `.agents/skills`

It then scans these global directories:

1. `~/.cline/skills`
2. `~/.agents/skills`

Each skill is a directory containing `SKILL.md`.

The current SDK runtime registers the activation tool as `skills`. Its required input field is `skill`; `args` is
optional. Runtime configuration maps the older `use_skill` name to `skills`, and the VS Code message translator
recognizes both names.

Cline's own telemetry extractor accepts three parameter spellings:

```text
skill
skill_name
skillName
```

The telemetry adapter should accept the same spellings. This keeps the detector compatible with current SDK events,
configured-agent aliases, and older payloads.

## File hook model

Cline declares these file hook types:

```text
TaskStart
TaskResume
TaskCancel
TaskComplete
PreToolUse
PostToolUse
UserPromptSubmit
Notification
PreCompact
```

The SDK runtime adapter wires these hook types:

| Cline hook | Runtime callback | Integration relevance |
| --- | --- | --- |
| `TaskStart` | `beforeRun` | Not required for skill detection |
| `UserPromptSubmit` | `beforeRun` | Contains private prompt text; do not collect |
| `PreToolUse` | `beforeTool` | Sees activation intent but not the outcome |
| `PostToolUse` | `afterTool` | Recommended; includes parameters and success |
| `TaskComplete` | `afterRun` | Not required for skill detection |
| `TaskCancel` | `afterRun` | Not required for skill detection |

`TaskResume`, `PreCompact`, and `Notification` are declared but are not wired through the reviewed SDK runtime adapter.

### Hook locations

The global hook directory is:

```text
~/Documents/Cline/Hooks/
```

Each workspace can also provide:

```text
<workspace-root>/.clinerules/hooks/
```

A multi-root workspace can contribute one workspace hook directory per root. Cline discovers the runtime hook
directory, global directory, and workspace directories, then runs matching hooks concurrently. It does not guarantee
execution order across directories.

### Platform-specific names and execution

On macOS and Linux, Cline recognizes only an executable extensionless file:

```text
PostToolUse
```

The executable bit is also the enabled state. Mode `0755` enables the hook; mode `0644` disables it.

On Windows, Cline recognizes only:

```text
PostToolUse.ps1
```

Cline invokes it with PowerShell using these arguments:

```text
-NoProfile -NonInteractive -ExecutionPolicy Bypass -File <hook-path>
```

Windows hook toggling is not implemented. The file is active when it exists. Cline's source contains a follow-up note
for a JSON-backed enabled state.

### Hook input

Cline serializes one JSON object to stdin. Common fields are:

```json
{
  "clineVersion": "4.1.3",
  "hookName": "PostToolUse",
  "timestamp": "<milliseconds-since-epoch>",
  "taskId": "<conversation-or-run-id>",
  "workspaceRoots": ["<absolute-workspace-path>"],
  "userId": "<cline-user-or-machine-id>",
  "model": {
    "provider": "<provider>",
    "slug": "<model>"
  }
}
```

`PostToolUse` adds:

```json
{
  "postToolUse": {
    "toolName": "skills",
    "parameters": {
      "skill": "<skill-name>"
    },
    "result": "<unbounded-tool-result>",
    "success": true,
    "executionTimeMs": 123
  }
}
```

The runtime converts each parameter value to a string. It preserves string values and applies `JSON.stringify` to
other values.

The adapter must decode an allowlist. It must not decode or emit `userId`, model details, arbitrary parameters, or the
tool result.

### Hook output and failure behavior

A hook can return this JSON shape on stdout:

```json
{
  "cancel": false,
  "contextModification": "",
  "errorMessage": ""
}
```

All fields are optional. For telemetry, the hook should return `{"cancel":false}` and write diagnostics only to
stderr.

Cline applies these limits:

- 30-second execution timeout.
- 1 MiB combined stdout and stderr limit.
- 50,000-character `contextModification` limit.

`PostToolUse` runs after the tool finishes. Its adapter ignores a cancellation result and catches hook errors, so a
telemetry failure does not replace the tool result. The telemetry CLI also treats malformed or unsupported input as a
no-event result.

## Recommended detector contract

The Cline adapter should follow this sequence:

1. Parse the top-level hook envelope.
2. Ignore events whose `hookName` is not `PostToolUse`.
3. Read only `taskId`, `workspaceRoots`, and the allowlisted `postToolUse` fields.
4. Accept `toolName` values `skills` and `use_skill`.
5. Require `success` to be `true`.
6. Read the first non-empty string from `skill`, `skill_name`, and `skillName`.
7. Resolve repository identity from the applicable workspace root.
8. Create `skill_executed` with `agent=cline`.
9. Ignore malformed input, unsuccessful calls, unrelated tools, and missing skill names.

The experiment must establish how `workspaceRoots` maps to the active root in a multi-root workspace. The reviewed
payload does not contain a separate working directory. A global hook runs with the primary workspace root as its
process working directory, but that value is not an explicit JSON field.

The experiment must also establish whether subagent skill calls reach the same `PostToolUse` adapter and how Cline
sets `taskId` for those calls.

## Hook installation constraints

Cline supports one hook file per hook type and directory. It can run a global `PostToolUse` and one or more workspace
`PostToolUse` files, but it cannot represent multiple independent global handlers in one configuration object.

Cline's hook creation controller refuses to create a hook when the exact file already exists. The telemetry installer
must preserve the same safety boundary.

The first implementation should use these ownership rules:

- Create a telemetry-owned global `PostToolUse` or `PostToolUse.ps1` when the path is absent.
- Record ownership in stable file content that status and uninstall can verify.
- Report `installed` only when the file is byte-for-byte or structurally equivalent to the supported telemetry hook.
- Report a conflict when an unknown file occupies the path.
- Leave a conflicting file byte-for-byte unchanged.
- Remove only a verified telemetry-owned file.
- Continue installing or removing other harnesses when Cline fails.

An automatic dispatcher is outside the first implementation. A dispatcher would need to preserve stdin for multiple
children, run platform-specific scripts, merge hook output JSON, propagate cancellation correctly, and recover the
original hook during uninstall.

The experiment must verify that the VS Code Extension Host can resolve the bare `ai-agent-telemetry` command. GUI
launches on macOS and Windows may expose a different `PATH` from an interactive shell.

## APM integration

APM CLI 0.13.0 accepts these deployment targets:

```text
copilot
claude
cursor
opencode
codex
gemini
windsurf
agent-skills
all
```

It does not accept `cline`. Cline scans `.agents/skills`, so the lifecycle installer needs an explicit mapping:

```text
cline harness -> agent-skills APM target
```

The lifecycle selection and APM deployment target are no longer the same enum. For example:

```text
--harnesses codex,cline -> apm --target codex,agent-skills
```

The Go lifecycle installer should manage the global Cline hook. The APM package should deploy shared skills through
`agent-skills`; it should not add a `skill-call-cline-hooks.json` file until APM defines a Cline hook target and merge
contract.

Cline Plugins are not an alternative for the VS Code phase. Cline documents Plugins for SDK, CLI, and Kanban, and
explicitly excludes the VS Code and JetBrains extensions.

## Session and log storage

### Legacy VS Code storage

On the research machine, Cline 4.1.3 stores legacy VS Code state under:

```text
~/Library/Application Support/Code/User/globalStorage/saoudrizwan.claude-dev/
```

The relevant layout is:

```text
state/taskHistory.json
tasks/<task-id>/api_conversation_history.json
tasks/<task-id>/ui_messages.json
tasks/<task-id>/task_metadata.json
```

Observed shapes:

- `taskHistory.json` is a JSON array of task summaries.
- `api_conversation_history.json` is a JSON array of messages with `role`, `content`, and `ts`.
- `ui_messages.json` is a JSON array of Cline UI messages.
- `task_metadata.json` is a JSON object with environment, file-context, and model-usage data.

These are complete JSON documents, not append-only JSONL streams.

### Shared Cline storage

The shared storage defaults to:

```text
~/.cline/data/
```

Relevant files include:

```text
sessions/<session-id>/<session-id>.json
sessions/<session-id>/<session-id>.messages.json
db/sessions.db
logs/cline.log
```

The session manifest is a JSON object. The messages file is a JSON object that contains a `messages` array. The
`sessions.db` SQLite table records session, parent/subagent, agent, conversation, workspace, transcript, hook, and
message paths.

`cline.log` is an NDJSON diagnostic log. Observed entries include process, component, working-directory, event, and
message fields. Diagnostic logs can contain private prompts and operational context, so the adapter must not read them
for normal detection.

Cline resolves storage in this order:

```text
CLINE_DATA_DIR
CLINE_DIR/data
~/.cline/data
```

`CLINE_SESSION_DATA_DIR` can override the session directory separately.

### Storage verdict

Transcript parsing is a poor primary integration because:

- two storage generations coexist;
- legacy tasks remain readable without immediate migration;
- files are rewritten JSON documents rather than append-only streams;
- shared sessions add SQLite metadata and nested session artifacts;
- diagnostic logs contain more private data than the hook needs.

Keep storage readers out of the first Cline adapter. Use them only for controlled troubleshooting if a live hook probe
shows a coverage gap.

## Native OpenTelemetry

Cline can create OTLP metric and log exporters. Runtime environment variables include:

```text
CLINE_OTEL_TELEMETRY_ENABLED
CLINE_OTEL_METRICS_EXPORTER
CLINE_OTEL_LOGS_EXPORTER
CLINE_OTEL_EXPORTER_OTLP_PROTOCOL
CLINE_OTEL_EXPORTER_OTLP_ENDPOINT
CLINE_OTEL_EXPORTER_OTLP_HEADERS
CLINE_OTEL_EXPORTER_OTLP_METRICS_PROTOCOL
CLINE_OTEL_EXPORTER_OTLP_METRICS_ENDPOINT
CLINE_OTEL_EXPORTER_OTLP_LOGS_PROTOCOL
CLINE_OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
CLINE_OTEL_METRIC_EXPORT_INTERVAL
CLINE_OTEL_EXPORTER_OTLP_INSECURE
```

The implementation checks `CLINE_OTEL_TELEMETRY_ENABLED === "true"`. A nearby source comment says `"1"`, which does
not match the code. Any environment-based experiment must use `true` and verify the effective configuration after a
full VS Code reload.

The reviewed VS Code provider sets:

```text
service.name = cline
service.version = <extension-version>
```

Do not treat these values as a permanent public contract until a live, version-pinned export confirms them.

### Native skill event

Cline detects a `skills` tool content event and emits `task.skill_used`. The event includes the session ULID, skill
name, skill source, provider, model, and agent identity fields.

The OpenTelemetry provider emits generic events through the logs signal. It can attach these attributes:

```text
distinct_id
user_id
user_name
organization_id
organization_name
member_id
member_role
```

This makes native logs unsuitable for the first onboarding phase. The repository-generated hook event decodes a small
allowlist and provides a stronger privacy boundary.

Native metrics can complement hook telemetry after a separate privacy and cardinality review. They do not replace
repository-aware `skill_executed` events.

## Pull request 26 backend changes

Pull request 26 adds the native metrics backend and substantial operational tooling. Its functional backend changes
include:

- VictoriaMetrics 1.148.0 with a persistent volume and 365-day default retention.
- Authenticated `/v1/metrics` and `/v1/logs` routes in Caddy.
- A Collector metrics pipeline with a memory limiter, privacy processors, delta-to-cumulative conversion, and batch.
- Removal of `session.id`, `user.email`, `user.account_uuid`, and `organization.id` from metric resources and points.
- A 100,000-stream delta conversion bound.
- Default hourly and daily series limits of 50,000 and 200,000.
- A 1 GiB minimum free-disk reserve.
- A VictoriaMetrics Grafana data source and native metrics dashboards.
- Backend release packaging, backup, update, rollback, recovery, and retention scripts.

The Cline fixture is manually authored:

```text
service.name = cline-fixture
metric name = cline.fixture.task.count
```

It proves that the authenticated backend accepts a metric payload and removes the four tested sensitive attributes. It
does not validate a Cline client, exporter configuration, headers, metric names, stable resource labels, or dashboard
selection.

The pull request support matrix classifies Cline as follows:

| Capability | Pull request 26 state |
| --- | --- |
| Hook events | Not supported |
| Native metrics | Supported |
| Validation | Backend fixture |
| APM | Not supported |

The pull request documentation also states that its lifecycle installer configures hook telemetry only and that Cline
requires manual Remote Configuration for native metrics.

### Backend privacy gap for Cline logs

The pull request applies privacy processors to the metrics pipeline. Its logs pipeline contains only the memory limiter
and batch processors. Cline's native log attributes use underscore names such as `user_id` and `organization_id`, while
the metric removal list uses dotted vendor attributes such as `user.email` and `organization.id`.

Sending native Cline logs to this backend can persist user and organization identifiers. The Cline experiment must not
enable the logs exporter against a shared or production backend.

### Backend work still needed for first-class Cline support

The Cline feature still needs:

- `cline` in the CLI event-agent allowlist and tests;
- a Cline hook adapter and sanitized fixtures;
- Cline hook install, status, update, and uninstall behavior;
- lifecycle and CLI harness selection;
- APM mapping to `agent-skills`;
- backend hook-event fixtures and dashboard expectations;
- a real native metrics pilot before any Cline metric dashboard is added;
- a privacy review of observed native resource and data-point attributes.

## Branch assessment

The open pull requests reviewed on August 5, 2026 were:

- Pull request 26, multi-harness native OTLP metrics and backend operations. It is blocked pending review.
- Pull request 29, Cursor nested and subagent attribution. It has changes requested and is behind `main`.
- Pull request 32, a Renovate security update. It is unrelated to Cline behavior.

Pull request 30 merged during the research. The local `main` was updated again after that merge.

The implementation should not branch from an open pull request head. Branch from updated `main` after pull request 26
merges. If the client work must start earlier, isolate it from the native metrics work and rebase onto `main` before
publication.

## Required live experiment

Source review leaves several runtime questions that a controlled extension experiment can answer:

1. Does a successful skill activation produce `toolName=skills` in Cline 4.1.3?
2. Which parameter spelling appears in the raw hook payload?
3. Does an unsuccessful activation set `success=false` and still invoke `PostToolUse`?
4. What `taskId` does the main agent use?
5. Do subagent skill calls invoke the same hook, and how do their task IDs relate?
6. Which workspace root applies in a multi-root workspace?
7. What process working directory does a global hook receive?
8. Can the VS Code Extension Host resolve `ai-agent-telemetry` by its bare name?
9. How quickly does Cline detect create, permission, and content changes to a hook file?
10. Does deleting the telemetry-owned hook disable it without a VS Code restart?
11. Which native metrics and attributes does Cline 4.1.3 export with logs disabled?

The experiment should capture schemas and execution metadata, not prompts, tool results, user IDs, model names, file
contents, or authentication headers.

## Sources

- [Cline repository at the reviewed commit][cline-commit]
- [Cline hook types and locations][cline-hook-utils]
- [Cline hook input protobuf][cline-hook-proto]
- [Cline SDK hook adapter][cline-hook-adapter]
- [Cline hook runner and discovery][cline-hook-factory]
- [Cline skill directories][cline-skill-directories]
- [Cline skills tool definition][cline-skills-tool]
- [Cline runtime OpenTelemetry configuration][cline-otel-config]
- [Cline OpenTelemetry provider][cline-otel-provider]
- [Cline shared storage paths][cline-storage-paths]
- [Cline Plugins scope][cline-plugins]
- [`ai-agent-telemetry` pull request 26][telemetry-pr-26]

[cline-commit]: https://github.com/cline/cline/tree/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a
[cline-hook-utils]: https://github.com/cline/cline/blob/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a/apps/vscode/src/core/hooks/utils.ts
[cline-hook-proto]: https://github.com/cline/cline/blob/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a/apps/vscode/proto/cline/hooks.proto
[cline-hook-adapter]: https://github.com/cline/cline/blob/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a/apps/vscode/src/sdk/hooks-adapter.ts
[cline-hook-factory]: https://github.com/cline/cline/blob/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a/apps/vscode/src/core/hooks/hook-factory.ts
[cline-skill-directories]: https://github.com/cline/cline/blob/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a/apps/vscode/src/core/storage/skill-directories.ts
[cline-skills-tool]: https://github.com/cline/cline/blob/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a/sdk/packages/core/src/extensions/tools/definitions.ts
[cline-otel-config]: https://github.com/cline/cline/blob/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a/apps/vscode/src/shared/services/config/otel-config.ts
[cline-otel-provider]: https://github.com/cline/cline/blob/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a/apps/vscode/src/services/telemetry/providers/opentelemetry/OpenTelemetryTelemetryProvider.ts
[cline-storage-paths]: https://github.com/cline/cline/blob/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a/sdk/packages/shared/src/storage/paths.ts
[cline-plugins]: https://github.com/cline/cline/blob/d626cfb0b5c09be1bb84ea6d36fb106e4ce9251a/docs/customization/plugins.mdx
[telemetry-pr-26]: https://github.com/Netcracker/qubership-ai-agent-telemetry/pull/26
