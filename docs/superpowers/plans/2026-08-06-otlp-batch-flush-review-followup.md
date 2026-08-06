# Plan: address vlsi blockers on PR #36

## Goals

1. An expired flush context with an empty batch buffer must be a delivery failure
   (`sent=0`, non-nil error, outbox retained, `last_delivery_error` recorded — not cleared).
2. Ordinary delivery failures (HTTP/network) must surface as `export events: …`, not
   `shut down exporter: …`, because the single export now runs during Shutdown.

## Approach

- After the emit loop / `provider.Shutdown`, if `ctx.Err() != nil` and no delivery error
  was already captured, join `export events: %w` from `ctx.Err()` into `deliveryErr`.
- In `batchingExporter.Shutdown`, wrap `flush` failures as `export events:` and inner
  shutdown failures as `shut down exporter:`; `deliverEvents` joins `shutdownErr` without
  re-labeling.
- Tests: expired-context flush; HTTP 401/500 label assertion.

## Non-goals

- README/spec wording sync (suggestion only).
- Changing retry policy or default timeout.
