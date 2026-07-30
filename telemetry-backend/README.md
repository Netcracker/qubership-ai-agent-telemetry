# Collector backend

A self-contained observability backend for `ai-agent-telemetry`. Caddy is the only published service. It routes
authenticated ingest to OpenTelemetry Collector, dashboards to Grafana, and diagnostic queries to VictoriaLogs and
VictoriaMetrics.

```text
Agent ──OTLP/HTTPS──▸ Caddy ──▸ OTel Collector ─┬─▸ VictoriaLogs
                        │                       └─▸ VictoriaMetrics
                        │                              ▲
                        ├──▸ /grafana/* ──▸ Grafana ───┘
                        ├──▸ /select/* ──▸ VictoriaLogs VMUI
                        └──▸ /prometheus/* ──▸ VictoriaMetrics
```

Grafana is outside the ingest path. Stopping it does not interrupt telemetry delivery.

## Prerequisites

- Docker Engine 24+ with Compose v2.
- A machine with a public IP, or `localhost` for local testing with Caddy's internal CA.
- Ports 80 and 443 open and unoccupied on a server. Local ports can be changed in `.env`.

## Setup

### 1. Create credentials

Generate separate values for ingest, dashboard viewers, and the Grafana administrator:

```bash
python3 -c 'import secrets; print(secrets.token_urlsafe(32))'
docker run --rm caddy:2 caddy hash-password --plaintext '<dashboard-password>'
python3 -c 'import secrets; print(secrets.token_urlsafe(32))'
```

The Caddy command prints a bcrypt hash. Keep the original dashboard password for viewers.

### 2. Create the environment file

```sh
cp .env.example .env
```

Set every value in `.env`:

| Variable | Purpose |
| --- | --- |
| `SITE_ADDRESS` | Domain served by Caddy. Use `<ip-with-dashes>.sslip.io` on a VPS or `localhost` locally. |
| `CADDY_TLS` | ACME email on a VPS or `internal` locally. |
| `INGEST_TOKEN` | Write-only bearer token used by telemetry clients. |
| `DASHBOARD_AUTH_USER` | Shared read-only username in front of Grafana and VMUI. |
| `DASHBOARD_AUTH_PASSWORD_HASH` | Caddy bcrypt hash, enclosed in single quotes to preserve dollar signs. |
| `GRAFANA_ADMIN_PASSWORD` | Initial Grafana administrator password. |
| `VL_RETENTION` | VictoriaLogs retention, such as `30d`. |
| `VM_RETENTION` | VictoriaMetrics retention, such as `30d`. |
| `HTTP_PORT`, `HTTPS_PORT` | Published Caddy ports. Keep `80` and `443` on a public server. |

Do not put the plaintext dashboard password in `.env`. `GRAFANA_ADMIN_PASSWORD` initializes a new `grafana-data`
volume; changing it later does not change an existing administrator account.

### 3. Start and verify the stack

```sh
docker compose up -d --build
docker compose ps
```

Open these URLs and enter `DASHBOARD_AUTH_USER` plus the original dashboard password. Grafana exchanges the Basic
Auth credentials for a login cookie, so the browser prompts once per Grafana session:

- `https://<SITE_ADDRESS>:<HTTPS_PORT>/grafana/` for management dashboards;
- `https://<SITE_ADDRESS>:<HTTPS_PORT>/select/vmui/` for ad hoc VictoriaLogs queries;
- `https://<SITE_ADDRESS>:<HTTPS_PORT>/prometheus/vmui/` for ad hoc VictoriaMetrics queries.

For Grafana administration, open
`https://<SITE_ADDRESS>:<HTTPS_PORT>/grafana/login?disableAutoLogin=true` after passing Caddy Basic Auth. Then enter
`admin` and `GRAFANA_ADMIN_PASSWORD`. The Grafana administrator username is `admin`. The shared dashboard user receives
the Viewer role under an isolated Grafana identity and cannot edit provisioned dashboards.

With `CADDY_TLS=internal`, trust the generated Caddy root certificate in the browser or accept the local certificate
warning. Do not disable certificate verification for production clients.

## Upgrade an existing stack

Add the dashboard credentials and Grafana administrator password to an existing `.env` before updating the stack:

```dotenv
DASHBOARD_AUTH_USER=viewer
DASHBOARD_AUTH_PASSWORD_HASH='<caddy-bcrypt-hash>'
GRAFANA_ADMIN_PASSWORD=<new-admin-password>
VM_RETENTION=365d
```

Generate the hash as described in [Create credentials](#1-create-credentials), and remove an obsolete
`GRAFANA_ADMIN_USER` entry from `.env`. If an earlier preview created `grafana-data` with any administrator username
other than `admin`, recreate only that volume before updating:

```sh
docker compose down
docker volume ls --filter label=com.docker.compose.volume=grafana-data
docker volume rm <grafana-data-volume>
docker compose up -d --build
```

Select the volume that belongs to this Compose project. Grafana restores provisioned dashboards and the datasource,
but local Grafana changes are lost. Do not run `docker compose down -v`: it deletes both the VictoriaLogs and
VictoriaMetrics data volumes.

Validate and update the stack:

```sh
docker compose config
docker compose up -d --build
```

The administrator username is `admin`. `GRAFANA_ADMIN_PASSWORD` initializes only a new `grafana-data` volume. If the
volume already uses `admin`, reset its password after the update:

```sh
docker compose exec grafana grafana cli admin reset-admin-password '<new-admin-password>'
```

## Dashboards

`Adoption overview` defaults to 30 days. `Telemetry health`, `Native agent metrics overview`, and
`Codex native metrics` default to seven days.

- **Adoption overview** — daily active installations, active repositories, and observed sessions; Telemetry activity,
  Onboarding over time, and Activity per installation (the per-installation ranking); top skills and MCPs; harness and
  operating-system distributions; and skill and MCP repository views in matrix, stacked, and table formats.
- **Telemetry health** — target-version gap; 24-hour and 48-hour inactivity; native-metrics freshness; correlated stale
  installations; and active-installation distributions by version and by harness and operating system.
- **Native agent metrics overview** — signal availability and freshness, hourly sessions and tokens, the model and token
  type matrix, and observed client versions for native Codex and Claude Code metrics.
- **Codex native metrics** — hourly sessions, turns, calls, failure ratio, and tokens; top tool, MCP, and skill
  rankings; the model and token type matrix; and turn, tool, and API latency.

Open the native-metrics dashboards at `/grafana/d/native-agent-metrics-overview` and
`/grafana/d/codex-native-metrics`.

Filters expose repositories, harnesses, operating systems, and CLI versions. Panels do not display raw session or event
identifiers. Activity per installation is the one exception: the installation is the unit of analysis, so its ID labels
each row.

## Native agent metrics

The support matrix is the scope boundary for native metrics and hook telemetry. It is not an exhaustive inventory of
harnesses that might export OTLP metrics.

| Harness | Hook events | Native metrics | Validation | APM |
| --- | --- | --- | --- | --- |
| Codex | Supported | Supported | Live pilot and backend fixture | Supported |
| Claude Code | Supported | Supported | Backend fixture | Supported |
| Cursor | Supported | Not documented | Existing hook coverage | Supported |
| Cline | Not supported | Supported | Backend fixture | Not supported |

`Backend fixture` means that a manually authored OTLP payload passes through the authenticated backend pipeline.
It does not validate a client binary, configuration syntax, environment variable support, header encoding, or actual
export behavior.

`Live pilot` means that an actual client exported metrics to the deployed backend.
The current pilot covers Codex CLI `0.146.0`, observed on July 30, 2026. Claude Code and Cline onboarding is
documentation-derived until a version-pinned
client integration test or recorded live pilot is available.

### Native metrics and hook telemetry

Native exporters provide vendor counters and histograms. `ai-agent-telemetry` hooks provide normalized skill, MCP,
command, repository, and machine events. One signal does not replace the other: native metrics describe the vendor's
runtime behavior, while hooks capture repository-aware activity.

The released `ai-agent-telemetry` binary is sufficient for hook telemetry because it does not configure native
exporters. Configure a native exporter separately only when you want its metrics.

### Codex metrics-only

Add the following block to the user-level `~/.codex/config.toml`. Replace both placeholders with the remote Caddy
address and the ingest token.

```toml
[otel]
environment = "prod"
exporter = "none"
trace_exporter = "none"
log_user_prompt = false
metrics_exporter = { otlp-http = { endpoint = "https://<SITE_ADDRESS>/v1/metrics", protocol = "binary", headers = { Authorization = "Bearer <INGEST_TOKEN>" } } }
```

If `~/.codex/config.toml` already contains an `[otel]` table, merge these settings into that table rather than adding
a second table. Fully restart Codex, run a turn, then verify that `codex_thread_started_total` appears in the Codex
dashboard or a VictoriaMetrics query.

This configuration exports metrics without exporting Codex logs, traces, or user prompts. Codex does not expand
environment variables in OTLP header values, so the token is plaintext in `config.toml`. Restrict the file to the
user account and rotate the shared ingest token if the file is exposed.

Limit the pilot to one Codex installation. Native Codex metrics do not include a stable producer identifier, so
cumulative counters from installations with identical resource labels can collide in the same time series. Add a
producer-identity design before enabling native metrics for additional installations.

### Claude Code metrics-only

Claude Code includes an anonymous installation-scoped `user.id` in every telemetry export. OAuth authentication can
also include `user.email` plus organization and account attributes. This deployment has no Collector privacy
processor, so do not configure this exporter unless the receiving telemetry data is acceptable under your privacy
policy.

Set these variables in the shell that starts Claude Code, replace the placeholders, and then start the client:

```bash
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_METRICS_EXPORTER=otlp
export OTEL_LOGS_EXPORTER=none
export OTEL_TRACES_EXPORTER=none
export OTEL_EXPORTER_OTLP_METRICS_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=https://<SITE_ADDRESS>/v1/metrics
export OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer <INGEST_TOKEN>'
export OTEL_METRICS_INCLUDE_SESSION_ID=false
export OTEL_METRICS_INCLUDE_VERSION=true
claude
```

`OTEL_METRICS_INCLUDE_SESSION_ID=false` avoids per-session high-cardinality labels. It reduces privacy exposure,
storage cost, and query cardinality.

The default metrics export interval is 60 seconds. Run `claude --debug` to expose export failures when the expected
metrics do not arrive.

### Cursor

As of July 30, 2026, public Cursor documentation has no documented native OTLP metrics exporter. Use the existing
hook telemetry for Cursor activity. It remains available in VictoriaLogs and can feed event-derived dashboard panels.

### Cline

Enable telemetry and the OTLP metrics exporter in the Cline dashboard's Remote Configuration. Set the metrics endpoint
to `https://<SITE_ADDRESS>/v1/metrics`, use `http/protobuf`, and provide the ingest bearer header. Environment
overrides take precedence over Remote Configuration, but Cline documents only `console` and `otlp` for its logs
exporter. It does not document a `none` value, so an environment-only setup cannot guarantee metrics-only export.

An administrator must disable logs in Remote Configuration before onboarding Cline.
Start VS Code once with the following command and use the Developer Tools Console to verify the effective exporters.

Verify the effective exporters: a metrics exporter must be present and a logs exporter must be absent.

```bash
TEL_DEBUG_DIAGNOSTICS=true code .
```

If a logs exporter appears, stop onboarding until Remote Configuration is corrected. Do not invent a native Cline
metric name or a stable `service.name`; the backend fixture proves only backend compatibility.

Cline is not an accepted `--harnesses` target or a first-class APM target.

GitHub Copilot CLI is a documented native-OTLP client, but it remains outside this PR and the support matrix.

## Connect another Grafana through Caddy

To query logs from a separate Grafana instance, add a VictoriaLogs datasource with:

- UID: `victorialogs`, which is referenced by the provisioned dashboards;
- URL: `https://<SITE_ADDRESS>:<HTTPS_PORT>` — do not append `/select/logsql`; omit `:<HTTPS_PORT>` when it is `443`;
- access mode: Server/Proxy;
- Basic Auth enabled;
- user: the value of `DASHBOARD_AUTH_USER`;
- password: the original dashboard password, not `DASHBOARD_AUTH_PASSWORD_HASH`.

The datasource appends VictoriaLogs `/select/*` API paths to the base URL. Keep TLS verification enabled. If the
remote server uses a private CA, add that CA to the Grafana container trust store instead of enabling skip-verify.

The equivalent provisioning fields are:

```yaml
apiVersion: 1
datasources:
  - name: Remote VictoriaLogs
    uid: victorialogs
    type: victoriametrics-logs-datasource
    access: proxy
    url: https://<SITE_ADDRESS>:<HTTPS_PORT>
    basicAuth: true
    basicAuthUser: <DASHBOARD_AUTH_USER>
    secureJsonData:
      basicAuthPassword: <dashboard-password>
```

To query metrics, add a Prometheus datasource with:

- UID: `victoriametrics`;
- URL: `https://<SITE_ADDRESS>:<HTTPS_PORT>/prometheus`;
- access mode: Server/Proxy;
- Basic Auth enabled with the dashboard username and plaintext password.

The equivalent provisioning fields are:

```yaml
apiVersion: 1
datasources:
  - name: Remote VictoriaMetrics
    uid: victoriametrics
    type: prometheus
    access: proxy
    url: https://<SITE_ADDRESS>:<HTTPS_PORT>/prometheus
    basicAuth: true
    basicAuthUser: <DASHBOARD_AUTH_USER>
    secureJsonData:
      basicAuthPassword: <dashboard-password>
```

To point the bundled dashboards at a remote server temporarily, sign in as the Grafana administrator and open
**Connections > Data sources > VictoriaLogs**. Replace the URL and configure Basic Auth with the remote Caddy viewer
credentials. Keep the `victorialogs` UID so every provisioned dashboard uses the updated datasource. Grafana restores
the internal datasource settings the next time provisioning runs.

Repeat the process for **VictoriaMetrics**, use `https://<SITE_ADDRESS>:<HTTPS_PORT>/prometheus`, and keep the
`victoriametrics` UID.

## Operations

| Task | Command |
| --- | --- |
| Run the complete local backend test | `sh tests/smoke.sh` |
| Stop the stack and preserve data | `docker compose down` |
| Stop the stack and delete all data | `docker compose down -v` |
| View Grafana logs | `docker compose logs -f grafana` |
| View Caddy logs | `docker compose logs -f caddy` |
| View Collector logs | `docker compose logs -f collector` |
| View VictoriaMetrics logs | `docker compose logs -f victoriametrics` |
| Restart Grafana only | `docker compose restart grafana` |

Reset an initialized Grafana administrator password with:

```sh
docker compose exec grafana grafana cli admin reset-admin-password '<new-admin-password>'
```

## Routing

| Path | Backend | Authentication |
| --- | --- | --- |
| `/v1/logs` | OpenTelemetry Collector `:4318` | `Authorization: Bearer <INGEST_TOKEN>` |
| `/v1/metrics` | OpenTelemetry Collector `:4318` | `Authorization: Bearer <INGEST_TOKEN>` |
| `/grafana/login` | Grafana `:3000` | Caddy Basic Auth, then a Grafana Viewer or administrator session |
| other `/grafana/*` paths | Grafana `:3000` | Grafana session cookie |
| `/select/*` | VictoriaLogs `:9428` | Caddy Basic Auth |
| `/prometheus/*` | VictoriaMetrics `:8428` | Caddy Basic Auth |
| everything else | None | `404 not found` |
