# Multi-harness native metrics design

## Goal

Extend the remote metrics pilot with explicit, tested onboarding for every
harness that can export native OTLP metrics. Keep the existing hook-based
telemetry contract for supported harnesses that do not provide a native metrics
exporter.

The repository supports Claude Code, Codex, and Cursor hooks. This design also
adds Cline as a documented native-metrics client without claiming full hook or
Agent Package Manager (APM) integration.

## Support matrix

| Harness | Hook events | Native metrics | This PR | APM |
| --- | --- | --- | --- | --- |
| Codex | Supported | Supported | Tested | Supported |
| Claude Code | Supported | Supported | Tested | Supported |
| Cursor | Supported | Not documented | Event-derived | Supported |
| Cline | Not supported | Supported | Tested | Not supported |

The matrix distinguishes four independent capabilities. Hook support does not
imply native metrics support, and an APM target does not imply telemetry
support.

## Architecture

The backend architecture does not change:

```text
Harness native OTLP metrics
          │
          ▼
Caddy /v1/metrics
          │
          ▼
OpenTelemetry Collector
          │
          ▼
VictoriaMetrics
          │
          ▼
Grafana Prometheus datasource
```

Each native exporter sends OTLP/HTTP metrics to the authenticated
`/v1/metrics` endpoint. The Collector converts delta metrics to cumulative
values, batches them, and exports them to VictoriaMetrics. VictoriaMetrics
applies Prometheus naming rules while preserving resource and datapoint
attributes as labels.

The backend does not normalize vendor metric names in the first phase.
Dashboards use harness-specific queries and combine them only when the
underlying meanings are equivalent.

## Harness contracts

### Codex

Codex uses the existing `[otel]` configuration with:

- `metrics_exporter` set to the remote OTLP/HTTP endpoint;
- `exporter = "none"` for logs;
- `trace_exporter = "none"` for traces;
- `log_user_prompt = false`.

The live pilot confirms that Codex exports process, thread, tool, MCP, token,
API latency, and turn latency metrics. Prometheus naming produces series such
as `codex_thread_started_total`, `codex_tool_call_total`, and
`codex_turn_token_usage_count`.

Native Codex metrics do not expose a stable installation identifier. The
documentation keeps the single-installation pilot limit until a producer
identity is available.

### Claude Code

Claude Code uses its standard OpenTelemetry environment variables:

- enable telemetry;
- enable the OTLP metrics exporter;
- select `http/protobuf`;
- set the metrics endpoint to `/v1/metrics`;
- set the shared bearer header;
- leave logs and traces disabled.

Claude Code exports stable metric names for sessions, tokens, cost, active
time, code changes, commits, pull requests, and permission decisions. Its
standard attributes include `service.name`, `session.id`, and
installation-scoped `user.id`.

When the user authenticates through OAuth, Claude Code can attach `user.email`
to metrics. This PR does not add a Collector privacy processor, so the
documentation must disclose that behavior before showing the configuration.

Source: [Claude Code monitoring][claude-monitoring].

### Cursor

Cursor remains supported through the repository's hook adapter. The available
official Cursor documentation describes hooks and structured CLI output but
does not define a native OTLP metrics exporter.

The README must not claim native Cursor metrics. Cursor activity stays in
VictoriaLogs and can feed event-derived dashboard panels. Adding a
Cursor-native exporter is out of scope until Cursor publishes a supported
contract.

Source: [Cursor hooks][cursor-hooks].

### Cline

Cline uses its `CLINE_OTEL_*` environment variables:

- enable telemetry;
- enable only the OTLP metrics exporter;
- select `http/protobuf`;
- set the metrics endpoint to `/v1/metrics`;
- set the shared bearer header;
- leave the logs exporter unset.

Cline provides separate metrics and logs endpoints, custom authentication
headers, and a configurable export interval. Its documented metrics cover
usage, task execution, errors, and performance. Distributed tracing is not
supported.

Cline states that exported telemetry is anonymous and excludes code, file
paths, and sensitive content. The backend still treats all received labels as
telemetry data that requires normal access control.

Sources:

- [Cline OpenTelemetry integration][cline-otel]
- [Cline environment variable override][cline-override]

## Cline and APM

Microsoft APM does not define a first-class `cline` target. Passing
`--target cline` would fail target validation, so this repository must not add
Cline to `hookTarget` or forward it to APM as part of this PR.

APM's generic `agent-skills` target deploys project skills to `.agents/skills`.
Cline documents `.cline/skills` and `~/.cline/skills` as its native locations.
The generic target does not cover Cline rules, hooks, commands, user-scope
layout, or MCP configuration, so this design does not treat it as Cline
support.

A separate feature can add a Cline hook adapter after APM gains a native target
or this repository defines an explicit non-APM installation path.

Source: [Microsoft APM target registry][apm-targets].

## Documentation changes

The backend README gains:

1. A support matrix that uses the capability definitions in this design.
2. Metrics-only setup instructions for Codex, Claude Code, and Cline.
3. A Cursor note that points readers to existing hook-based telemetry.
4. Privacy and identity caveats next to the affected client configuration.
5. A statement that Cline is not an accepted `--harnesses` or APM target.

Examples use placeholders for the server address and ingest token. They do not
enable prompts, logs, tool content, or traces.

## Test design

The smoke suite gains deterministic OTLP metrics fixtures for Claude Code and
Cline in addition to the Codex fixture. Each fixture includes:

- a vendor-appropriate `service.name`;
- a representative metric name;
- bounded low-cardinality labels;
- rendered timestamps;
- a deterministic value.

The fixture stack renders all timestamps before starting the test. The smoke
test:

1. Sends every fixture through the authenticated `/v1/metrics` endpoint.
2. Polls the read-only Prometheus query endpoint until every expected series
   appears.
3. Verifies the metric value and one identifying label.
4. Preserves the existing negative authentication and write-access tests.

The configuration contract continues to verify one generic metrics pipeline.
Harness-specific routing is unnecessary because all native clients use the
same OTLP receiver and exporter.

## Error handling

- Invalid or missing bearer credentials return `401` at Caddy.
- Unsupported OTLP payloads surface in Collector logs and do not affect the
  logs pipeline.
- A missing harness metric fails the bounded smoke-test poll with the expected
  metric name and value.
- Documentation directs Cline users to `TEL_DEBUG_DIAGNOSTICS=true` when its
  exporter cannot connect.
- Documentation does not provide a Cline APM command that the upstream CLI
  rejects.

## Non-goals

- Adding a Collector privacy processor.
- Adding traces or native harness logs.
- Normalizing vendor metrics into a new common metric schema.
- Adding dashboards in this PR.
- Adding a Cline hook adapter or detector.
- Adding a Cline target to Microsoft APM or the lifecycle CLI.
- Claiming native OTLP support for Cursor.

## Acceptance criteria

- Codex, Claude Code, and Cline have metrics-only remote configuration
  examples.
- Cursor's support level is explicit and does not imply a native exporter.
- The README discloses Claude Code identity attributes and the Codex producer
  identity limitation.
- The smoke test verifies representative metrics from Codex, Claude Code, and
  Cline.
- Existing logs, dashboards, authentication boundaries, and tests remain
  unchanged.
- Cline is not passed to APM or accepted by `--harnesses`.

<!-- markdownlint-disable MD013 -->
[claude-monitoring]: https://code.claude.com/docs/en/monitoring-usage
[cursor-hooks]: https://cursor.com/blog/hooks-partners
[cline-otel]: https://docs.cline.bot/enterprise-solutions/monitoring/opentelemetry
[cline-override]: https://docs.cline.bot/enterprise-solutions/monitoring/opentelemetry_override
[apm-targets]: https://github.com/microsoft/apm
<!-- markdownlint-enable MD013 -->
