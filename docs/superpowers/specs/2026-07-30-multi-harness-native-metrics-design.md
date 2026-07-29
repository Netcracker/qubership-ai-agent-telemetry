# Multi-harness native metrics design

## Goal

Extend the remote metrics pilot with explicit onboarding, backend fixture
coverage, and Grafana dashboards for the native-metrics clients in the support
matrix. Keep the existing hook-based telemetry contract for supported
harnesses that do not provide a native metrics exporter.

The repository supports Claude Code, Codex, and Cursor hooks. This design also
adds Cline as a documented native-metrics client without claiming full hook or
Agent Package Manager (APM) integration. The matrix is the scope boundary, not
an exhaustive inventory of every agent harness with an OTLP exporter.

## Support matrix

| Harness | Hook events | Native metrics | Validation in this PR | APM |
| --- | --- | --- | --- | --- |
| Codex | Supported | Supported | Live pilot and backend fixture | Supported |
| Claude Code | Supported | Supported | Backend fixture | Supported |
| Cursor | Supported | Not documented | Existing hook coverage | Supported |
| Cline | Not supported | Supported | Backend fixture | Not supported |

The matrix distinguishes four independent capabilities. Hook support does not
imply native metrics support, and an APM target does not imply telemetry
support.

`Backend fixture` means that a manually authored OTLP payload passes through
the repository's authenticated backend pipeline. It does not validate a client
binary, client configuration syntax, environment variable support, header
encoding, or actual export behavior.

`Live pilot` means that an actual client exported metrics to the deployed
backend. The current pilot covers Codex CLI `0.146.0`, observed on July 30,
2026. Claude Code and Cline onboarding remains documentation-derived until a
version-pinned client integration test or recorded live pilot is added.

## Architecture

The backend topology does not change:

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
values, batches the data, and exports it to VictoriaMetrics. VictoriaMetrics
applies Prometheus naming rules while preserving resource and datapoint
attributes as labels.

The metrics processor order remains `deltatocumulative`, then `batch`. The
Collector does not rename vendor metrics, copy them into a canonical schema, or
add a harness identity label.

## Normalization boundary

Harnesses publish different metric names, instrument types, units, labels, and
signal coverage. A shared name alone cannot make a counter equivalent to a
histogram or replace a metric that a harness emits only as a log event.

The Collector preserves every vendor metric name and instrument. Dashboard
queries perform semantic normalization after storage and combine only
equivalent signals. Exact source selectors classify the documented contracts:

| Harness | Query selector |
| --- | --- |
| Codex | `service_name="codex_cli_rs"` |
| Claude Code | `service_name=~"claude-code|claude-code-desktop"` |

These selectors organize dashboard results. They are not authenticated
producer identities because every OTLP attribute remains client-controlled
while harnesses share one ingest credential. The dashboards do not read or
trust a client-provided `agent.harness`, `agent_harness`, or spelling variant.

Cline's public documentation does not define a stable `service.name`. The
manually authored fixture is not evidence of a client identity contract, so
this PR does not include Cline in a dashboard query. A Cline mapping requires a
recorded live export or a version-pinned client contract.

Stable mappings can move from dashboard expressions to VictoriaMetrics
recording rules after live multi-harness data validates their semantics.
Canonical series could then use names such as
`ai_agent_sessions_started_total` or `ai_agent_tokens_total`. This promotion
must not change or delete the raw vendor series. Collector-side metric copies
remain out of scope because they duplicate stored series and cannot resolve
semantic differences between instrument types.

## Dashboard design

This PR adds two provisioned Grafana dashboards that use the VictoriaMetrics
Prometheus datasource.

### Native agent metrics overview

The overview compares the safe intersection of Codex and Claude Code signals.
It contains:

- metric freshness by harness;
- top-level sessions started over the selected period;
- token usage by harness, model, and token type;
- observed client versions; and
- a signal-availability table that distinguishes an unsupported signal from a
  supported signal whose value is zero.

Codex session queries select `codex_thread_started_total` with
`session_source="cli"` so spawned subagent threads do not count as user
sessions. Claude Code session queries select its session counter and exclude
`start_type="agents_view"`, which represents the local agents dashboard rather
than a conversational session.

Token panels preserve each vendor's documented token categories. They do not
sum categories whose cache semantics may overlap, and they do not imply that
Codex and Claude Code calculate cached input identically.

The overview uses separate harness-specific expressions and readable legends.
It does not depend on a stored synthetic harness label.

### Codex native metrics

The Codex dashboard uses the live `0.146.0` pilot data to cover:

- top-level sessions, conversation turns, tool calls, MCP calls, and tokens;
- tool failure ratio;
- tool and MCP activity over time;
- top tools, MCP servers, and MCP outcomes;
- token usage by model and token type;
- turn duration at p50 and p95;
- tool duration at p50 and p95;
- API time to first token, time between tokens, and inference latency; and
- skill activity when a live series has stable, useful labels.

The dashboard does not synthesize cost, active sessions, in-progress work, or
other signals that Codex does not export. Panel descriptions identify the
vendor metric and any semantic limitation.

A Claude-specific deep-dive dashboard remains follow-up work until a real
Claude Code client exports enough data to validate query names, units, labels,
and visual behavior.

## Harness contracts

### Codex

Codex uses an exporter selector and inline exporter table. The endpoint cannot
be assigned directly to `metrics_exporter`.

<!-- markdownlint-disable MD013 -->
```toml
[otel]
environment = "prod"
exporter = "none"
trace_exporter = "none"
log_user_prompt = false
metrics_exporter = { otlp-http = { endpoint = "https://<host>/v1/metrics", protocol = "binary", headers = { Authorization = "Bearer <token>" } } }
```
<!-- markdownlint-enable MD013 -->

The `otlp-http` table requires `endpoint` and `protocol`. The optional
`headers` table carries the ingest credential. Setting `exporter` and
`trace_exporter` to `"none"` keeps the client in metrics-only mode.

The live pilot confirms that Codex exports process, thread, tool, MCP, token,
API latency, and turn latency metrics. Prometheus naming produces series such
as `codex_thread_started_total`, `codex_tool_call_total`, and
`codex_turn_token_usage_count`.

Native Codex metrics do not expose a stable installation identifier. The
documentation keeps the single-installation pilot limit until a producer
identity is available.

Source: [Codex configuration schema][codex-schema].

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

Cursor remains supported through the repository's hook adapter. As of July 30,
2026, a review of the public Cursor documentation found no documented native
OTLP metrics exporter. This is a bounded documentation finding, not proof that
no private, experimental, or future exporter exists.

The README must not claim native Cursor metrics. Cursor activity stays in
VictoriaLogs and can feed event-derived dashboard panels. Adding a
Cursor-native exporter is out of scope until Cursor publishes a supported
contract.

Sources:

- [Cursor documentation index][cursor-docs]
- [Cursor hooks][cursor-hooks]

### Cline

Cline uses its `CLINE_OTEL_*` environment variables:

- enable telemetry;
- enable the OTLP metrics exporter;
- select `http/protobuf`;
- set the metrics endpoint to `/v1/metrics`;
- set the shared bearer header;
- do not set `CLINE_OTEL_LOGS_EXPORTER` in the local environment.

Leaving `CLINE_OTEL_LOGS_EXPORTER` unset does not guarantee metrics-only
behavior. Environment variables take precedence over Remote Configuration, but
Cline documents only `console` and `otlp` as supported logs exporter values. It
does not document a `none` value. If organization Remote Configuration enables
logs, an environment-only setup may leave that exporter active.

Before onboarding Cline under a metrics-only policy, an administrator must
disable logs in the Cline dashboard. The operator then starts Cline once with
`TEL_DEBUG_DIAGNOSTICS=true` and confirms in the VS Code Developer Tools
Console that the effective configuration creates a metrics exporter and no
logs exporter. If a logs exporter appears, onboarding stops until the Remote
Configuration is corrected.

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
2. Metrics-only setup instructions for Codex and Claude Code.
3. Conditional Cline setup instructions with an effective-configuration check.
4. A Cursor note that points readers to existing hook-based telemetry.
5. Privacy and identity caveats next to the affected client configuration.
6. A statement that Cline is not an accepted `--harnesses` or APM target.
7. An explanation of the difference between native metrics and the repository's
   hook telemetry.
8. Links to the native metrics overview and Codex deep-dive dashboards.

Examples use placeholders for the server address and ingest token. They do not
enable prompts, logs, tool content, or traces.

## Test design

The backend smoke suite gains deterministic OTLP metrics fixtures for Claude
Code and Cline in addition to the Codex fixture. Each fixture includes:

- a documented or test-scoped `service.name`;
- a representative metric name;
- bounded low-cardinality labels;
- rendered timestamps;
- a deterministic value.

The Cline fixture uses a test-scoped service name that is not presented as a
client contract and does not match an identity transform rule.

The fixture stack renders all timestamps before starting the test. The smoke
test:

1. Sends every fixture through the authenticated `/v1/metrics` endpoint.
2. Polls the read-only Prometheus query endpoint until every expected series
   appears.
3. Verifies the metric value and one identifying label.
4. Preserves the existing negative authentication and write-access tests.

The Codex and Claude Code fixtures include representative session and token
metrics. They confirm that Prometheus-normalized vendor names and documented
labels remain queryable. The Cline fixture verifies backend compatibility
without assigning a dashboard classification.

The configuration contract verifies one shared metrics pipeline without an
identity transform. Harness-specific routing is unnecessary because all native
clients use the same OTLP receiver and exporter.

The dashboard contract verifies:

- valid JSON and stable dashboard UIDs;
- the provisioned `victoriametrics` datasource;
- exact Codex and Claude Code source selectors;
- the absence of a dependency on `agent_harness`;
- the required overview and Codex deep-dive panels; and
- documented units and readable legends for token and duration panels.

Browser validation loads both dashboards in the local Grafana experiment and
checks that the Codex panels render against the accumulated pilot data.

These tests provide backend fixture coverage only. They do not launch Codex,
Claude Code, or Cline, and they do not claim that the documented client
configuration works against a specific client release.

## Error handling

- Invalid or missing bearer credentials return `401` at Caddy.
- Unsupported OTLP payloads surface in Collector logs and do not affect the
  logs pipeline.
- A missing harness metric fails the bounded smoke-test poll with the expected
  metric name and value.
- An unknown or missing `service.name` remains stored as raw telemetry but does
  not match a dashboard harness selector.
- An unsupported dashboard signal appears as unsupported rather than zero.
- Documentation directs Cline users to `TEL_DEBUG_DIAGNOSTICS=true` when its
  exporter cannot connect.
- Documentation does not provide a Cline APM command that the upstream CLI
  rejects.

## Non-goals

- Adding a Collector privacy processor.
- Adding traces or native harness logs.
- Renaming or copying vendor metrics into a common schema at ingestion.
- Adding VictoriaMetrics recording rules.
- Adding a Claude-specific deep-dive dashboard before a live pilot.
- Adding a Cline hook adapter or detector.
- Adding a Cline target to Microsoft APM or the lifecycle CLI.
- Claiming native OTLP support for Cursor.
- Onboarding GitHub Copilot CLI or any harness outside the support matrix.

GitHub Copilot CLI has a documented native OTLP metrics exporter, including
token, tool-call, and latency metrics. It remains outside this PR because it is
not one of the repository's declared hook harnesses and was not part of the
requested Cline extension. A separate design can add it without changing this
scope boundary.

Source: [GitHub Copilot CLI OpenTelemetry reference][copilot-otel].

## Acceptance criteria

- Codex and Claude Code have metrics-only remote configuration examples.
- Cline has a conditional remote configuration example that requires logs to
  be disabled in Remote Configuration and verified in diagnostics.
- Cursor's support level is explicit and does not imply a native exporter.
- The README discloses Claude Code identity attributes and the Codex producer
  identity limitation.
- The smoke test verifies representative backend fixture payloads for Codex,
  Claude Code, and Cline without claiming client integration coverage.
- Raw vendor metric names remain queryable without a Collector identity
  transform or canonical metric copies.
- The native metrics overview uses explicit Codex and Claude Code selectors and
  combines only semantically compatible signals.
- The Codex deep-dive dashboard renders sessions, turns, tool and MCP activity,
  tokens, failures, and latency from live pilot series.
- Unsupported signals are distinguishable from supported signals whose value
  is zero.
- Existing logs, dashboards, and authentication boundaries remain unchanged.
- Existing test assertions continue to pass.
- Cline is not passed to APM or accepted by `--harnesses`.

<!-- markdownlint-disable MD013 -->
[claude-monitoring]: https://code.claude.com/docs/en/monitoring-usage
[codex-schema]: https://github.com/openai/codex/blob/main/codex-rs/core/config.schema.json
[copilot-otel]: https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference#opentelemetry-monitoring
[cursor-docs]: https://cursor.com/docs
[cursor-hooks]: https://cursor.com/blog/hooks-partners
[cline-otel]: https://docs.cline.bot/enterprise-solutions/monitoring/opentelemetry
[cline-override]: https://docs.cline.bot/enterprise-solutions/monitoring/opentelemetry_override
[apm-targets]: https://github.com/microsoft/apm
<!-- markdownlint-enable MD013 -->
