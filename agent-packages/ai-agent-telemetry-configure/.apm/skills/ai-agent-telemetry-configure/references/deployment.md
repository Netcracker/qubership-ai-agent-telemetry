# Deployment specifics

These values depend on where the collector runs, so the skill stays generic and asks for them
rather than hardcoding any one deployment.

## Install telemetry

Use the canonical release bootstrap to install the managed CLI, telemetry configuration, and native
telemetry hooks for Claude Code, Cline, Codex, and Cursor. The bootstrap downloads and verifies the
current lifecycle CLI. The managed binary is installed at `~/.local/bin/ai-agent-telemetry` (`.exe`
on Windows), and the lifecycle adds `~/.local/bin` to `PATH`.

```sh
# macOS / Linux
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.sh | sh
# Windows (PowerShell)
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1')))"
```

Interactive installation asks for missing telemetry configuration. For an unattended install, set
the collector endpoint and optional token before passing `--non-interactive`. Existing saved
telemetry configuration also satisfies the endpoint requirement. Noninteractive mode disables
telemetry prompts; a missing endpoint fails preflight before the managed CLI or native hooks change.

```sh
# macOS / Linux
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.sh \
  | AI_AGENT_TELEMETRY_ENDPOINT=https://collector.example/v1/logs \
    AI_AGENT_TELEMETRY_TOKEN=<token> sh -s -- --non-interactive
```

```powershell
# Windows PowerShell
$env:AI_AGENT_TELEMETRY_ENDPOINT = 'https://collector.example/v1/logs'
$env:AI_AGENT_TELEMETRY_TOKEN = '<token>'
$release = 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download'
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod '$release/install.ps1'))) --non-interactive"
```

After installation, prefer the bare name (`ai-agent-telemetry configure`). The current process may
not yet have `~/.local/bin` on `PATH`, so use the full path only until a restart refreshes it:

```sh
~/.local/bin/ai-agent-telemetry configure
```

The installer scripts are published as release assets, so `releases/latest/download` always
resolves to the current installer.

The `PATH` change reaches only new processes: the bare-name hook resolves after the agent is
restarted. Call the binary by its bare name (`ai-agent-telemetry <cmd>`); fall back to the full
path (`~/.local/bin/ai-agent-telemetry <cmd>`) only while the bare name does not yet resolve in
this just-installed session. If the installer cannot update `PATH` automatically it prints the
line to add manually.

## Endpoint

The OTLP/HTTP logs endpoint, of the form `https://<collector-host>/v1/logs`. Get it from the
onboarding portal or an admin. Always `https://` — the sender never sends over plaintext.

## CA certificate

Needed only when the collector's certificate does not chain to a root the machine already
trusts.

- **Public certificate or MDM-distributed corporate CA** — already in the system trust store;
  nothing to do.
- **Corporate CA, not in the trust store** — download the root CA from the internal
  distribution point, then pass its path to `configure --ca`.
- **Local self-signed cluster (cert-manager)** — extract the CA from the issuing secret, then
  pass the file to `configure --ca`:

  ```sh
  kubectl -n <namespace> get secret <ca-secret> \
    -o jsonpath='{.data.tls\.crt}' | base64 -d > ca.crt
  ```

## Confirm delivery in the store (optional)

Only if the user has read access to the store. After `selftest`, confirm the probe landed by
querying for its probe name, e.g. against VictoriaLogs:

```sh
curl -s '<query-url>/select/logsql/query' --data-urlencode 'query=skill.name:="__selftest__"'
```

The probe carries `skill.name="__selftest__"`, so it is easy to find and easy to filter out of
real skill-usage metrics on the collector.

Most participants won't have read access — a passing `selftest` (accepted and dequeued) is the
guarantee to rely on.
