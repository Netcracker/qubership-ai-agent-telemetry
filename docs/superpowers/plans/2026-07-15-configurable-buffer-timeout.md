# Configurable buffer capacity and flush timeout implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the outbox capacity and ordinary flush timeout configurable through environment variables and
`configure` flags while preserving the existing defaults.

**Architecture:** Add a typed delivery-settings resolver in `config.go` with process environment, saved env-file, and
default precedence. Parse and persist configure overrides through the existing CLI flow, then pass one resolved settings
value into ingest, flush, and status paths.

**Tech Stack:** Go 1.25, standard library parsing (`strconv`, `time`), the existing table-driven Go tests, and Markdown
CLI documentation.

## Global constraints

- Keep `100` as the default buffer capacity and `2s` as the default ordinary flush timeout.
- Accept only positive buffer capacities and positive Go durations.
- Use process environment values before saved env-file values, then use defaults.
- Warn and use the corresponding default for an invalid runtime value.
- Reject invalid `configure` values with exit code `2` before writing configuration or hooks.
- Leave the self-test timeout at `10s`.
- Show effective settings only in `status --verbose`.
- Do not add dependencies or a generic configuration registry.

---

### Task 1: Resolve typed delivery settings

**Files:**

- Modify: `config.go`
- Test: `config_test.go`

**Interfaces:**

- Produces: `deliverySettings{BufferCap int, FlushTimeout time.Duration}`.
- Produces: `resolveDeliverySettings() deliverySettings` for command entry points.
- Produces: `resolveDeliverySettingsFrom(processEnv, fileEnv map[string]string, warn func(string)) deliverySettings`
  for deterministic tests.
- Produces: `parseBufferCap(string) (int, error)` and `parseFlushTimeout(string) (time.Duration, error)` for CLI reuse.

- [ ] **Step 1: Write failing resolver tests**

Add table-driven tests that assert defaults, valid saved values, process-environment precedence, and invalid-value
fallback. Include a warning collector so tests verify that the warning names the invalid key, value, and selected
default.

```go
func TestResolveDeliverySettingsFromDefaults(t *testing.T) {
    got := resolveDeliverySettingsFrom(nil, nil, func(string) {})
    if got.BufferCap != 100 || got.FlushTimeout != 2*time.Second {
        t.Fatalf("settings = %+v, want buffer 100 and timeout 2s", got)
    }
}

func TestResolveDeliverySettingsFromProcessEnvWins(t *testing.T) {
    got := resolveDeliverySettingsFrom(
        map[string]string{envBufferCap: "1000", envFlushTimeout: "30s"},
        map[string]string{envBufferCap: "200", envFlushTimeout: "5s"},
        func(string) {},
    )
    if got.BufferCap != 1000 || got.FlushTimeout != 30*time.Second {
        t.Fatalf("settings = %+v", got)
    }
}
```

- [ ] **Step 2: Run the focused tests and confirm the red state**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test ./... -run 'Test(Parse|Resolve)DeliverySettings' -count=1
```

Expected: build failure because the settings types and resolvers do not exist.

- [ ] **Step 3: Implement constants, parsers, precedence, and warnings**

Add the following focused model to `config.go` and keep OS/environment access in `resolveDeliverySettings`:

```go
const (
    envBufferCap       = "AI_AGENT_TELEMETRY_BUFFER_CAP"
    envFlushTimeout    = "AI_AGENT_TELEMETRY_FLUSH_TIMEOUT"
    defaultBufferCap   = 100
    defaultFlushTimeout = 2 * time.Second
)

type deliverySettings struct {
    BufferCap    int
    FlushTimeout time.Duration
}
```

Use `strconv.Atoi` and `time.ParseDuration`, reject values less than or equal to zero, and emit messages in this form:

```text
invalid AI_AGENT_TELEMETRY_BUFFER_CAP value "zero": expected a positive integer; using default 100
```

- [ ] **Step 4: Run focused resolver tests and confirm the green state**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test ./... -run 'Test(Parse|Resolve)DeliverySettings' -count=1
```

Expected: `ok ai-agent-telemetry`.

- [ ] **Step 5: Commit the resolver**

```bash
git add config.go config_test.go
git commit -m "feat(config): resolve delivery settings"
```

### Task 2: Parse and persist configure flags

**Files:**

- Modify: `hooks.go`
- Modify: `main.go`
- Modify: `commands.go`
- Modify: `help.go`
- Test: `commands_test.go`
- Test: `main_test.go`

**Interfaces:**

- Consumes: `parseBufferCap` and `parseFlushTimeout` from Task 1.
- Produces: `deliverySettingOverrides{BufferCap string, FlushTimeout string}`.
- Produces: `configureOptions.Delivery deliverySettingOverrides`.
- Changes: `applyConfigure(..., delivery deliverySettingOverrides) error` persists only nonempty overrides.

- [ ] **Step 1: Write failing flag and persistence tests**

Cover both accepted forms, invalid/missing/zero/negative values, no writes on validation failure, and preservation of
existing keys when a flag is omitted.

```go
func TestParseConfigureFlagsAcceptsDeliverySettings(t *testing.T) {
    opts, err := parseConfigureFlags([]string{"--buffer-cap", "1000", "--flush-timeout=30s"})
    if err != nil {
        t.Fatal(err)
    }
    if opts.Delivery.BufferCap != "1000" || opts.Delivery.FlushTimeout != "30s" {
        t.Fatalf("delivery settings = %+v", opts.Delivery)
    }
}
```

- [ ] **Step 2: Run configure-focused tests and confirm the red state**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test ./... \
  -run 'Test(ParseConfigure|ApplyConfigure|RunConfigure)' -count=1
```

Expected: build or assertion failures for the new fields and flags.

- [ ] **Step 3: Implement CLI parsing and atomic persistence**

Add both new fields to `configureOptions`, validate values while parsing, and pass the overrides into `applyConfigure`.
Write only supplied values:

```go
if delivery.BufferCap != "" {
    updates[envBufferCap] = delivery.BufferCap
}
if delivery.FlushTimeout != "" {
    updates[envFlushTimeout] = delivery.FlushTimeout
}
```

Add both equals and space-separated forms to configure help. Keep validation before `pkgConfigDir`, hook installation,
or any file write so invalid input has no side effects.

- [ ] **Step 4: Run configure-focused tests and confirm the green state**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test ./... \
  -run 'Test(ParseConfigure|ApplyConfigure|RunConfigure)' -count=1
```

Expected: `ok ai-agent-telemetry`.

- [ ] **Step 5: Commit configure support**

```bash
git add hooks.go main.go commands.go help.go commands_test.go main_test.go
git commit -m "feat(config): persist delivery setting overrides"
```

### Task 3: Apply settings and expose verbose status

**Files:**

- Modify: `main.go`
- Modify: `commands.go`
- Test: `main_test.go`
- Test: `commands_test.go`

**Interfaces:**

- Consumes: `resolveDeliverySettings` and `deliverySettings` from Task 1.
- Changes: `ingest(..., settings deliverySettings) int` uses `settings.BufferCap` and `settings.FlushTimeout`.
- Changes: `gatherStatus(..., settings deliverySettings) statusReport` records effective values.
- Produces: verbose `configuration.buffer_cap` and `configuration.flush_timeout` status fields.

- [ ] **Step 1: Write failing runtime and status tests**

Add an ingest test with capacity `1` and an empty endpoint, then assert that only the newest event remains. Add status
tests that assert compact output omits the settings and verbose output includes them.

```go
func TestFormatStatusShowsDeliverySettingsOnlyWhenVerbose(t *testing.T) {
    report := statusReport{BufferCap: 1000, FlushTimeout: 30 * time.Second}
    if strings.Contains(formatStatus(report, false), "buffer_cap") {
        t.Fatal("compact status contains delivery settings")
    }
    verbose := formatStatus(report, true)
    for _, want := range []string{"configuration:", "  buffer_cap: 1000", "  flush_timeout: 30s"} {
        if !strings.Contains(verbose, want) {
            t.Fatalf("verbose status = %q, want %q", verbose, want)
        }
    }
}
```

- [ ] **Step 2: Run runtime-focused tests and confirm the red state**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test ./... -run 'Test(Ingest|FormatStatus|GatherStatus)' -count=1
```

Expected: build or assertion failures because runtime paths still use constants.

- [ ] **Step 3: Wire one resolved settings value into each command path**

Resolve settings inside the `ingest`, `flush`, and `status` cases. Print resolver warnings to stderr with a
`configuration:` prefix. Replace `bufferCap` and `flushTimeout` uses in ordinary delivery paths, but retain
`selftestTimeout` for self-test.

Render this block only when `verbose` is true:

```go
fmt.Fprint(&b, "configuration:\n")
fmt.Fprintf(&b, "  buffer_cap: %d\n", r.BufferCap)
fmt.Fprintf(&b, "  flush_timeout: %s\n", r.FlushTimeout)
```

- [ ] **Step 4: Run runtime-focused tests and confirm the green state**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test ./... -run 'Test(Ingest|FormatStatus|GatherStatus)' -count=1
```

Expected: `ok ai-agent-telemetry`.

- [ ] **Step 5: Commit runtime integration**

```bash
git add main.go commands.go main_test.go commands_test.go
git commit -m "feat(delivery): apply configurable limits"
```

### Task 4: Document and verify the public contract

**Files:**

- Modify: `README.md`
- Modify: `docs/cli.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/README.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/.apm/skills/ai-agent-telemetry-configure/SKILL.md`
- Test: all `*_test.go` files

**Interfaces:**

- Consumes: the environment-variable names, defaults, precedence, validation, CLI flags, and verbose status fields from
  Tasks 1–3.
- Produces: operator-facing examples for persistent and temporary overrides.

- [ ] **Step 1: Update reference documentation and skill instructions**

Document this persistent configuration example:

```bash
ai-agent-telemetry configure --buffer-cap=1000 --flush-timeout=30s
```

Document both environment variables, defaults, process-env precedence, positive-value validation, default fallback for
invalid runtime values, and the `status --verbose` verification command.

- [ ] **Step 2: Run formatting and consistency checks**

Run:

```bash
gofmt -w config.go config_test.go hooks.go main.go commands.go help.go commands_test.go main_test.go
git diff --check
```

Expected: no output from `git diff --check`.

- [ ] **Step 3: Run the full test suite with race detection**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test ./... -race -count=1
```

Expected: `ok ai-agent-telemetry` with no race reports.

- [ ] **Step 4: Build the CLI and smoke-test help**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go build -o /tmp/ai-agent-telemetry-configurable .
/tmp/ai-agent-telemetry-configurable help configure
```

Expected: build exit code `0`; help lists `--buffer-cap` and `--flush-timeout`.

- [ ] **Step 5: Commit documentation**

```bash
git add README.md docs/cli.md agent-packages/ai-agent-telemetry-configure/README.md \
  agent-packages/ai-agent-telemetry-configure/.apm/skills/ai-agent-telemetry-configure/SKILL.md
git commit -m "docs: describe configurable delivery settings"
```

- [ ] **Step 6: Review the complete branch against the spec**

Run:

```bash
git diff --stat origin/main...HEAD
git log --oneline origin/main..HEAD
```

Expected: the design, implementation plan, resolver, CLI integration, runtime wiring, tests, and docs are all present.
