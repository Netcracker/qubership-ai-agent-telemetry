# Event schema and privacy decisions — 2026-06-12

Decisions taken after the first developer end-to-end run of the Codex sender. The run worked,
but the emitted event carried a few fields that were either privacy-sensitive or unclear in
the dashboards. This document records what changed in the event schema and why, so the
reasoning survives past the session.

Scope: the OTLP log record the sender emits (`sender/flush.go`) and the fields that feed it
(`sender/event.go`, `sender/adapter.go`, `sender/machine.go`). The collector, gateway, and
storage are infrastructure and are out of scope.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| `repo.path` | Removed | The local working directory leaks `/Users/<username>/…` — personal data. The repo is identified by `repo.remote` alone. `cwd` stays in-process only, to resolve the git remote. |
| Repos without a remote | Left unidentified | A non-git checkout, or one with no `origin`, now has no repo label. Accepted: rare, and not worth leaking the path to cover. |
| `service.name` value | `qubership-skills-telemetry-sender` | `skills-telemetry` was ambiguous in dashboards — it read like the harness or the telemetry domain, not our sender. Derived from the canonical package name `qubership-skills-telemetry` plus a `-sender` suffix for the component. |
| `service.name` key | Kept the standard OTel key | Rejected renaming to `telemetry.sender.name`. `service.name` is an OTel resource convention that VictoriaLogs and Grafana key on; renaming it loses that interop, and the clarified value already resolves the confusion. |
| Instrumentation scope | Set `scope.version` to the build version | `scope.version` was `unknown` because no version was passed. The logger now uses `WithInstrumentationVersion(version)`, and `scope.name` matches the service name. The scope duplicates `service.*` for a self-emitting binary, but OTel always creates a logger from some scope, so the field cannot be removed — only made meaningful. |
| `turn.id` | Removed | Finer than `session.id`, with no value for skill-usage analytics. The only theoretical use, server-side dedup of duplicate hook fires, is not needed. |
| `machine.id` | Added | An anonymous, stable per-install identifier, so the collector can tell installs apart — for example, to spot one install skewing skill-usage counts — without identifying the user. |
| `machine.id` source | Random UUID v4, persisted | Minted once with `crypto/rand` on first run and stored under `os.UserConfigDir()` at `qubership-skills-telemetry/machine-id` (0600). Never derived from hardware or the username. The user can delete the file to reset it. Rejected hashing the OS or hardware machine-id: it collapses every account on a shared machine into one id and reads as a fingerprint. |
| `machine.id` key | Kept as a custom key | Rejected `host.id` (the OTel convention means the real hardware or cloud host id; our value is a random UUID — a semantic mismatch) and `device.id` (the `device.*` namespace is built for client and mobile apps, not a CLI). A custom `machine.*` group names the concept honestly and needs no backend special-casing. |
| Transport security | TLS required, no plaintext fallback | The event and the access token never leave the machine unencrypted. The endpoint is always `https://`, certificate verification is never skipped, and a TLS failure keeps the event in the spool instead of downgrading. CA trust is hybrid and additive: the sender adds a CA at the well-known path `<config>/ca.crt` to the system trust pool when the file exists, else uses the system store alone, else the send fails. The CA file stays optional — for local development and deployments without a trusted certificate. See the provisioning decisions record. |

## Resulting event shape

The OTLP log record now carries:

- `agent` — the harness that produced the event (`codex`).
- `session.id` — the harness session identifier.
- `repo.remote` — the git remote URL, when one resolves. The only repo identifier.
- `skill.name`, `skill.source` — from the `[skill-called]` marker.

Resource attributes:

- `service.name` = `qubership-skills-telemetry-sender`, `service.version` = the build version.
- `machine.id` = the anonymous per-install UUID.

Removed since the first run: `repo.path`, `turn.id`. Scope `unknown` version: fixed.

## Verification

Re-ran the developer end-to-end after rebuilding the binary: a synthetic Codex Stop payload
through the installed bootstrap and cached binary, over OTLP/HTTP to the local collector,
queried back from VictoriaLogs. The stored record showed `repo.path` and `turn.id` absent,
`machine.id` present and grouped into the stream, `service.name` and `scope.version` correct.

The skill did not need reinstalling: the changes are confined to the Go binary, delivered
through the per-machine cache. Reinstalling would have wiped the detection marker from the
deployed skill, so it was deliberately left alone.
