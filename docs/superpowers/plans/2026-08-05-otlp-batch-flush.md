# OTLP Batch Flush Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Flush the outbox as one OTLP/HTTP export (true batch) under a short timeout, matching the original
local-sender design and stopping serial per-event POSTs from wedging delivery.

**Architecture:** Wrap the OTLP `sdklog.Exporter` in a small buffering decorator used by `deliverEvents`.
`SimpleProcessor` may still `Export` once per `Emit`; the decorator accumulates records and performs a single
inner `Export` during `Shutdown`. Preserve the existing exporter retry policy and `2s` ordinary flush timeout;
the outer flush context remains the hard wall-clock limit.

**Tech Stack:** Go, `go.opentelemetry.io/otel/sdk/log`, `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp`,
existing `httptest` OTLP capture tests.

**Spec:** `docs/superpowers/specs/2026-08-05-otlp-batch-flush-design.md`

## Global Constraints

- English-only committed files and Conventional Commits.
- Do not edit dated historical docs under `docs/superpowers/` except by adding this plan/spec; update live
  docs in `docs/cli.md`.
- Preserve all-or-nothing outbox delete: no partial commit.
- Preserve `selftestTimeout = 10s`.
- Preserve the ordinary default `flush_timeout = 2s`.
- Preserve the OTLP/HTTP exporter retry policy.
- No daemon / scheduled flusher.
- Prefer decorator over `BatchProcessor` (no poll goroutine for a one-shot CLI flush).

## File map

| File | Responsibility |
|---|---|
| `flush.go` | `batchingExporter`, wire it in `deliverEvents`, retain shutdown failures in both flush modes |
| `flush_test.go` | Assert one HTTP request for multi-event flush; keep failure/retention coverage |
| `docs/cli.md` | Document batched flush while retaining the `2s` default |

---

### Task 1: Prove multi-event flush is one OTLP request

**Files:**

- Modify: `flush_test.go`
- Test: `flush_test.go`

**Interfaces:**

- Consumes: existing `Flush`, `seed`, `newOTLPCapture`, `capturedRecords`
- Produces: failing test `TestFlushBatchesMultipleEventsIntoOneOTLPRequest` that requires one collector POST

- [ ] **Step 1: Write the failing test**

Add to `flush_test.go`:

```go
func TestFlushBatchesMultipleEventsIntoOneOTLPRequest(t *testing.T) {
	isolateConfigCache(t)
	capture := newOTLPCapture(t)
	defer capture.server.Close()

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 5)

	sent, err := Flush(s, capture.server.URL, "", nil, 2*time.Second)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 5 {
		t.Fatalf("sent = %d, want 5", sent)
	}
	if got := len(capture.requests); got != 1 {
		t.Fatalf("OTLP requests = %d, want 1 (batched export)", got)
	}
	if got := len(capturedRecords(capture.requests)); got != 5 {
		t.Fatalf("log records = %d, want 5", got)
	}
	files, _ := s.List()
	if len(files) != 0 {
		t.Fatalf("outbox not cleared: %d files remain", len(files))
	}
}
```

Also tighten `TestFlushSendsAndClearsOnSuccess` so `atomic.LoadInt32(&hits)` must equal `1` (not merely non-zero) after seeding 3 events.

Add a second test with `seed(t, s, 100)` that verifies the local capture receives one request containing 100
records. This is a request-shape regression test; it must not assert a real collector latency target.

- [ ] **Step 2: Run the new test and confirm it fails on request count**

Run:

```bash
cd /home/mew/work/repos/qubership-ai-agent-telemetry/.worktrees/cursor-nested-repository-attribution
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test . -count=1 -run 'TestFlushBatchesMultipleEventsIntoOneOTLPRequest|TestFlushSendsAndClearsOnSuccess'
```

Expected: FAIL with `OTLP requests = 5, want 1` (or hits != 1 for the tightened test).

- [ ] **Step 3: Commit the failing tests**

```bash
git add flush_test.go
git commit -m "$(cat <<'EOF'
test(flush): require single OTLP request per multi-event flush

EOF
)"
```

---

### Task 2: Buffering exporter and shutdown-error retention

**Files:**

- Modify: `flush.go`
- Test: `flush_test.go` (from Task 1)

**Interfaces:**

- Produces: `type batchingExporter struct` implementing `sdklog.Exporter`
- Produces: `func newBatchingExporter(inner sdklog.Exporter) *batchingExporter`
- Changes: `deliverEvents` wraps the factory result with `newBatchingExporter`
- Changes: `deliverEvents` treats `provider.Shutdown(ctx)` failure as delivery failure in
  both strict and opportunistic flush paths

- [ ] **Step 1: Implement `batchingExporter` in `flush.go`**

Add (near the top of the flush helpers, after imports already include what you need):

```go
// batchingExporter accumulates Export calls and sends one inner.Export on
// Shutdown. SimpleProcessor exports per Emit; this restores the
// design's single OTLP batch per flush without BatchProcessor poll loops.
type batchingExporter struct {
	inner  sdklog.Exporter
	mu     sync.Mutex
	buffer []sdklog.Record
}

func newBatchingExporter(inner sdklog.Exporter) *batchingExporter {
	return &batchingExporter{inner: inner}
}

func (b *batchingExporter) Export(_ context.Context, records []sdklog.Record) error {
	if len(records) == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buffer = append(b.buffer, records...)
	return nil
}

func (b *batchingExporter) Shutdown(ctx context.Context) error {
	flushErr := b.flush(ctx)
	shutErr := b.inner.Shutdown(ctx)
	return errors.Join(flushErr, shutErr)
}

func (b *batchingExporter) ForceFlush(ctx context.Context) error {
	return b.flush(ctx)
}

func (b *batchingExporter) flush(ctx context.Context) error {
	b.mu.Lock()
	records := b.buffer
	b.buffer = nil
	b.mu.Unlock()
	if len(records) == 0 {
		return nil
	}
	return b.inner.Export(ctx, records)
}
```

Add `"sync"` to the import block in `flush.go`.

- [ ] **Step 2: Wrap the exporter in `deliverEvents`**

Replace the exporter construction block so the provider always sees the batching wrapper:

```go
	if exporterFactory == nil {
		exporterFactory = newOTLPLogExporter
	}
	inner, err := exporterFactory(ctx, endpoint, token, tlsConfig)
	if err != nil {
		recordLastDeliveryError(s, err)
		return 0, err
	}
	exp := newBatchingExporter(inner)
```

Keep the rest of `deliverEvents` (SimpleProcessor, Emit loop, Shutdown, delete-on-success) unchanged.

- [ ] **Step 3: Retain the outbox when shutdown reports the batched export failure**

```go
	deliveryErr := exportIssues.err()
	if shutdownErr != nil {
		deliveryErr = errors.Join(deliveryErr, fmt.Errorf("shut down exporter: %w", shutdownErr))
	}
	if deliveryErr != nil {
		if strict {
			retainedIssues.add(deliveryErr)
			err := retainedIssues.err()
			recordLastDeliveryError(s, err)
			return 0, err
		}
		recordLastDeliveryError(s, deliveryErr)
		return 0, deliveryErr
	}
```

Replace the existing strict/non-strict branch immediately after `provider.Shutdown(ctx)` with this shared
delivery-error check. Leave `newOTLPLogExporter` unchanged so its existing retry policy remains available
within the outer context deadline.

- [ ] **Step 4: Run flush tests**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test . -count=1 -run 'TestFlush'
```

Expected: all `TestFlush*` pass. In particular, `TestFlushKeepsBufferOnServerError` must still retain
all events; this catches the regression where the single batch error arrives from `Shutdown` and an
opportunistic flush mistakenly deletes the outbox.

- [ ] **Step 5: Commit**

```bash
git add flush.go flush_test.go
git commit -m "$(cat <<'EOF'
fix(flush): export outbox as one OTLP batch

SimpleProcessor exported each Emit as its own HTTPS POST, so a short flush
timeout could not drain a normal backlog. Buffer records so one attempt
matches the original batch-flush design.

EOF
)"
```

---

### Task 3: Document batched delivery

**Files:**

- Modify: `docs/cli.md`

**Interfaces:**

- Docs: state one flush attempt sends one OTLP request with all selected records and that the
  ordinary timeout remains configurable with a `2s` default

- [ ] **Step 1: Update live delivery documentation**

In `docs/cli.md` buffering section, replace the “2-second ordinary flush timeout” sentence with wording that:

- default ordinary flush timeout remains **2 seconds**;
- a flush attempt exports buffered events in **one** OTLP/HTTP request;
- delete still happens only after that export succeeds.

- [ ] **Step 2: Run focused tests**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test . -count=1 -run 'TestFlush'
```

Expected: PASS.

- [ ] **Step 3: Full suite and build**

Run:

```bash
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go test ./... -count=1
env GOCACHE=/tmp/ai-agent-telemetry-go-cache go build -o /tmp/ai-agent-telemetry-batch .
```

Expected: tests PASS; build succeeds.

- [ ] **Step 4: Commit**

```bash
git add docs/cli.md
git commit -m "$(cat <<'EOF'
docs(cli): describe batched outbox delivery

Explain that one ordinary flush exports its selected outbox records in one
OTLP request while retaining the existing configurable timeout.

EOF
)"
```

---

## Self-review

1. **Spec coverage:** batch export, all-or-nothing delete, unchanged short timeout and retry policy, docs, tests — each has a task.
2. **Placeholders:** none; concrete code and commands included.
3. **Types:** `batchingExporter` implements `sdklog.Exporter` with `Export` / `ForceFlush` / `Shutdown` matching the SDK.

## Manual verification (after implementation)

Optional on a machine with a real collector:

1. Enqueue ~25 synthetic outbox events (or temporarily lower nothing — use a test helper / copy fixtures).
2. `ai-agent-telemetry flush` with default settings should clear the outbox in one attempt.
3. `ai-agent-telemetry status` shows `buffered: 0`.
