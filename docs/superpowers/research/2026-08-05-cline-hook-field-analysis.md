# Cline `PostToolUse` Hook Field Analysis

Research snapshot from August 5, 2026. This document maps the payload captured during the controlled Cline VS Code
Extension hook probe to the existing `ai-agent-telemetry` event contract. It covers hook-based skill telemetry only.
Cline native OpenTelemetry metrics and logs are separate signals.

The raw event remains local and is not reproduced here. The analysis records field names, JSON types, bounded shape
facts, and mapping decisions without copying user, model, workspace, or tool-result values.

## Evidence and confidence

The live sample came from a successful project skill invocation in Cline. The extension displayed the skill load, ran
the global `PostToolUse` hook, accepted `{"cancel":false}`, and completed the task. Exactly one captured event matched
the probe predicate.

This is one positive-path sample with one workspace root. It confirms the observed shape for that call, but it does not
establish behavior for failed skill calls, multi-root workspaces, subagents, aliases from other Cline versions, or
repeated invocations.

The mapping also uses these repository contracts:

- a `skill_executed` event contains `agent`, `session.id`, optional `repo.remote`, and `skill.name`;
- the CLI generates `event.id` and records the event time;
- local paths may be used in memory for repository resolution and policy checks but are never serialized;
- prompts, tool inputs and results, model identifiers, user identifiers, and arbitrary fields must not leave the
  machine.

## Observed shape

The captured top-level object contained eight fields:

| Field | JSON type | Observed shape |
| --- | --- | --- |
| `clineVersion` | string | Non-empty version-like value |
| `hookName` | string | `PostToolUse` |
| `timestamp` | string | 13 decimal digits |
| `taskId` | string | 13 decimal digits |
| `workspaceRoots` | array | One absolute path in this sample |
| `userId` | string | Non-empty opaque identifier |
| `model` | object | `provider` and `slug` string fields |
| `postToolUse` | object | Five fields described below |

The `postToolUse` object contained:

| Field | JSON type | Observed shape |
| --- | --- | --- |
| `toolName` | string | `use_skill` |
| `parameters` | object | `skill_name` and `task_progress` string fields |
| `result` | string | Non-empty, unbounded tool output |
| `success` | boolean | `true` |
| `executionTimeMs` | number | Non-negative integer |

The observed `clineVersion` value differed from the installed VS Code package version. It must not be treated as a
verified extension version without a separate version-source investigation.

## Field decisions

### `clineVersion`

- **Role in detection:** none.
- **Telemetry mapping:** none.
- **Decision:** ignore.
- **Reason:** the observed value did not match the installed package version. The current hook event schema also has no
  harness-version attribute. Exporting it would create an unverified dimension with no first-stage requirement.

### `hookName`

- **Role in detection:** require the exact value `PostToolUse`.
- **Telemetry mapping:** none.
- **Decision:** decode only as an event discriminator.
- **Reason:** a global hook receives a hook-specific payload, but checking the discriminator prevents an unexpected
  shape from being interpreted as a skill event.

### `timestamp`

- **Role in detection:** none for the first implementation.
- **Telemetry mapping:** none; use the CLI receive time for `ts`.
- **Decision:** ignore.
- **Reason:** existing adapters use the time supplied by the CLI ingestion path. Keeping that rule avoids trusting an
  undocumented client clock field and keeps event-time behavior consistent across harnesses. A later change would need
  validation for units, clock skew, missing values, and malformed values.

### `taskId`

- **Role in detection:** required session identifier.
- **Telemetry mapping:** `session.id`.
- **Decision:** decode and validate with the existing session identifier profile.
- **Reason:** the observed value fits the current 1 to 128 character ASCII contract and identifies the Cline task that
  produced the hook event. It must be treated as opaque. No numeric parsing or timestamp interpretation is needed.

The sample suggests that `taskId` may encode a task start time, but that inference is not part of the contract. A
subagent experiment is still needed to determine whether child calls reuse or replace it.

### `workspaceRoots`

- **Role in detection:** local repository attribution.
- **Telemetry mapping:** resolve an allowed normalized Git remote into `repo.remote`.
- **Decision:** decode only the root candidates needed for local resolution. Never serialize a root path.
- **Reason:** absolute workspace paths can expose usernames and directory structure. The current privacy contract
  permits local paths only as in-memory inputs to repository resolution and policy checks.

The single-root sample supports using its only element. It does not justify selecting the first element in a multi-root
workspace. The payload has no explicit active root, current working directory, or skill file path. Until a multi-root
experiment or version-pinned Cline source contract identifies the active root, the safe production rule is:

1. resolve all non-empty roots locally;
2. emit only when exactly one allowed normalized remote remains, including the case where several roots resolve to the
   same remote;
3. suppress repository-scoped emission when different allowed remotes remain and the active one cannot be proven.

This conservative rule avoids attributing a skill call to the wrong repository. It can reuse the stronger attribution
work from pull request 29 after that change is merged.

### `userId`

- **Role in detection:** none.
- **Telemetry mapping:** none.
- **Decision:** do not decode into the typed adapter structure.
- **Reason:** it is an opaque user or installation identifier and is unnecessary for skill-usage analytics. The CLI's
  anonymous `machine.id` already represents an installation without copying a harness identifier.

### `model.provider`

- **Role in detection:** none.
- **Telemetry mapping:** none.
- **Decision:** do not decode.
- **Reason:** model identifiers are explicitly outside the hook telemetry allowlist.

### `model.slug`

- **Role in detection:** none.
- **Telemetry mapping:** none.
- **Decision:** do not decode.
- **Reason:** model identifiers are explicitly outside the hook telemetry allowlist.

### `postToolUse.toolName`

- **Role in detection:** require `use_skill`; also accept `skills` for the version-pinned SDK shape found in source.
- **Telemetry mapping:** none.
- **Decision:** decode only as a tool discriminator.
- **Reason:** the live VS Code extension used `use_skill`, while reviewed Cline code and telemetry extraction support
  another skill-tool name. The allowlist must remain exact and must not accept arbitrary tools.

### `postToolUse.parameters.skill_name`

- **Role in detection:** required skill name in the observed VS Code payload.
- **Telemetry mapping:** `skill.name` inside the existing `skill_executed` event.
- **Decision:** decode, require a non-empty string, and validate with the existing skill-name profile.
- **Reason:** this is the only observed parameter needed for the event.

For compatibility with the version-pinned Cline source, the adapter may read the first valid non-empty string from
`skill`, `skill_name`, and `skillName`. It must define these three fields explicitly rather than retain the whole
parameters object. The live fixture must lock `skill_name` as the observed VS Code case.

### `postToolUse.parameters.task_progress`

- **Role in detection:** none.
- **Telemetry mapping:** none.
- **Decision:** do not decode.
- **Reason:** task progress is user and agent content. It is unbounded, unnecessary for skill counting, and can contain
  private task context.

### `postToolUse.result`

- **Role in detection:** none.
- **Telemetry mapping:** none.
- **Decision:** do not decode.
- **Reason:** tool results are unbounded private content. The captured result included operational text from the skill
  call, confirming that it is not safe metadata.

### `postToolUse.success`

- **Role in detection:** require `true` for `skill_executed`.
- **Telemetry mapping:** none.
- **Decision:** decode as a filter, not as an exported outcome.
- **Reason:** the existing event means that the skill was executed successfully and has no outcome field. A failed
  `use_skill` call must not be counted as successful skill usage. A controlled failure experiment is required to verify
  that Cline still emits `PostToolUse`, and to capture its safe structural shape, before considering a separate failure
  signal.

### `postToolUse.executionTimeMs`

- **Role in detection:** none.
- **Telemetry mapping:** none in the first implementation.
- **Decision:** ignore.
- **Reason:** `skill_executed` has no duration attribute. Adding one would expand the shared event schema and backend
  contract for a field available from only one harness. The first stage does not need that expansion.

### Unknown top-level, nested, and parameter fields

- **Role in detection:** none.
- **Telemetry mapping:** none.
- **Decision:** ignore through typed allowlist decoding.
- **Reason:** Cline can add fields and tool parameters over time. Retaining a generic map would make accidental export
  possible and weaken the current privacy boundary.

## Resulting event

For a valid sample, the adapter should create the existing event shape with synthetic values equivalent to:

```json
{
  "schema_version": 1,
  "event_name": "skill_executed",
  "event_id": "<generated-by-cli>",
  "agent": "cline",
  "session_id": "<opaque-task-id>",
  "repo_remote": "<locally-resolved-normalized-remote>",
  "ts": "<cli-receive-time>",
  "payload": {
    "skill_name": "<validated-skill-name>"
  }
}
```

Only `taskId` and the selected skill-name parameter cross directly from the Cline payload into the serialized event.
The repository remote is derived locally. `agent`, event name, schema version, event ID, and event time are supplied by
the CLI.

## Detector contract

The first Cline adapter should:

1. Parse a small top-level structure containing only `hookName`, `taskId`, `workspaceRoots`, and `postToolUse`.
2. Require `hookName=PostToolUse`.
3. Parse only `toolName`, `success`, and the three allowlisted skill-name parameter spellings from `postToolUse`.
4. Require `toolName` to be `use_skill` or `skills`.
5. Require `success=true`.
6. Select and validate one skill name.
7. Resolve an unambiguous allowed repository remote locally.
8. Create `skill_executed` with `agent=cline` and CLI receive time.
9. Return no event for malformed, unsupported, failed, ambiguous, or privacy-unsafe input.

Like the existing adapters, a no-event result must not fail the Cline hook. The hook must return `{"cancel":false}`
even when detection or delivery fails.

## Backend effect

Hook events use the existing OTLP logs pipeline, not the native metrics pipeline from pull request 26. The current
collector and Adoption dashboard group hook events by the dynamic `agent` attribute, so `agent=cline` does not require
a new storage schema or Cline-specific dashboard selector.

The backend still needs contract coverage:

- add a sanitized Cline `skill_executed` record to the hook-event fixture;
- include `cline` in the smoke assertion that currently expects only `claude`, `codex`, and `cursor`;
- confirm the Adoption and Telemetry health harness variables expose `cline` from stored data;
- keep native Cline logs disabled because their vendor payload and privacy contract are separate from the strict hook
  allowlist.

Pull request 26 provides a Cline native-metrics fixture, but it is manually authored and intentionally uses a
test-scoped service name. It proves backend OTLP metrics compatibility only. It does not validate this hook payload,
the Cline client, a stable metric selector, or hook-event privacy.

## Open pull requests and base branch

The active pull requests relevant on August 5, 2026 are:

- **Pull request 26, native metrics and backend operations.** It changes backend fixtures, dashboards, and
  documentation but does not add a Cline hook adapter. Do not branch from its head.
- **Pull request 29, Cursor repository and subagent attribution.** It directly changes `detect.go`, `hooks.go`, policy,
  and related tests. Reuse it after merge or rebase the Cline work carefully.
- **Pull request 36, batched outbox flush.** It changes `flush.go` and flush tests only. It does not block Cline work.

Production implementation should start from updated `main`, never from an open pull request head. If pull request 29
merges first, use that updated `main` because its repository-attribution work overlaps the Cline detector. Pull request
26 does not need to block the client hook adapter, but the feature branch should be rebased onto `main` after it merges
before the Cline pull request is published. Pull request 36 is orthogonal.

The current research branch is correctly based on `main` and should remain documentation-only.

## Required follow-up experiments

Before implementation is considered complete, run these bounded experiments with the same redaction rules:

1. **Multi-root workspace:** invoke one project skill with two roots whose repositories have different remotes. Record
   only root count, resolution cardinality, process working-directory relation, and which attribution rule is proven.
2. **Failed skill call:** trigger a controlled missing or invalid skill. Record only hook presence, `success`, field
   names and types, and whether a skill-name parameter remains available.
3. **Repeated invocation:** call the same skill twice in one task and confirm two hook events share `taskId` but produce
   distinct CLI-generated event IDs.
4. **Subagent invocation:** invoke a skill from a Cline subagent and determine whether the global hook runs and whether
   `taskId` is shared or replaced.
5. **Tool-name compatibility:** validate a version or client surface that emits `skills`, if one is available. The live
   VS Code fixture already locks `use_skill`.
6. **Hook lifecycle:** verify create, update, status, conflict, and uninstall behavior on macOS and Windows without
   replacing a user-owned `PostToolUse` file.

The first implementation can proceed with the positive single-root path while tests make every unproven case fail
closed. Multi-root attribution and ownership-safe installation remain release gates for full Cline onboarding.
