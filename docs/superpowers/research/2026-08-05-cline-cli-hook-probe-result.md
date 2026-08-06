# Cline CLI hook probe result

- Date: August 5, 2026
- Client: Cline CLI 3.0.50
- Working directory: `qubership-ai-agent-telemetry`
- Outcome: `PASS`
- Raw capture: `/Users/denifilatov/.cache/ai-agent-telemetry/research/cline-cli/20260805T150233Z`

## Question

The probe tested whether Cline CLI needs a hook separate from the Cline VS Code Extension hook. It also recorded the
CLI payload shape needed by a future `ai-agent-telemetry` adapter.

## Setup

The experiment installed one temporary executable hook at:

```text
~/Documents/Cline/Hooks/PostToolUse
```

No hook existed at `~/.cline/hooks/PostToolUse`. The project exposed one inert skill at
`.cline/skills/cline-hook-probe/SKILL.md`. The task asked Cline CLI to invoke that skill and return the sentinel
`CLINE_CLI_HOOK_PROBE_OK` without using another tool.

The hook retained complete stdin payloads in the private raw-capture directory. It did not invoke
`ai-agent-telemetry`, send OTLP data, or alter the payload. The capture script used a macOS-compatible `mktemp`
template that ends in `X`, then appended `.json` after allocation.

## Result

Cline CLI discovered one global hook and no workspace hook. Its own diagnostic event reported `globalCount=1`,
`workspaceCount=0`, and `totalCount=1` for the internal `tool_result` hook event. This proves that Cline CLI searched
`~/Documents/Cline/Hooks` even though its command-line help names `~/.cline/hooks` as the default additional hooks
directory.

The successful task made one tool call:

- Tool name: `skills`
- Skill parameter key: `skill`
- Optional argument key: `args`
- Visible response: `CLINE_CLI_HOOK_PROBE_OK`
- Task completion: successful

The capture directory contained one event. Exactly one event matched all probe predicates:

- `hookName` was `tool_result`.
- `postToolUse.toolName` was `skills`.
- Serialized `postToolUse.parameters` contained `cline-hook-probe`.
- `postToolUse.success` was `true`.

The first sandboxed attempt stopped before tool execution because Cline could not write its SQLite session database.
It still completed hook discovery and found the same single global hook. Repeating the identical task with write access
to Cline's local state completed successfully. The SQLite failure was an execution-environment restriction, not a hook
failure.

## Observed schema

The top-level fields in the captured CLI payload were:

| Field | JSON type |
| --- | --- |
| `agent_id` | string |
| `clineVersion` | string |
| `hookName` | string |
| `iteration` | number |
| `parent_agent_id` | null |
| `postToolUse` | object |
| `sessionContext` | object |
| `taskId` | string |
| `timestamp` | string |
| `tool_result` | object |
| `userId` | string |
| `workspaceInfo` | object |
| `workspaceRoots` | array |

The compatibility `postToolUse` object contained:

| Field | JSON type |
| --- | --- |
| `executionTimeMs` | number |
| `parameters` | object |
| `result` | string |
| `success` | boolean |
| `toolName` | string |

The observed `postToolUse.parameters` object contained:

| Field | JSON type |
| --- | --- |
| `args` | null |
| `skill` | string |

The top-level `tool_result` object duplicated richer tool execution data:

| Field | JSON type |
| --- | --- |
| `durationMs` | number |
| `endedAt` | string |
| `id` | string |
| `input` | object |
| `name` | string |
| `output` | string |
| `startedAt` | string |

Its `input` object contained `args` as null and `skill` as a string. The `sessionContext` object contained a
`rootSessionId` string.

The `workspaceInfo` object contained fields that must remain local:

| Field | JSON type |
| --- | --- |
| `associatedRemoteUrls` | array |
| `hint` | string |
| `latestGitBranchName` | string |
| `latestGitCommitHash` | string |
| `rootPath` | string |

The event had one workspace root and one associated remote URL. Their values were not copied into this document.

## Comparison with the VS Code Extension

The two clients can share one global hook file, but their successful skill events have different discriminators and
parameter names:

| Client | `hookName` | Tool name | Skill parameter |
| --- | --- | --- | --- |
| VS Code Extension 4.1.3 | `PostToolUse` | `use_skill` | `skill_name` |
| CLI 3.0.50 | `tool_result` | `skills` | `skill` |

A Cline detector must normalize both shapes. Restricting detection to `hookName == "PostToolUse"` would drop the CLI
event. Restricting the tool name to `use_skill` or the parameter to `skill_name` would have the same effect.

## Installer implication

The installer should not create identical managed hooks in both global directories. Cline CLI reads the existing
`~/Documents/Cline/Hooks/PostToolUse` hook. Adding `~/.cline/hooks/PostToolUse` would create a duplicate-handler risk.
The initial implementation can manage the Documents hook for the VS Code Extension and leave the CLI directory
untouched.

CLI ingestion is still a separate support decision. Sharing the hook file does not automatically make the CLI a
supported telemetry surface. First-class CLI support needs its observed envelope added to the detector and tests.

The adapter should read only the bounded fields required for the telemetry event. It must not forward `userId`, tool
output, workspace paths, raw remote URLs, branch names, commit hashes, agent identifiers, or arbitrary arguments.

## Cleanup

- The hook matched its installation bytes before removal.
- `~/Documents/Cline/Hooks/PostToolUse` was removed.
- `~/.cline/hooks/PostToolUse` remained absent throughout the experiment.
- The skill matched its recorded SHA-256 digest before removal.
- `.cline/skills/cline-hook-probe` was removed.
- The raw capture directory remains at mode `0700`.
- The retained event remains at mode `0600`.
- No incomplete `.tmp` file remains.
- No active experiment artifact remains in the worktree.

The complete raw payload remains local for implementation and fixture design. It must not be committed or sent to the
telemetry backend.
