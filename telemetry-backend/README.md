# Collector backend

A self-contained observability backend that receives skill-usage telemetry from the
`ai-agent-telemetry` CLI. Three containers behind a single reverse proxy:

- **Caddy** — TLS termination, bearer-token auth on the ingest path, basic-auth on
  the query UI. The only service that exposes ports.
- **OpenTelemetry Collector** — accepts OTLP/HTTP logs from Caddy and forwards them
  to VictoriaLogs.
- **VictoriaLogs** — log storage and the built-in query UI (VMUI).

```
CLI ──OTLP/HTTPS──▸ Caddy ──▸ OTel Collector ──▸ VictoriaLogs
                      │
                      └──▸ /select/*  (VMUI, basic-auth)
```

## Prerequisites

- Docker Engine 24+ with Compose v2.
- A machine with a public IP (for Let's Encrypt) or `localhost` (for local
  development with Caddy's internal CA).
- Ports 80 and 443 open and unoccupied (80 is needed for the ACME challenge;
  for local dev you can remap both in `.env`).

## Setup

### 1. Create the environment file

```sh
cp .env.example .env
```

Edit `.env` and fill in every value. The fields:

| Variable | Purpose |
|---|---|
| `SITE_ADDRESS` | Domain Caddy serves. VPS: `<ip-with-dashes>.sslip.io`. Local: `localhost`. |
| `CADDY_TLS` | TLS mode. VPS: an ACME email (Let's Encrypt). Local: `internal`. |
| `INGEST_TOKEN` | Shared bearer token the CLI sends with each request. Generate a strong random value (see the comment in `.env.example`). |
| `VL_RETENTION` | How long VictoriaLogs keeps data (e.g. `30d`). |
| `HTTP_PORT` | Host port for HTTP (ACME). VPS: `80`. Local: any free port. |
| `HTTPS_PORT` | Host port for HTTPS (traffic). VPS: `443`. Local: any free port. |

### 2. Set the VictoriaLogs UI password

The Caddyfile protects `/select/*` (VMUI) with basic auth. Replace the placeholder
hash:

```sh
# Pick a password and generate its bcrypt hash:
docker run --rm caddy:2 caddy hash-password --plaintext 'YourPassword'
```

Open `Caddyfile`, find the `basic_auth` block, and paste the hash in place of
`<REPLACE_WITH_BCRYPT_HASH>`. Change the username from `admin` if you prefer.

### 3. Start the stack

```sh
docker compose up -d
```

On a VPS, Caddy obtains a Let's Encrypt certificate automatically (first request
takes a few seconds while the ACME challenge completes). Locally with
`CADDY_TLS=internal`, Caddy mints a self-signed certificate for `localhost`
immediately.

### 4. Verify the stack

Check that all three containers are healthy:

```sh
docker compose ps
```

Confirm TLS and ingest auth:

```sh
# Should return 401 (no token):
curl -s -o /dev/null -w '%{http_code}' https://$SITE_ADDRESS:$HTTPS_PORT/v1/logs

# Should return 200 or 400 (token accepted, no body):
source .env
curl -sk -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $INGEST_TOKEN" \
  https://$SITE_ADDRESS:$HTTPS_PORT/v1/logs
```

Open the VMUI in a browser at `https://<SITE_ADDRESS>/select/vmui/` and log in
with the username and password you set in step 2.

## Operations

| Task | Command |
|---|---|
| Stop the stack (data preserved) | `docker compose down` |
| Stop and delete all data | `docker compose down -v` |
| View Caddy logs (TLS, auth) | `docker compose logs -f caddy` |
| View collector logs | `docker compose logs -f collector` |
| Query events from the host | `curl -su admin:'<password>' 'https://<SITE_ADDRESS>/select/logsql/query?query=skill_executed&limit=5'` |

## Routing

Caddy is the single entry point. All other services are on an internal Docker
network with no published ports.

| Path | Backend | Auth |
|---|---|---|
| `/v1/logs` | OTel Collector `:4318` | `Authorization: Bearer <INGEST_TOKEN>` |
| `/select/*` | VictoriaLogs `:9428` | Basic auth (VMUI + query API) |
| everything else | — | `401 unauthorized` |
