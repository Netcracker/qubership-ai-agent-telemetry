# Remote Codex Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Accept native Codex OTLP metrics remotely, store them in VictoriaMetrics, and expose them to Grafana without
changing the existing logs pipeline.

**Architecture:** Caddy authenticates OTLP logs and metrics on separate paths and proxies both to the OpenTelemetry
Collector. The Collector keeps logs in VictoriaLogs and sends metrics to a dedicated VictoriaMetrics service. Grafana
uses internal datasources, while external Grafana installations query both stores through Basic Auth routes in Caddy.

**Tech Stack:** Docker Compose, Caddy, OpenTelemetry Collector, VictoriaLogs, VictoriaMetrics, Grafana, POSIX shell,
and jq.

## Global Constraints

- Preserve the existing `/v1/logs` behavior and VictoriaLogs data volume.
- Publish host ports only from Caddy.
- Require the shared ingest bearer token for `/v1/logs` and `/v1/metrics`.
- Require dashboard Basic Auth for the VictoriaMetrics query API.
- Disable Codex logs, traces, and prompt logging in the documented client configuration.
- Limit the initial native-metrics pilot to one Codex installation.

---

### Task 1: Metrics backend and configuration contract

**Files:**

- Modify: `telemetry-backend/tests/config-contract.sh`
- Modify: `telemetry-backend/.env.example`
- Modify: `telemetry-backend/docker-compose.yml`
- Modify: `telemetry-backend/otel-collector-config.yaml`
- Modify: `telemetry-backend/Caddyfile`

**Interfaces:**

- Consumes: OTLP/HTTP metrics on `/v1/metrics`.
- Produces: Prometheus-compatible metrics in VictoriaMetrics on the internal backend network.

- [ ] Add a failing configuration contract for the VictoriaMetrics service, volume, retention, and port isolation.
- [ ] Run `sh telemetry-backend/tests/config-contract.sh` and confirm that the new contract fails.
- [ ] Add the VictoriaMetrics service and `VM_RETENTION` configuration.
- [ ] Add the Collector metrics pipeline and authenticated Caddy routes.
- [ ] Run `sh telemetry-backend/tests/config-contract.sh` and confirm that it passes.
- [ ] Commit with `feat(telemetry): add remote OTLP metrics pipeline`.

### Task 2: Datasource and end-to-end smoke coverage

**Files:**

- Modify: `telemetry-backend/grafana/provisioning/datasources/victorialogs.yaml`
- Create: `telemetry-backend/tests/fixtures/otel-metrics.json`
- Modify: `telemetry-backend/tests/with-fixture-stack.sh`
- Modify: `telemetry-backend/tests/smoke.sh`

**Interfaces:**

- Consumes: the authenticated OTLP metrics endpoint and the Caddy `/prometheus/*` query route.
- Produces: Grafana datasource UID `victoriametrics` and a verified `codex_tool_call_total` series.

- [ ] Add failing smoke assertions for endpoint authentication, metric ingestion, querying, and datasource health.
- [ ] Run the smoke test and confirm that the new assertions fail because metrics support is absent.
- [ ] Add a deterministic OTLP metric fixture and render its timestamp in the fixture stack.
- [ ] Provision the VictoriaMetrics Prometheus datasource.
- [ ] Run `TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 sh telemetry-backend/tests/smoke.sh`.
- [ ] Commit with `test(telemetry): cover metrics ingest and queries`.

### Task 3: Deployment and pilot documentation

**Files:**

- Modify: `telemetry-backend/README.md`

**Interfaces:**

- Consumes: the metrics backend and datasource from Tasks 1 and 2.
- Produces: server upgrade, external Grafana, and Codex pilot instructions.

- [ ] Document the two-storage architecture and `VM_RETENTION`.
- [ ] Document `/v1/metrics`, `/prometheus/*`, and the external Grafana datasource.
- [ ] Add a metrics-only Codex `[otel]` configuration example.
- [ ] Document the literal bearer-token caveat and the single-installation pilot limit.
- [ ] Run all backend and repository tests.
- [ ] Commit with `docs(telemetry): document Codex metrics pilot`.

### Task 4: Final verification

**Files:**

- Verify only; no planned file changes.

**Interfaces:**

- Consumes: the completed implementation.
- Produces: evidence that the branch is ready for review.

- [ ] Run `go test ./...`.
- [ ] Run `sh telemetry-backend/tests/config-contract.sh`.
- [ ] Run `TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 sh telemetry-backend/tests/smoke.sh`.
- [ ] Review the diff, repository status, and contribution rules.
