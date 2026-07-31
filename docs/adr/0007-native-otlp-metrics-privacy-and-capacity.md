# Native OTLP metrics privacy and capacity boundary

## Status

Accepted

**Date:** 2026-07-31

**Related ADRs:**

- [ADR 0004](0004-event-schema-and-privacy.md), the privacy contract for hook telemetry
- [ADR 0006](0006-generic-event-schema-and-privacy.md), the typed hook-event allowlist

## Context

Native agent exporters send vendor-defined OTLP metrics directly to the backend. Unlike the hook path, the repository
does not construct these payloads and cannot enforce a complete client-side attribute allowlist. Documented exporters
may attach session and account identifiers. Every distinct label set also creates Collector state and a VictoriaMetrics
series, so an unbounded exporter can exhaust memory or disk.

## Decision

Treat native OTLP metrics as a separate, less-trusted ingest contract. Before delta conversion and storage, the
Collector removes these resource and data-point attributes:

- `session.id`;
- `user.email`;
- `user.account_uuid`;
- `organization.id`.

This removal list is a minimum backend safeguard, not a complete allowlist. Supporting a new exporter requires checking
its documented and observed attributes and extending both privacy processors when needed. Hook telemetry keeps the
stricter client-side allowlist from ADRs 0004 and 0006.

Bound state and storage independently:

- run the Collector memory limiter before other metrics processors;
- limit delta-to-cumulative state to 100,000 streams;
- default VictoriaMetrics to 50,000 hourly and 200,000 daily series;
- reserve 1 GiB of free VictoriaMetrics storage.

Operators can tune the VictoriaMetrics limits through environment variables after sizing the deployment. Retention is
not a cardinality control and does not replace these bounds.

## Consequences

- Known session and account identifiers from native metrics do not reach VictoriaMetrics.
- Unknown vendor attributes can still enter storage until the exporter contract is reviewed and the removal list is
  updated.
- Metrics may be rejected under memory, cardinality, or disk pressure instead of destabilizing the entire backend.
- Native OTLP metrics do not inherit the full PII-free guarantee of the repository-generated hook envelope.
