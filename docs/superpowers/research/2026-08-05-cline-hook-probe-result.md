# Cline `PostToolUse` Hook Probe Result

- Date: August 5, 2026
- Extension: `saoudrizwan.claude-dev` 4.1.3
- Outcome: `PASS`
- Raw capture: `/Users/denifilatov/.cache/ai-agent-telemetry/research/cline/20260805T121118Z`

## Result

Cline invoked the project skill through its native `use_skill` tool. The global `PostToolUse` hook received the event,
returned `{"cancel":false}`, and did not interrupt the task. Cline displayed `CLINE_HOOK_PROBE_OK` and completed without
a visible hook error.

One retained event matched every probe predicate:

- `hookName` was `PostToolUse`.
- `postToolUse.toolName` was `use_skill`.
- Serialized `postToolUse.parameters` contained `cline-hook-probe`.
- `postToolUse.success` was `true`.
- The visible Cline response matched the sentinel.

No raw JSON object or private field value was copied into this document.

## Initial false negative

The first invocation did not discover `cline-hook-probe`. VS Code had no folder open, so Cline reported that it was
using the Desktop directory and listed only global skills from `~/.agents/skills`. The same list also omitted the
repository's existing `ai-agent-telemetry-configure` skill, confirming that parsing of the probe file was not the
failing boundary.

Opening `/Users/denifilatov/Repos/qubership-ai-agent-telemetry` as the VS Code workspace and trusting that folder
changed the discovery root. A new Cline task then loaded `cline-hook-probe` from
`.cline/skills/cline-hook-probe/SKILL.md`.

This establishes a precondition for future Cline tests and onboarding: repository-scoped skills are available only when
Cline receives the repository as a workspace root. With no workspace folder, Cline falls back to a different directory
and project skills are absent.

## Observed schema

The captured top-level fields and JSON types were:

| Field | JSON type |
| --- | --- |
| `clineVersion` | string |
| `hookName` | string |
| `model` | object |
| `postToolUse` | object |
| `taskId` | string |
| `timestamp` | string |
| `userId` | string |
| `workspaceRoots` | array |

The captured `postToolUse` fields and JSON types were:

| Field | JSON type |
| --- | --- |
| `executionTimeMs` | number |
| `parameters` | object |
| `result` | string |
| `success` | boolean |
| `toolName` | string |

The `parameters` object exposed these field names:

- `skill_name`
- `task_progress`

Their values were not recorded in this document, except for the boolean observation that the serialized parameters
contained the probe name.

## Capture-script finding

The probe used this macOS `mktemp` template:

```text
event.XXXXXXXX.tmp
```

On this machine, `/usr/bin/mktemp` did not replace the `X` characters because the template did not end with them. The
resulting file was named `event.XXXXXXXX.json`. A later invocation could reuse the temporary name and overwrite the
final JSON file during `mv`.

This did not invalidate the single-event proof: the capture directory was empty immediately before the Cline task, one
matching event was written, and no second `PostToolUse` event occurred. It does mean that this exact temporary-file
pattern must not be reused by the production adapter or a multi-event experiment.

A corrected macOS-compatible probe should create the unique file with a template ending in `X`, then rename that
resolved path to add the `.json` suffix. The corrected behavior needs its own multi-event test before production use.

## Cleanup

- The installed hook matched its recorded SHA-256 digest before removal.
- `~/Documents/Cline/Hooks/PostToolUse` was removed.
- The test skill matched its recorded SHA-256 digest before removal.
- `.cline/skills/cline-hook-probe` was removed.
- The raw capture directory remains at mode `0700`.
- The retained event remains at mode `0600`.
- No incomplete `.tmp` capture remains.
- No active experiment artifact remains in the repository worktree.

The raw payload remains local for the later event-mapping study. It must not be committed, pasted into chat, or sent to
the telemetry backend.
