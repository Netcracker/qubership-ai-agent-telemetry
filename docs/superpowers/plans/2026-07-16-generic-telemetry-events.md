# Generic telemetry events implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add typed skill, command, and MCP telemetry events while preserving
legacy outbox data and the existing selftest probe.

**Architecture:** A discriminated `TelemetryEvent` owns validation and strict
JSON encoding. Harness adapters produce typed events, repository policy runs
before enqueue, and one OTLP mapper exports the allowed attributes. The
CLI-managed hooks and retained APM package install the same lifecycle events.

**Tech Stack:** Go, `encoding/json`, OpenTelemetry Logs OTLP/HTTP, native Claude
Code/Codex/Cursor JSON hooks, Markdown, and GitHub Actions.

## Global constraints

- Start from a branch that contains merged skill-detection hotfix PR #17
  (`64384f7`).
- Preserve `skill_executed` OTLP body and attributes for existing skill events.
- Write only schema version `1`; read both version `1` and the legacy skill
  shape without rewriting buffered files.
- Serialize `ts` as UTC RFC 3339 with Go `time.RFC3339Nano` behavior.
- Reject unknown JSON fields, explicit `null`, invalid enums, mismatched
  payloads, and invalid identifiers.
- Validate input exactly. Do not trim, truncate, replace characters, or change
  case.
- Permit identifiers only under the length and ASCII profiles in the approved
  design.
- Permit `agent=selftest` only with `skill_name=__selftest__`, a UUID session,
  and no repository value.
- Never serialize prompts, command arguments, tool inputs or results, errors,
  paths, URLs, IDs outside the allowlist, models, or user email.
- Keep hook execution fail-open: `ingest` reports local errors but exits `0`.
- Do not add token, usage, cost, ordinary-tool, or inferred-result telemetry.
- Do not add a CLI command, CLI flag, dependency, or CI workflow.
- Preserve Linux, macOS, and Windows behavior.

---

### Task 1: Confirm the post-hotfix baseline

**Files:**

- Verify: `detect.go`
- Verify: `transcript_codex.go`
- Verify: `transcript_cursor.go`
- Verify: `docs/agent-integration.md`

**Interfaces:**

- Consumes: merged PR #17 at `64384f7`.
- Produces: a clean feature branch whose skill detection includes nested skill
  paths and ignores non-command Codex payloads.

- [ ] **Step 1: Verify the branch contains the merged hotfix**

```bash
git merge-base --is-ancestor 64384f7 HEAD
```

Expected: exit code `0`. Stop if it returns a nonzero code; rebase the branch
onto an updated `origin/main` before changing feature code.

- [ ] **Step 2: Verify the required hotfix behavior exists**

```bash
rg -n 'validDetectedSkillName|codexToolTexts|custom_tool_call' \
  detect.go transcript_codex.go
```

Expected: every identifier is found. Stop if any is absent; the prerequisite
has not merged.

- [ ] **Step 3: Run the baseline tests**

```bash
go test ./...
```

Expected: PASS before feature code changes.

### Task 2: Add the typed event model and identifier validation

**Files:**

- Create: `event.go`
- Create: `event_test.go`

**Interfaces:**

- Consumes: repository identity selected later by policy.
- Produces: `TelemetryEvent`, typed payloads, constructors, event validation,
  and persistence-boundary validation.

- [ ] **Step 1: Write failing identifier and payload tests**

Create table-driven tests for accepted boundaries and rejected input:

```go
func TestTelemetryIdentifiers(t *testing.T) {
    tests := []struct {
        name, value string
        profile     identifierProfile
        want        bool
    }{
        {"session minimum", "s", sessionIdentifier, true},
        {"session maximum", strings.Repeat("a", 128), sessionIdentifier, true},
        {"session oversized", strings.Repeat("a", 129), sessionIdentifier, false},
        {"namespaced skill", "plugin-name:skill-name", nameIdentifier, true},
        {"control character", "skill\nname", nameIdentifier, false},
        {"unicode", "skіll", nameIdentifier, false},
        {"path", "skills/demo", nameIdentifier, false},
        {"shell", "demo$(id)", nameIdentifier, false},
        {"MCP punctuation", "github.get-issue_v2", mcpIdentifier, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := validIdentifier(tt.value, tt.profile); got != tt.want {
                t.Fatalf("validIdentifier(%q) = %v, want %v", tt.value, got, tt.want)
            }
        })
    }
}
```

Also test invalid event names, agents, outcomes, expansion types, payload/event
name mismatches, negative durations, and invalid optional MCP server names.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./... -run 'TestTelemetry(Identifiers|EventValidation)' -count=1
```

Expected: FAIL because the event types and validators do not exist.

- [ ] **Step 3: Implement the event types and exact interfaces**

Use a sealed payload interface so the in-memory model is discriminated:

```go
type EventName string

const (
    eventSkillExecuted EventName = "skill_executed"
    eventCommandInvoked EventName = "command_invoked"
    eventMCPExecuted    EventName = "mcp_tool_executed"
    eventSchemaVersion           = 1
)

type MCPOutcome string

const (
    mcpSucceeded MCPOutcome = "succeeded"
    mcpFailed    MCPOutcome = "failed"
    mcpUnknown   MCPOutcome = "unknown"
)

type telemetryPayload interface {
    eventName() EventName
}

type SkillPayload struct {
    SkillName string `json:"skill_name"`
}

type CommandPayload struct {
    CommandName   string `json:"command_name"`
    CommandSource string `json:"command_source"`
    ExpansionType string `json:"expansion_type"`
}

type MCPPayload struct {
    ServerName string     `json:"server_name,omitempty"`
    ToolName   string     `json:"tool_name"`
    Outcome    MCPOutcome `json:"outcome"`
    DurationMS *int64     `json:"duration_ms,omitempty"`
}

type TelemetryEvent struct {
    SchemaVersion int
    EventName     EventName
    Agent         string
    SessionID     string
    RepoRemote    string
    RepoDir       string
    TS            time.Time
    Payload       telemetryPayload
}
```

Define `identifierProfile` values for session (128), skill/command (255),
command source (64), and MCP server/tool (128):

```go
type identifierProfile struct {
    max        int
    allowColon bool
    firstAlnum bool
}

var (
    sessionIdentifier = identifierProfile{
        max: 128, allowColon: true, firstAlnum: true,
    }
    nameIdentifier = identifierProfile{
        max: 255, allowColon: true, firstAlnum: true,
    }
    sourceIdentifier  = identifierProfile{max: 64}
    mcpIdentifier     = identifierProfile{max: 128}
)
```

`validIdentifier` accepts only the ASCII bytes allowed by the approved regexes.
Constructors have these exact signatures:

```go
func newSkillEvent(
    agent, sessionID, repoRemote, repoDir, skillName string,
    ts time.Time,
) (TelemetryEvent, error)

func newCommandEvent(
    agent, sessionID, repoRemote, repoDir string,
    payload CommandPayload, ts time.Time,
) (TelemetryEvent, error)

func newMCPEvent(
    agent, sessionID, repoRemote, repoDir string,
    payload MCPPayload, ts time.Time,
) (TelemetryEvent, error)

func newSelftestProbe(ts time.Time) TelemetryEvent
func validateTelemetryEvent(ev TelemetryEvent) error
func validateSerializableEvent(ev TelemetryEvent) error
```

Ordinary constructors accept only `claude`, `codex`, and `cursor`.
`newSelftestProbe` uses `newUUID()` and reserved constants. The validator
accepts that exception only as the exact pair from the design. Event validation
allows an adapter's temporary raw remote before policy. Serializable validation
allows an empty `RepoRemote` for an explicitly unscoped repository policy. When
the value is present, it must already be a normalized canonical identity, so
raw URLs, credentials, and unnormalized identities cannot enter JSON.

- [ ] **Step 4: Run the focused tests**

```bash
gofmt -w event.go event_test.go
go test ./... -run 'TestTelemetry(Identifiers|EventValidation)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the model**

```bash
git add event.go event_test.go
git commit -m "feat: add typed telemetry event model"
```

### Task 3: Add strict versioned JSON and legacy decoding

**Files:**

- Modify: `event.go`
- Modify: `event_test.go`
- Create: `testdata/events/skill-v1.json`
- Create: `testdata/events/command-v1.json`
- Create: `testdata/events/mcp-v1.json`
- Create: `testdata/events/selftest-v1.json`
- Create: `testdata/events/skill-legacy.json`

**Interfaces:**

- Consumes: typed events and validation from Task 2.
- Produces: strict versioned JSON and legacy decoding for `Outbox.Read`.

- [ ] **Step 1: Add failing golden and rejection tests**

Use the exact examples from the design as golden files. Compare
`json.MarshalIndent` output after removing the fixture's trailing newline.
Test unknown fields at both levels, `null`, unknown version, partial
discriminators, wrong payload, invalid timestamp, trailing JSON, and invalid
legacy identifiers. Accept an absent repository remote, reject an unnormalized
persisted remote, and accept the legacy skill and exact legacy selftest pair.
Build the selftest golden with the fixed UUID from the design rather than the
random selftest constructor.

- [ ] **Step 2: Run the codec tests to verify they fail**

```bash
go test ./... -run 'TestTelemetryEvent(CanonicalJSON|Rejects|Legacy)' -count=1
```

Expected: FAIL because custom JSON is not implemented.

- [ ] **Step 3: Implement strict encoding and decoding**

Marshal through one direct payload field:

```go
type eventEnvelope struct {
    SchemaVersion int              `json:"schema_version"`
    EventName     EventName        `json:"event_name"`
    Agent         string           `json:"agent"`
    SessionID     string           `json:"session_id"`
    RepoRemote    string           `json:"repo_remote,omitempty"`
    TS            time.Time        `json:"ts"`
    Payload       telemetryPayload `json:"payload"`
}
```

`MarshalJSON` calls `validateSerializableEvent`, forces `TS.UTC()`, and excludes
`RepoDir`.
`UnmarshalJSON` inspects the discriminator, rejects a partially versioned
object, decodes with `DisallowUnknownFields`, verifies EOF, selects the concrete
payload by `event_name`, and validates. Use these helpers for both envelope and
payload objects:

```go
func decodeStrictJSON(data []byte, dst any) error
func rejectExplicitNulls(data []byte) error
```

`decodeStrictJSON` uses `json.Decoder.DisallowUnknownFields()` and requires EOF
after the first value. `rejectExplicitNulls` decodes a
`map[string]json.RawMessage` and rejects every present field whose trimmed raw
value is `null`. Apply it separately to the envelope and direct payload before
typed decoding. This rejects `repo_remote: null`, optional MCP fields set to
`null`, and required fields set to `null` instead of silently treating them as
absent. A strict legacy struct maps to `skill_executed`; its service exception
must be exact.

- [ ] **Step 4: Run the codec tests**

```bash
gofmt -w event.go event_test.go
go test ./... -run 'TestTelemetryEvent(CanonicalJSON|Rejects|Legacy)' -count=1
```

Expected: PASS with all five fixture shapes parsed as valid JSON.

- [ ] **Step 5: Commit the codec**

```bash
git add event.go event_test.go testdata/events
git commit -m "feat: add versioned telemetry event JSON"
```

### Task 4: Migrate the existing skill pipeline and selftest

**Files:**

- Modify: `outbox.go`
- Modify: `outbox_test.go`
- Modify: `detect.go`
- Modify: `detect_test.go`
- Modify: `transcript_codex.go`
- Modify: `transcript_codex_test.go`
- Modify: `transcript_cursor.go`
- Modify: `transcript_cursor_test.go`
- Modify: `policy.go`
- Modify: `policy_test.go`
- Modify: `flush.go`
- Modify: `flush_test.go`
- Modify: `commands.go`
- Modify: `commands_test.go`
- Modify: `main.go`
- Modify: `main_test.go`

**Interfaces:**

- Consumes: `TelemetryEvent` constructors and codec.
- Produces: the existing skill-only flow on the typed model before enabling new
  harness events.

- [ ] **Step 1: Add failing migration tests**

Add these named cases:

```text
TestOutboxWritesOnlyVersionOne
TestOutboxReadsMixedLegacyAndVersionOneInOrder
TestOutboxKeepsInvalidVersionedEntry
TestPolicyAppliesToEveryHarnessEvent
TestFlushPreservesSkillOTLPSchema
TestFlushMapsAllTypedPayloads
TestSelftestDeliversVersionOneProbeAndClearsIt
TestSelftestFindsLegacyAndVersionOneProbes
TestSelftestRejectsModifiedReservedPairs
```

Decode the OTLP protobuf request and compare body and attributes. Use this
mapping matrix:

```go
var wantAttrs = map[EventName]map[string]any{
    eventSkillExecuted: {"skill.name": "brainstorming"},
    eventCommandInvoked: {
        "command.name": "review-pr",
        "command.source": "plugin",
        "command.expansion_type": "slash_command",
    },
    eventMCPExecuted: {
        "mcp.server.name": "github",
        "mcp.tool.name": "get_issue",
        "mcp.outcome": "succeeded",
        "mcp.duration_ms": int64(42),
    },
}
```

- [ ] **Step 2: Run the migration tests to verify they fail**

```bash
go test ./... -run \
  'Test(Outbox|Policy|Flush|Selftest|Detect|Codex|Cursor)' -count=1
```

Expected: FAIL because storage and producers still use `SkillEvent`.

- [ ] **Step 3: Replace `SkillEvent` throughout the pipeline**

Change exact signatures:

```go
func (s *Outbox) Enqueue(ev TelemetryEvent) error
func (s *Outbox) Read(name string) (TelemetryEvent, error)

func detect(
    agent string, stdin []byte, remote remoteResolver, now time.Time,
) ([]TelemetryEvent, error)

func filterEventsByPolicy(
    events []TelemetryEvent, policy telemetryPolicy,
    remotes func(string) []string,
) []TelemetryEvent
```

Use `newSkillEvent` in all skill producers. Invalid detected names produce no
event. Keep transcript offsets unchanged. `Outbox.Enqueue` validates before
writing; `Outbox.Read` uses the strict decoder. Invalid files remain buffered
and are skipped by `Flush`.

- [ ] **Step 4: Add the generic OTLP mapper**

Keep transport and deletion behavior unchanged. Extract:

```go
func eventRecord(
    ev TelemetryEvent, observed time.Time,
) (otellog.Record, error)
```

Set the body from `EventName`; always add `agent`, `session.id`, and
`repo.remote`, including the existing empty value; then type-switch on the
payload. Omit unavailable optional MCP attributes. A mapping error leaves that
file buffered.

- [ ] **Step 5: Preserve selftest through its dedicated constructor**

```go
probe := newSelftestProbe(time.Now().UTC())
if err := s.Enqueue(probe); err != nil {
    return selftestResult{}, err
}
```

Make `probesRemaining` require both reserved values after type-asserting a
skill payload. It must recognize the same values after legacy decoding.

- [ ] **Step 6: Run the migrated skill pipeline**

```bash
gofmt -w outbox.go outbox_test.go detect.go detect_test.go \
  transcript_codex.go transcript_codex_test.go \
  transcript_cursor.go transcript_cursor_test.go policy.go policy_test.go \
  flush.go flush_test.go commands.go commands_test.go main.go main_test.go
go test ./... -count=1
```

Expected: PASS. Do not delete or weaken existing skill assertions.

- [ ] **Step 7: Commit the migration**

```bash
git add outbox.go outbox_test.go detect.go detect_test.go \
  transcript_codex.go transcript_codex_test.go \
  transcript_cursor.go transcript_cursor_test.go policy.go policy_test.go \
  flush.go flush_test.go commands.go commands_test.go main.go main_test.go
git commit -m "refactor: migrate telemetry pipeline to typed events"
```

### Task 5: Add Claude command and MCP adapters

**Files:**

- Modify: `detect.go`
- Modify: `detect_test.go`

**Interfaces:**

- Consumes: `newCommandEvent`, `newMCPEvent`, and centralized validation.
- Produces: Claude routing for `PreToolUse`, `UserPromptExpansion`,
  `PostToolUse`, and `PostToolUseFailure`.

- [ ] **Step 1: Write failing Claude fixtures and privacy assertions**

Use successful command and MCP fixtures that include forbidden fields:

```json
{"hook_event_name":"UserPromptExpansion","session_id":"s1","cwd":"/repo","command_name":"review-pr","command_source":"plugin","expansion_type":"slash_command","command_args":"secret","prompt":"private"}
```

```json
{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/repo","tool_name":"mcp__github__get_issue","duration_ms":42,"tool_input":{"token":"secret"},"tool_response":{"email":"person@example.com"}}
```

Repeat MCP with `PostToolUseFailure`, error content, and outcome `failed`.
Assert normalized events contain only allowed values. Reject built-in tools,
malformed MCP names, unsupported expansion types, and invalid identifiers.
Assert a negative duration is omitted while the rest of the MCP event remains.

- [ ] **Step 2: Run the Claude adapter tests to verify they fail**

```bash
go test ./... -run 'TestDetectClaude(Command|MCP|Privacy|Rejects)' -count=1
```

Expected: FAIL because only the skill payload is supported.

- [ ] **Step 3: Route Claude by `hook_event_name`**

Decode only allowlisted fields into event-specific structs. A legacy
`PreToolUse` payload without `hook_event_name` must still route by
`tool_name == "Skill"`. Implement:

```go
func claudeAdapter(
    stdin []byte, remote remoteResolver, now time.Time,
) ([]TelemetryEvent, error)

func normalizeMCPToolName(name string) (server, tool string, ok bool)
```

The MCP normalizer requires `mcp__<server>__<tool>`, splits only the separator
after the server, and validates both pieces. Do not retain the composite name.

- [ ] **Step 4: Run the Claude adapter tests**

```bash
gofmt -w detect.go detect_test.go
go test ./... -run 'TestDetectClaude' -count=1
```

Expected: PASS, including existing skill and BOM cases.

- [ ] **Step 5: Commit the Claude adapters**

```bash
git add detect.go detect_test.go
git commit -m "feat: collect Claude command and MCP events"
```

### Task 6: Add Codex and Cursor MCP adapters

**Files:**

- Modify: `detect.go`
- Modify: `detect_test.go`

**Interfaces:**

- Consumes: the MCP normalizer from Task 5.
- Produces: native MCP completion handling without changing transcript skill
  detection.

- [ ] **Step 1: Add failing Codex and Cursor tests**

Use payloads with forbidden content:

```json
{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/repo","tool_name":"mcp__github__get_issue","tool_response":{"token":"secret"}}
```

```json
{"hook_event_name":"afterMCPExecution","session_id":"s1","workspace_roots":["/repo"],"tool_name":"get_issue","duration":42,"result_json":{"email":"person@example.com"}}
```

Assert Codex emits `outcome=unknown` with server `github`. Cursor emits
`outcome=unknown` without a server. Reject invalid names, omit negative
duration, and retain existing `Stop` and `afterAgentResponse` routing.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./... -run 'TestDetect(CodexMCP|CursorMCP|RoutesExistingSkill)' -count=1
```

Expected: FAIL because both agents route only to transcript readers.

- [ ] **Step 3: Implement native-event routing**

```go
func codexAdapter(stdin []byte, now time.Time) ([]TelemetryEvent, error)

func cursorAdapter(
    stdin []byte, remote remoteResolver, now time.Time,
) ([]TelemetryEvent, error)
```

Inspect `hook_event_name` before opening a transcript. Codex never reads
`tool_response`. Cursor validates direct `tool_name`, leaves `ServerName`
empty, converts non-negative `duration` to `*int64`, and never reads
`result_json`. Other events call existing transcript functions.

- [ ] **Step 4: Run all adapter and transcript tests**

```bash
gofmt -w detect.go detect_test.go
go test ./... -run 'Test(Detect|Codex|Cursor)' -count=1
```

Expected: PASS without new transcript inference.

- [ ] **Step 5: Commit the adapters**

```bash
git add detect.go detect_test.go
git commit -m "feat: collect Codex and Cursor MCP events"
```

### Task 7: Install the complete native hook set

**Files:**

- Modify: `hooks.go`
- Modify: `hooks_claude.go`
- Modify: `hooks_claude_test.go`
- Modify: `hooks_codex.go`
- Modify: `hooks_codex_test.go`
- Modify: `hooks_cursor.go`
- Modify: `hooks_cursor_test.go`
- Modify: `hooks_package_test.go`
- Modify: `agent-packages/ai-agent-telemetry/.apm/hooks/skill-call-claude-hooks.json`
- Modify: `agent-packages/ai-agent-telemetry/.apm/hooks/skill-call-codex-hooks.json`
- Modify: `agent-packages/ai-agent-telemetry/.apm/hooks/skill-call-cursor-hooks.json`

**Interfaces:**

- Consumes: unchanged `ai-agent-telemetry ingest --agent=<harness>` commands.
- Produces: idempotent global and APM-package registrations with equal event
  coverage.

- [ ] **Step 1: Add failing merge, status, and parity tests**

Require these managed events:

```go
var claudeHookSpecs = []hookSpec{
    {event: "PreToolUse", matcher: "Skill"},
    {event: "UserPromptExpansion"},
    {event: "PostToolUse", matcher: "mcp__.*"},
    {event: "PostToolUseFailure", matcher: "mcp__.*"},
}

var codexHookSpecs = []hookSpec{
    {event: "Stop"},
    {event: "PostToolUse", matcher: "mcp__.*"},
}

var cursorHookEvents = []string{
    "afterAgentResponse", "afterMCPExecution",
}
```

For each harness, test empty installation, unrelated handler preservation,
legacy/APM replacement, duplicate removal, incompatible managed structures,
complete status, and second-install idempotence. Expand package parity to
compare every event, matcher, and command.

- [ ] **Step 2: Run hook tests to verify they fail**

```bash
go test ./... -run 'Test.*(Claude|Codex|Cursor|HookPackage|HookStatus)' -count=1
```

Expected: FAIL because only original skill hooks are managed.

- [ ] **Step 3: Generalize merges around managed event specifications**

Keep harness-native JSON shapes. Claude and Codex merge one canonical command
handler into the matching group for each managed event. Cursor merges one
canonical entry into each managed event array.

Define the shared `hookSpec` in `hooks.go`. Only remove handlers with the
canonical command, a known legacy command, or
`_apm_source: ai-agent-telemetry`. Preserve unrelated keys, groups, handlers,
and extension fields. Status requires the complete managed set. Use
`Recording agent telemetry` for Claude/Codex status messages; keep command
strings exact so the Codex policy remains valid.

- [ ] **Step 4: Update retained APM hook JSON and parity**

Represent the same event/matcher sets from Step 1. Do not add scripts or a new
package. The Go parity test must parse every JSON file and compare the complete
managed configuration.

- [ ] **Step 5: Run focused hook tests**

```bash
gofmt -w hooks.go hooks_claude.go hooks_claude_test.go hooks_codex.go \
  hooks_codex_test.go hooks_cursor.go hooks_cursor_test.go \
  hooks_package_test.go
go test ./... -run '^Test.*(Hook|Configure($|[A-Z]))' -count=1
```

Expected: PASS on Linux. CI repeats this expression on macOS and Windows.

- [ ] **Step 6: Commit hook coverage**

```bash
git add hooks.go hooks_claude.go hooks_claude_test.go hooks_codex.go \
  hooks_codex_test.go hooks_cursor.go hooks_cursor_test.go \
  hooks_package_test.go agent-packages/ai-agent-telemetry/.apm/hooks
git commit -m "feat: install command and MCP telemetry hooks"
```

### Task 8: Add end-to-end privacy regression coverage

**Files:**

- Create: `privacy_test.go`
- Modify: `flush_test.go`
- Modify: `main_test.go`

**Interfaces:**

- Consumes: all adapters, policy, outbox codec, and OTLP mapper.
- Produces: a negative privacy matrix proving excluded input does not leave the
  process.

- [ ] **Step 1: Write the end-to-end privacy matrix**

For each harness/event, seed payloads with unique values for prompt, arguments,
tool input, result, error, local path, URL, call/turn ID, model, and email. Run
detect, policy, enqueue, outbox read, and OTLP flush. Scan both serialized
forms:

```go
var forbiddenSentinels = []string{
    "PROMPT_SECRET_7f1",
    "ARG_SECRET_7f2",
    "INPUT_SECRET_7f3",
    "RESULT_SECRET_7f4",
    "ERROR_SECRET_7f5",
    "person@example.com",
    "/home/private-user/project",
    "https://mcp.internal/token",
}
```

Add invalid identifier cases for empty, maximum+1, Unicode, whitespace,
newline, tab, slash, backslash, shell metacharacters, and malformed optional
server. Invalid events must create neither an outbox file nor collector
request. `detect("selftest", ...)` must fail as unsupported while
`newSelftestProbe` succeeds.

- [ ] **Step 2: Run the completed end-to-end privacy matrix**

```bash
go test ./... -run 'TestPrivacy|TestReservedSelftest' -count=1
```

Expected: PASS only when every sentinel is absent. If a case fails, return to
the task that owns the leaking boundary and add the failing case to that task's
focused tests. Do not weaken or remove a sentinel.

- [ ] **Step 3: Run all event-pipeline tests**

```bash
gofmt -w privacy_test.go flush_test.go main_test.go
go test ./... -run \
  'Test(Telemetry|Detect|Outbox|Flush|Policy|Selftest|Privacy|Reserved)' \
  -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit privacy coverage**

```bash
git add privacy_test.go flush_test.go main_test.go
git commit -m "test: protect telemetry event privacy boundary"
```

### Task 9: Document the expanded event and privacy contract

**Files:**

- Modify: `README.md`
- Modify: `docs/agent-integration.md`
- Modify: `docs/cli.md`
- Create: `docs/adr/0006-generic-event-schema-and-privacy.md`
- Modify: `agent-packages/ai-agent-telemetry/README.md`

**Interfaces:**

- Consumes: final event names, attributes, hook matrix, and selftest exception.
- Produces: user and architecture documentation that matches implementation.

- [ ] **Step 1: Update the root Data section**

Document exact event-specific fields:

```text
skill_executed: skill.name
command_invoked: command.name, command.source, command.expansion_type
mcp_tool_executed: mcp.tool.name, mcp.outcome,
                   optional mcp.server.name and mcp.duration_ms
```

List identifier limits and excluded content. Keep installation concise. Add
only the required Codex restart/trust note after hook refresh.

- [ ] **Step 2: Update integration and CLI reference docs**

In `docs/agent-integration.md`, replace the single-signal table with the exact
harness capability matrix. In `docs/cli.md`, document routing by
`hook_event_name`, version `1` outbox JSON, legacy reads, fail-open invalid
events, and reserved selftest behavior.

- [ ] **Step 3: Record ADR 0006**

Use `Status`, `Context`, `Decision`, and `Consequences`. Record:

- the typed versioned envelope and direct payload;
- strict external identifier profiles;
- the exact internal selftest exception;
- repository-policy behavior;
- content fields that remain excluded;
- backend responsibility for distinct valid-identifier cardinality;
- token attribution as a separate decision.

Reference ADR 0004 rather than changing its accepted historical decision.

- [ ] **Step 4: Update the retained APM package README**

List new hook events in its compatibility matrix. Keep the package framed as
legacy compatibility and keep Codex-specific text to the required restart and
trust instruction.

- [ ] **Step 5: Lint changed Markdown**

```bash
npx --yes markdownlint-cli2 README.md docs/agent-integration.md docs/cli.md \
  docs/adr/0006-generic-event-schema-and-privacy.md \
  agent-packages/ai-agent-telemetry/README.md
```

Expected: `Summary: 0 error(s)`.

- [ ] **Step 6: Commit documentation**

```bash
git add README.md docs/agent-integration.md docs/cli.md \
  docs/adr/0006-generic-event-schema-and-privacy.md \
  agent-packages/ai-agent-telemetry/README.md
git commit -m "docs: describe generic telemetry events"
```

### Task 10: Run final local and cross-platform verification

**Files:**

- Verify only: `.github/workflows/go-build.yaml`
- Verify only: all changed Go, JSON, and Markdown files.

**Interfaces:**

- Consumes: complete implementation.
- Produces: review-ready evidence without changing CI workflow behavior.

- [ ] **Step 1: Check formatting and patch integrity**

```bash
test -z "$(gofmt -l .)"
git diff --check
```

Expected: both commands exit `0` with no output.

- [ ] **Step 2: Run static analysis, build, and all tests**

```bash
go vet ./...
go build ./...
go test ./... -count=1
```

Expected: all commands exit `0`.

- [ ] **Step 3: Run the cross-platform hook subset locally**

```bash
go test ./... -run '^Test.*(Hook|Configure($|[A-Z]))' -count=1
```

Expected: PASS. CI repeats it on `macos-latest` and `windows-latest`; Ubuntu
already runs the full suite.

- [ ] **Step 4: Validate JSON and Markdown assets**

```bash
go test ./... \
  -run 'TestLegacyHookPackageParity|TestTelemetryEventCanonicalJSON' \
  -count=1
npx --yes markdownlint-cli2 README.md docs \
  agent-packages/ai-agent-telemetry/README.md
```

Expected: Go tests pass and Markdown reports `0 error(s)`.

- [ ] **Step 5: Run a separate code review**

Request one independent subagent review after all commands pass. Give it the
approved design, this plan, and `git diff origin/main...HEAD`. Resolve findings
with test-first fixes and repeat Steps 1–4.

- [ ] **Step 6: Push and create the feature PR**

```bash
git push -u origin feat/telemetry-events
body=$'## Why\n\nSkill-only telemetry misses command and MCP usage.'
body+=$'\n\n## What\n\n- add typed, versioned telemetry events'
body+=$'\n- collect documented command and MCP hooks'
body+=$'\n- enforce identifier and content privacy boundaries'
body+=$'\n- preserve legacy outbox and selftest behavior'
body+=$'\n\n## How to verify\n\nRun `go test ./... -count=1`.'
gh pr create \
  --title "feat: collect generic agent telemetry events" \
  --body "$body"
```

Expected: the PR description is concise and identifies the compatibility and
privacy changes without requiring the reviewer to inspect the diff.

- [ ] **Step 7: Verify GitHub Actions and record release handoff**

```bash
gh pr checks --watch
```

Expected: Ubuntu build/test and both macOS/Windows global-hook jobs are green.
Do not modify `.github/workflows/go-build.yaml` unless a reproducible
platform-only failure requires it. After merge, release `v1.2.0` because the
change adds event types without changing skill records.
