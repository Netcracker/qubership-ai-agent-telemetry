# OTLP batch flush design

## Goal

Restore the original local-sender delivery model: opportunistic flush sends the outbox as
**one OTLP logs export** under a **hard short timeout**, and deletes buffered files only when
that export succeeds. Fix the accidental per-event HTTP round-trip that makes a full outbox
unable to drain within the default timeout.

## Background

The 2026-06-12 local telemetry sender design required:

- enqueue fast so hooks never block the agent turn on the network;
- opportunistic flush of buffered events as a **batch** over OTLP/HTTP;
- a **hard short timeout**;
- delete-on-success for the flush (at-least-once; duplicates acceptable).

The implementation used the OpenTelemetry `SimpleProcessor`, which calls `Exporter.Export`
on every `Emit`. Each outbox file therefore became its own HTTPS POST. Measured steady-state
cost on a reachable collector was roughly 150ms per event after a colder first request, so a
default `2s` budget could only cover a small prefix. Because delete is all-or-nothing for the
flush, a timeout left the entire outbox in place and later retries repeated the same prefix.

The `2s` default was a “hard short timeout” heuristic, not a capacity calculation sized for
serial POSTs up to `buffer_cap` (100).

## Decision

1. **Batch export.** Keep reading every outbox file and building one log record per valid
   event, but perform a **single** `Export` of that record slice to the OTLP/HTTP exporter
   before considering the flush successful.
2. **Preserve delete semantics.** On a clean export (and clean shutdown of the exporter),
   delete the files that were included in that export. On any export/shutdown failure, delete
   nothing.
3. **Keep the ordinary timeout at `2s`.** The existing setting is a deliberately short
   hook-path budget. Its configured override remains available when an installation needs
   more time, but there is no measurement yet that justifies changing the global default
   after the serial-request defect is removed.
4. **Keep the exporter’s current retry policy.** The outer flush context already limits a
   retry to the ordinary timeout. Disabling retries would be an unrelated behavior change
   and could prevent a prompt, throttled retry from succeeding.
5. **Treat shutdown errors as delivery errors for every flush mode.** `SimpleProcessor`
   reports the decorator's single inner export from `Shutdown`, not from an individual
   `Emit`. Both opportunistic and explicit flush must retain the outbox when
   `provider.Shutdown(ctx)` returns an error.

## Non-goals

- Partial delete / per-message commit.
- A background daemon or scheduled flusher.
- Changing `flushCountN` (10) or `flushIntervalT` (60s).
- Changing buffer capacity defaults.
- Changing event schema or harness adapters.

## Implementation shape

Prefer a small `sdklog.Exporter` decorator used by `deliverEvents`:

- `Export` appends records to an in-memory buffer and returns nil;
- `Shutdown` calls the inner exporter **once** with the buffered slice, then shuts it down;
- `ForceFlush` exports the buffered slice when invoked, but ordinary delivery uses
  `provider.Shutdown`;
- `deliverEvents` continues to use `SimpleProcessor` + `Emit` for record construction and
  the existing global OTel error-handler capture, and handles its returned shutdown error
  in both strict and opportunistic modes.

Avoid `BatchProcessor` for this CLI: it starts polling goroutines and interval-based export,
which is the wrong fit for a single short-lived flush.

## Observability / docs

- Document in `docs/cli.md` (and README delivery defaults) that ordinary flush sends one OTLP
  request per attempt containing all selected outbox records. The ordinary timeout remains
  `2s` by default.
- No new status fields required for this change.

## Testing

- Unit test: flushing N>1 seeded events yields **one** collector HTTP request containing N
  log records; outbox empty afterward.
- Existing opportunistic and explicit flush failure tests retain outbox files when the batched
  export error surfaces from shutdown.
- Unit test: the default-capacity-sized (100-record) batch is encoded in one request against
  the local OTLP capture; this proves request shape, not a real-collector latency guarantee.
- Full `go test ./...` and `go build`.

## Success criteria

- A full default outbox is attempted in one OTLP request rather than one request per event.
- Hook ingest still exits quickly on success (one RTT, not N).
- Failed export still leaves files for retry; no partial local delete.
