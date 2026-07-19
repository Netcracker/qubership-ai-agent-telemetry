# Collector backend

A self-contained observability backend for `ai-agent-telemetry`. Caddy is the only published service. It routes
authenticated ingest to OpenTelemetry Collector, dashboards to Grafana, and diagnostic queries to VictoriaLogs.

```text
CLI ──OTLP/HTTPS──▸ Caddy ──▸ OTel Collector ──▸ VictoriaLogs
                      │                              ▲
                      ├──▸ /grafana/* ──▸ Grafana ───┘
                      └──▸ /select/*  ──▸ VMUI
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
| `GRAFANA_ADMIN_USER` | Grafana administrator username. |
| `GRAFANA_ADMIN_PASSWORD` | Initial Grafana administrator password. |
| `VL_RETENTION` | VictoriaLogs retention, such as `30d`. |
| `HTTP_PORT`, `HTTPS_PORT` | Published Caddy ports. Keep `80` and `443` on a public server. |

Do not put the plaintext dashboard password in `.env`. `GRAFANA_ADMIN_PASSWORD` initializes a new `grafana-data`
volume; changing it later does not change an existing administrator account.

### 3. Start and verify the stack

```sh
docker compose up -d --build
docker compose ps
```

Open these URLs and enter `DASHBOARD_AUTH_USER` plus the original dashboard password:

- `https://<SITE_ADDRESS>:<HTTPS_PORT>/grafana/` for management dashboards;
- `https://<SITE_ADDRESS>:<HTTPS_PORT>/select/vmui/` for ad hoc VictoriaLogs queries.

For Grafana administration, open `https://<SITE_ADDRESS>:<HTTPS_PORT>/grafana/login` after passing Caddy Basic Auth,
then enter `GRAFANA_ADMIN_USER` and `GRAFANA_ADMIN_PASSWORD`. Normal viewers remain anonymous Viewer users inside
Grafana and cannot edit provisioned dashboards.

With `CADDY_TLS=internal`, trust the generated Caddy root certificate in the browser or accept the local certificate
warning. Do not disable certificate verification for production clients.

## Dashboards

The default time range is 30 days, except Telemetry health, which defaults to seven days.

- **Executive overview** — active installations, repositories, sessions, adoption trend, and top usage.
- **Skill adoption** — reach and frequency by skill, trend, concentration, and repository detail.
- **MCP usage and reliability** — calls, tools, servers, failure rate, latency, outcomes, and repository detail.
- **Command adoption** — Claude Code command coverage, trends, sources, and repository detail.
- **Telemetry health** — delivery-ID coverage, duplicates, duration coverage, versions, harnesses, and missing fields.

Filters expose repositories, harnesses, operating systems, CLI versions, skills, commands, and MCP names where they
apply. Dashboards never display raw machine, session, or event identifiers.

Event totals use distinct `event.id` values to collapse delivery retries. Legacy records without an ID remain visible
in active installation and repository counts but cannot be included safely in deduplicated event totals. MCP failure
rate excludes `unknown` outcomes, and latency uses only events that contain `mcp.duration_ms`.

## Connect another Grafana through Caddy

To test the dashboards from a separate Grafana instance, add a VictoriaLogs datasource with:

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

## Operations

| Task | Command |
| --- | --- |
| Run the complete local backend test | `sh tests/smoke.sh` |
| Stop the stack and preserve data | `docker compose down` |
| Stop the stack and delete all data | `docker compose down -v` |
| View Grafana logs | `docker compose logs -f grafana` |
| View Caddy logs | `docker compose logs -f caddy` |
| View Collector logs | `docker compose logs -f collector` |
| Restart Grafana only | `docker compose restart grafana` |

Reset an initialized Grafana administrator password with:

```sh
docker compose exec grafana grafana cli admin reset-admin-password '<new-admin-password>'
```

## Routing

| Path | Backend | Authentication |
| --- | --- | --- |
| `/v1/logs` | OpenTelemetry Collector `:4318` | `Authorization: Bearer <INGEST_TOKEN>` |
| `/grafana/*` | Grafana `:3000` | Caddy Basic Auth; anonymous Viewer or Grafana admin session inside |
| `/select/*` | VictoriaLogs `:9428` | Caddy Basic Auth |
| everything else | None | `404 not found` |
