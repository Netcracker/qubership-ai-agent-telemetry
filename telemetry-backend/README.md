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
| `VM_MAX_HOURLY_SERIES` | Maximum unique metric series accepted during one hour. |
| `VM_MAX_DAILY_SERIES` | Maximum unique metric series accepted during one day. |
| `VM_MIN_FREE_DISK_SPACE_BYTES` | Free space VictoriaMetrics reserves before rejecting new writes. |
| `VM_SELF_SCRAPE_INTERVAL` | Interval for collecting VictoriaMetrics operational metrics. Keep `30s` in production. |
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

The example above changes only metrics retention. Existing `.env` values remain in effect after an upgrade. Add
`VL_RETENTION=365d` separately if you also want to extend log retention; omitting either variable uses the 365-day
Compose default for that backend.

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
  installations; active-installation distributions; and hourly machine ID, duplicate-delivery, and MCP-duration
  quality signals.
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
| Claude Code | Supported | Supported | Live pilot and backend fixture | Supported |
| Cursor | Supported | Not documented | Existing hook coverage | Supported |
| Cline | Supported | Supported | Backend fixture | Not supported |

`Backend fixture` means that a manually authored OTLP payload passes through the authenticated backend pipeline.
It does not validate a client binary, configuration syntax, environment variable support, header encoding, or actual
export behavior.

`Live pilot` means that an actual client exported metrics to the deployed backend.
The current pilots cover Codex CLI `0.146.0` and Claude Code `2.1.220`, observed on July 30, 2026. Cline onboarding is
documentation-derived until a version-pinned client integration test or recorded live pilot is available.

### Native metrics and hook telemetry

Native exporters provide vendor counters and histograms. `ai-agent-telemetry` hooks provide normalized skill, MCP,
command, repository, and machine events. One signal does not replace the other: native metrics describe the vendor's
runtime behavior, while hooks capture repository-aware activity.

The backend accepts, stores, and visualizes supported native OTLP metrics, but this release does not configure harness
exporters. The `ai-agent-telemetry` lifecycle installer configures hook telemetry only. Follow
[Native OTLP onboarding](native-otlp-onboarding.md) to opt in manually, verify delivery, or remove the configuration.

The Collector removes `session.id`, `user.email`, `user.account_uuid`, and `organization.id` from native metric
resource and data-point attributes before export. Add new sensitive vendor attributes to both privacy processors before
supporting an exporter that emits them.

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

Caddy exposes only the Prometheus read endpoints allowlisted in `Caddyfile`, plus `/prometheus/vmui/*`. A Grafana
feature that calls an unsupported Prometheus API receives `404 not found`; ingest, remote-write, and delete APIs are not
available through dashboard credentials.

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

Production maintenance uses the ordinary Compose project
`ai-agent-telemetry-backend`, backend root
`/opt/ai-agent-telemetry-backend`, and backup root
`/opt/ai-agent-telemetry-backups`. Install a first release as described in the
[release guide](https://github.com/Netcracker/qubership-ai-agent-telemetry/blob/main/docs/release.md#first-backend-installation),
then use the standalone backup and update commands for subsequent maintenance.

| Task | Command |
| --- | --- |
| Stop the stack and preserve data | `docker compose down` |
| Stop the stack and delete all data | `docker compose down -v` |
| View Grafana logs | `docker compose logs -f grafana` |
| View Caddy logs | `docker compose logs -f caddy` |
| View Collector logs | `docker compose logs -f collector` |
| View VictoriaMetrics logs | `docker compose logs -f victoriametrics` |
| Restart Grafana only | `docker compose restart grafana` |

The default VictoriaMetrics limits accept 50,000 unique series per hour and 200,000 per day, and reserve 1 GiB of free
disk. Storage growth depends on the active series count, sampling interval, and label churn; measure the daily change
in `vm_data_size_bytes` during the pilot instead of assuming a fixed size per installation. Size the limits for the
deployment before increasing them. Monitor `vm_hourly_series_limit_current_series`,
`vm_daily_series_limit_current_series`, `vm_free_disk_space_bytes`, and `vm_data_size_bytes`. VictoriaMetrics drops new
series after a cardinality limit is reached and stops accepting writes when free space falls below the configured
reserve. `VM_SELF_SCRAPE_INTERVAL` controls how often VictoriaMetrics records these operational metrics. Keep the `30s`
default in production because shorter intervals increase the number of stored samples; the backend fixture uses `5s`
only to keep its bounded readiness check fast.

Before a release, update the `Target version` default in `grafana/dashboards/telemetry-health.json` to the release being
rolled out. The textbox remains editable at dashboard runtime for staged adoption checks.

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
| Allowlisted `/prometheus/api/v1/*` reads and `/prometheus/vmui/*` | VictoriaMetrics `:8428` | Caddy Basic Auth |
| everything else | None | `404 not found` |
