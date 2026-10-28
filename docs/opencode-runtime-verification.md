# OpenCode runtime verification

The OpenCode integration was exercised on macOS arm64 with OpenCode 2.1.4 and unchanged OpenChamber web 2.5.1.
The test used a deterministic local provider, stdio MCP fixtures, isolated configuration/cache/data roots, and the
compiled telemetry CLI. A loopback HTTPS receiver decoded the actual gzip OTLP protobuf requests. A separate
verification run sent real source events through the configured HTTPS collector to the deployed VictoriaLogs instance.

## Observed behavior

- Successful native skill calls emitted Skill.ID, including a skill whose display name contained spaces.
- Missing and permission-denied skills emitted no skill event.
- A structured attachment emitted one event on confirmed delivery. Enqueue alone, cancellation, missing IDs, and
  synthetic instructions did not count.
- Reload between enqueue and delivery preserved processing through the public context message lookup.
- Direct MCP success, protocol error, and transport closure produced the corresponding outcomes.
- Three parallel nested calls sharing one outer ID produced three records: two succeeded and one failed, with no
  individual durations. Child-session skill and MCP calls were attributed to the child in a second fixture location.
- The unchanged OpenChamber picker delivered a manually attached skill through the external OpenCode server. A second
  unchanged OpenChamber process launched OpenCode itself and completed an MCP call through its managed server.

The final source-to-receiver comparison covered 15 scenarios, including negative cases, and matched 15 expected
records by session, event type, count, and outcome. The complete receiver log contained 45 unique event IDs across the
successive verification runs; no duplicate IDs occurred, and the outbox was empty after explicit flush. Serialized
records contained no fixture skill bodies, tool arguments, or local paths.

Two UI compatibility examples from the receiver:

| Path | Event ID | Captured fields |
| --- | --- | --- |
| OpenChamber manual attachment | `01a17d73-8037-7d8c-93db-948e98c2b68a` | `agent=opencode`, `skill.name=probe-attach` |
| OpenChamber managed-server MCP | `01a17d77-bc8c-7349-a04a-52851364930f` | `agent=opencode`, `mcp.server.name=probe_server`, `mcp.tool.name=echo.tool` |

## Final VictoriaLogs delivery

The separate run used the build version `v0.0.0-opencode164-victoria-20261027` to isolate its records. A direct
VictoriaLogs query matched all 12 expected records: six skill events and six MCP events. Every `event.id` occurred
exactly once. Source session context confirmed the native skill calls and the structured manual attachment; missing
and denied skills produced zero records. The outbox was empty after flush, and stored records contained no tool
arguments, skill bodies, local paths, or duration fields.

| Scenario | Expected and stored records | Example event ID |
| --- | --- | --- |
| Native skill, including child session | 2 | `01a17d8d-424a-73ef-9a06-d9bf997b69b4` |
| Standalone skill activation | 1 | `01a17d8d-4698-7be1-959d-cfaf3e538822` |
| Structured attachment through OpenCode | 1 | `01a17d8d-4706-7594-9371-89c71d6d6db5` |
| Manual picker attachment through unchanged OpenChamber | 1 | `01a17d8e-7c78-757e-a475-31f3a0997f33` |
| Direct MCP success, protocol error, transport closure | 3 | `01a17d8d-4250-75b8-a24f-78ec5991419e` |
| Three nested MCP calls sharing one outer ID | 3 | `01a17d8d-44e7-7da8-b7bc-97e29434d88e` |
| Collector retry after a failed flush | 1 | `01a17d90-8207-7097-b123-961bb6de3452` |

The nested records retained two successful outcomes and one failed outcome. Direct error and transport closure both
retained failed outcomes. Real source calls with forbidden or disabled collection produced zero records. A forced
connection failure preserved the queued event byte for byte; retry delivered its original ID exactly once. The query
was repeated after indexing caught up. The test used existing collector credentials in place; it did not change the installed
telemetry CLI, personal telemetry configuration, OpenChamber files, or backend configuration.

## Performance review follow-up

The review fixes were exercised with OpenCode 2.1.4 using build
`v0.0.0-opencode173-review2-20261028`. All 15 expected source records reached the deployed VictoriaLogs, with 15
unique event IDs and an empty outbox after flush. The comparison included native and attached skills, an attachment
enqueued before plugin reload, child sessions in a second location, and direct/nested MCP success and failure.
The reload attachment reached VictoriaLogs as `01a181dc-7b22-7e4b-b2d0-53bb69e8e97d`.

The plugin now caches only attachment IDs from observed enqueue events. Observed ordinary prompts avoid context
reads; an unobserved enqueue still uses context for reload recovery. Pending MCP calls use per-tool queues and cursors
so completion matching does not search or shift the remaining batch. Deterministic operation counts establish these
work bounds; they do not measure production latency. The JavaScript suite contains 27 passing tests.

A subsequent cache-lifecycle fix groups queued entries by session. Deleting a session drops its complete bucket;
delivery and cancellation also remove empty buckets. The regression exercises 1,000 enqueue/delete pairs and
matching delivery/cancellation controls, with zero retained session entries, and checks that another session survives.
A real OpenCode deletion run using `v0.0.0-opencode173-delete-20261028` matched 18 expected records and
18 unique IDs in final VictoriaLogs. The deleted queued session produced zero skill events; the surviving queued
attachment produced one. One child session performed two distinct native skill calls, confirmed by source call IDs;
both were counted. The outbox was empty after flush.

Committed reverts also invalidate the affected session's cache bucket. A 1,000-cycle regression retains no discarded
inbox entries, while surviving attachments still resolve through context and other sessions retain their cached IDs.
A real OpenCode run using `v0.0.0-opencode173-revert-20261028` matched 17 expected records and
17 unique IDs in final VictoriaLogs. The committed revert removed its queued input and emitted no skill event;
another session's queued attachment emitted one. The outbox was empty after flush.

## Automated checks

`make test` runs the plugin's Node.js tests and the complete Go race suite. The plugin tests cover distinct nested
completions, denied calls without after hooks, terminal outer failures, late callbacks, live registry changes,
namespace collisions, cached attachment IDs with context-based reload recovery, cleanup, and reconnect without
historical reconstruction.
Operation-count regressions cover 1,000 and 2,000 pending tools completing in reverse order, repeated calls without
array shifting, and 2,000 ordinary deliveries without conversation-history reads.
A real child-process fixture verifies that ingestion can remain stalled while the hook returns and exports only the
bridge envelope. Go tests cover adapter validation/privacy, policy inputs, managed root changes, foreign/symbolic
entries, corrupted receipts, and cleanup conflicts.

The canonical POSIX bootstrap was also run against locally built release assets: initial install, update while
changing the OpenCode root, and purge uninstall removing both recorded plugin files. Go vet, build, the workflow's
Go linter, JavaScript static checks, Markdown lint, and bootstrap transport tests passed locally. Windows
PowerShell 5.1/7 and platform-specific runtime checks belong to CI and are not established by this macOS run.

## Accepted limits

MCP classification uses registry identity and can misclassify execution replaced by another plugin. Ambiguous server
mappings are omitted. Nested calls count separately and omit individual duration; direct-call duration is also omitted
in this implementation. Events missed while observation is disconnected are lost without backfill. The outbox still
retries already accepted events.

The tested OpenChamber picker flow for a display name containing spaces sent ordinary text without a structured
attachment; that text was not counted. OpenCode 1.x, downgrade scenarios, and OpenChamber desktop/mobile/VS Code
clients were outside this verification. OpenChamber source and installed package files were unchanged.
