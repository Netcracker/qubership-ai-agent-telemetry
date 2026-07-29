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
VM_RETENTION=30d
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
but local Grafana changes are lost. Do not run `docker compose down -v`: it also deletes the VictoriaLogs data volume.

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

The default time range is 30 days, except Telemetry health, which defaults to seven days.

- **Adoption overview** — installs, repositories, and sessions; skill and MCP activity over time; onboarding growth;
  per-machine, per-harness, and per-OS breakdowns; and skills and MCP tools per repository.
- **Telemetry health** — delivery-ID coverage, duplicates, duration coverage, versions, harnesses, and missing fields.

Filters expose repositories, harnesses, operating systems, and CLI versions. Panels do not display raw session or event
identifiers. The per-machine activity ranking on the Adoption overview is the one exception: the machine is the unit of
analysis there, so its ID labels each row.

The two dashboards count events differently, by design. Telemetry health measures raw `event.id` coverage, so its
event-based panels reflect only records that carry an ID. Adoption overview instead synthesizes a stable ID from the
delivery stream and the original event time for records that lack one. That collapses delivery retries while still
counting harnesses whose events predate `event.id`.

## Enable the Codex metrics pilot

Enable native Codex metrics only after the server exposes `/v1/metrics`. Add this configuration to the Codex
`config.toml`, replace both placeholders, and restart Codex:

```toml
[otel]
environment = "prod"
exporter = "none"
trace_exporter = "none"
log_user_prompt = false
metrics_exporter = { otlp-http = { endpoint = "https://<SITE_ADDRESS>/v1/metrics", protocol = "binary", headers = { Authorization = "Bearer <INGEST_TOKEN>" } } }
```

This configuration exports metrics without exporting Codex logs, traces, or user prompts. Codex does not expand
environment variables in OTLP header values, so `INGEST_TOKEN` is stored literally in `config.toml`. Restrict file
permissions to the user account and rotate the shared ingest token if the file is exposed.

Limit the initial pilot to one Codex installation. Native Codex metrics do not include a stable producer identifier,
so cumulative counters from installations with identical resource labels can collide in the same time series. Add a
producer-identity design before enabling native metrics for additional installations.

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
