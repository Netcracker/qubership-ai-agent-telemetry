# Native OTLP onboarding

The telemetry backend accepts native OTLP metrics from the harnesses listed in the
[support matrix](README.md#native-agent-metrics). This release does not configure harness exporters automatically.
The `ai-agent-telemetry` lifecycle installer configures hook telemetry only.

Use this guide to opt in to native metrics manually. Native metrics complement hook telemetry; they do not replace
repository-aware skill, MCP, command, and session events.

## Before you start

Obtain the remote Caddy address and `INGEST_TOKEN` from the backend operator. Keep TLS verification enabled. Replace
`<SITE_ADDRESS>` and `<INGEST_TOKEN>` in the examples below.

Native Codex metrics do not include a stable producer identifier. Cumulative counters from installations with
identical resource labels can collide in one time series. Limit the current pilot to one Codex installation until a
producer-identity design is available.

Claude Code includes an anonymous installation-scoped `user.id` in every telemetry export. OAuth authentication can
also add `user.email`, organization, and account attributes. The Collector removes the known `user.email`,
`user.account_uuid`, and `organization.id` attributes before metrics storage. This minimum removal list is not a full
vendor-attribute allowlist, so review the effective exporter attributes against your privacy policy.

## Codex metrics only

Add the following block to the user-level `~/.codex/config.toml`:

```toml
[otel]
environment = "prod"
exporter = "none"
trace_exporter = "none"
log_user_prompt = false
metrics_exporter = { otlp-http = { endpoint = "https://<SITE_ADDRESS>/v1/metrics", protocol = "binary", headers = { Authorization = "Bearer <INGEST_TOKEN>" } } }
```

If the file already contains an `[otel]` table, merge these settings into that table instead of adding a second table.
Fully restart Codex and run a turn.

This configuration exports metrics without exporting Codex logs, traces, or user prompts. Codex does not expand
environment variables in OTLP header values, so the token is plaintext in `config.toml`. Restrict the file to the user
account and rotate the shared ingest token if the file is exposed.

## Claude Code metrics only

Set these variables in the shell that starts Claude Code, then start the client:

```bash
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_METRICS_EXPORTER=otlp
export OTEL_LOGS_EXPORTER=none
export OTEL_TRACES_EXPORTER=none
export OTEL_EXPORTER_OTLP_METRICS_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=https://<SITE_ADDRESS>/v1/metrics
export OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer <INGEST_TOKEN>'
export OTEL_METRICS_INCLUDE_SESSION_ID=false
claude
```

`OTEL_METRICS_INCLUDE_SESSION_ID=false` avoids per-session high-cardinality labels. It reduces privacy exposure,
storage cost, and query cardinality. The default metrics export interval is 60 seconds. Run `claude --debug` to expose
export failures when the expected metrics do not arrive.

## Cursor

As of July 30, 2026, public Cursor documentation has no documented native OTLP metrics exporter. Use the existing hook
telemetry for Cursor activity.

## Cline

Enable telemetry and the OTLP metrics exporter in the Cline dashboard's Remote Configuration. Set the metrics endpoint
to `https://<SITE_ADDRESS>/v1/metrics`, use `http/protobuf`, and provide an `Authorization` header containing
`Bearer <INGEST_TOKEN>`.

Environment overrides take precedence over Remote Configuration, but Cline documents only `console` and `otlp` for
its logs exporter. It does not document a `none` value, so an environment-only setup cannot guarantee metrics-only
export. An administrator must disable logs in Remote Configuration before onboarding Cline.

Start VS Code once with diagnostics enabled and inspect the Developer Tools Console:

```bash
TEL_DEBUG_DIAGNOSTICS=true code .
```

The effective configuration must contain a metrics exporter and no logs exporter. If a logs exporter appears, stop
onboarding until Remote Configuration is corrected. Do not assume a native Cline metric name or stable `service.name`;
the backend fixture proves backend compatibility but does not define a dashboard selector.

Cline is an accepted lifecycle `--harnesses` target. The lifecycle installs its global file hook. When APM is already
on `PATH`, it installs the optional configure skill through APM's `agent-skills` target because Cline has no first-class
APM target. This hook path is separate from the native metrics configuration in this section. GitHub Copilot CLI is
outside this support matrix.

## Verify ingestion

After a complete harness restart and at least one interaction, allow one export interval and open:

- `/grafana/d/native-agent-metrics-overview` for Codex and Claude Code signal availability and sample age;
- `/grafana/d/codex-native-metrics` for the Codex detail dashboard;
- `/prometheus/vmui/` for an ad hoc VictoriaMetrics query.

For Codex, query `codex_thread_started_total`. For Claude Code, query `claude_code_session_count_total`. A backend
fixture proves only that the ingest pipeline accepts a payload. A successful live check requires a real client export.

## Remove the configuration

For Codex, remove the native-metrics settings from the existing `[otel]` table or set `metrics_exporter = "none"`,
then fully restart Codex. Preserve unrelated `[otel]` settings.

For Claude Code, remove the variables listed above from the shell profile, wrapper, or process environment that starts
Claude Code, then fully restart it.

For Cline, disable the metrics exporter in Remote Configuration and remove any telemetry environment overrides. Start
VS Code once with `TEL_DEBUG_DIAGNOSTICS=true` and verify that the metrics exporter is absent.
