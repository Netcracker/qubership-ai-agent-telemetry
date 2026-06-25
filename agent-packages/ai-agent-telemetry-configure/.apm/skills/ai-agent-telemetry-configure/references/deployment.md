# Deployment specifics

These values depend on where the collector runs, so the skill stays generic and asks for them
rather than hardcoding any one deployment.

## Install the binary

Run once per machine to install the binary onto `PATH`. The installer downloads the release
binary to `~/.local/bin/ai-agent-telemetry` (`.exe` on Windows), verifies its checksum against the
published `SHA256SUMS`, and adds `~/.local/bin` to `PATH`. It is idempotent and **install-only**
— it never runs the binary.

```sh
# macOS / Linux
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/bootstrap.sh | sh
# Windows (PowerShell)
iex "& { $(irm https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/bootstrap.ps1) }"
```

Then configure with the binary itself. Prefer the bare name (`ai-agent-telemetry configure`); right
after install `~/.local/bin` is not on this process's `PATH` yet, so until a restart refreshes it
the bare name will not resolve and you fall back to the full path:

```sh
~/.local/bin/ai-agent-telemetry configure
```

The bootstrap scripts are published as release assets, so `releases/latest/download` always
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
