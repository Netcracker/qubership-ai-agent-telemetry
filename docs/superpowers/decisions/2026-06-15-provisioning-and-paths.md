# Provisioning mechanic and path layout — 2026-06-15

How the sender's per-machine configuration reaches a machine, and where each kind of file
lives. The sender needs an endpoint, an access token, and — for a self-signed collector — a CA
certificate, none of which can ship in the public package. These decisions fix how a user sets
them up once per machine and where they are stored.

Scope: the provisioning step and the on-disk layout. The collector, gateway, and storage are
infrastructure and are out of scope.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Deterministic core | A `provision` subcommand on the sender binary | The cross-platform Go binary already ships and caches per machine. One subcommand does the security-sensitive work — atomic writes of the `env` file and `ca.crt` with the right permissions, validation, idempotency, and no-echo token entry (`golang.org/x/term`). No parallel `provision.sh` / `provision.ps1` to keep in sync. |
| Primary front-end | A provisioning skill that orchestrates the core | The project runs inside a skill-capable harness, so a skill drives onboarding in natural language: it discovers the endpoint, extracts or locates the CA, asks for anything missing, calls `skills-telemetry provision` for the writes, and verifies with `skills-telemetry status`. The model handles discovery and interaction; the binary handles the correct write and the verification. Rejected letting the skill write `env` / `ca.crt` directly: writing secrets and TLS material non-deterministically risks wrong paths, permissions, and format. |
| Token entry | No-echo through the binary, never through the model | The skill defers the token to the binary's no-echo prompt, so the secret never enters the model's context, the transcript, or a terminal scrollback. The endpoint and CA are not secrets and can flow through the skill. |
| Headless front-end | A `curl \| sh` one-liner that reuses `bootstrap.sh` as the fetcher | For non-agent and CI contexts. `bootstrap.sh \| sh -s -- provision` ensures the binary is in the cache (same logic and pinned version as events, so one shared cache entry) and runs `skills-telemetry provision`. Provisioning does not wait for the first skill event, and there is no second download implementation. Windows: `iex "& { $(irm bootstrap.ps1) } provision"`. Under the pipe, stdin is the pipe, so the subcommand reads prompts from `/dev/tty` on Unix and the console on Windows. |
| Config file format | `KEY=VALUE`, parsed not sourced | One neutral format both launchers read: `bootstrap.sh` reads and exports each line, `bootstrap.ps1` sets the matching `$env:` variables. PowerShell cannot source a shell file, so parsing keeps a single format across operating systems. |
| Cache vs config split | Disposable data in the cache, durable data in the config directory | The binary and spool stay in `os.UserCacheDir()`; the `env` file, CA certificate, and `machine.id` go in `os.UserConfigDir()`. The cache is disposable by design — the binary is there because it re-downloads — and macOS purges `~/Library/Caches` under disk pressure. Putting the token or endpoint there would silently drop them and stop telemetry. |
| Config base directory | `os.UserConfigDir()` on every OS | macOS `~/Library/Application Support/qubership-skills-telemetry/`, Linux `~/.config/qubership-skills-telemetry/`, Windows `%AppData%\qubership-skills-telemetry\`. Matches the Go standard library and where `token` and `machine.id` already live, so the `provision` subcommand reuses one API. Rejected forcing XDG `~/.config` on macOS: it diverges from `os.UserConfigDir()` and needs a custom path resolver. |
| Discoverability | Onboarding document plus a `status` check | The onboarding document lists provisioning (the skill, or the one-liner for headless contexts) as step 1 and `apm install` / `apm compile` as step 2. `skills-telemetry status` reports whether the machine is provisioned and prints the next step when it is not. Rejected a one-time stderr hint (hook stderr is rarely seen) and an APM post-install message (unverified that APM supports custom post-install output). |
| CA lookup | Auto-discovery of `<config>/ca.crt`, additive | The binary loads `<config>/ca.crt` when it exists and appends it to `x509.SystemCertPool()`, then passes the pool through `otlploghttp.WithTLSClientConfig`. Trust stays additive (system roots plus the private CA), and the `env` file holds no certificate path. Rejected `OTEL_EXPORTER_OTLP_CERTIFICATE` (it leaks a path into `env` and replaces the system pool with only that CA), despite it needing zero code. |
| CA acquisition | A local path, copied by `provision` | `provision` takes a path and copies the certificate to the canonical `<config>/ca.crt`; it does not fetch the certificate. Where the source comes from depends on the deployment: a publicly trusted or MDM-distributed CA needs no file; a corporate CA is downloaded by the user; the local self-signed stand's CA is extracted with a documented command the provisioning skill can run. `provision` stays deployment-agnostic. Rejected URL download and inline PEM paste for now. |

## Consequences

Done (implemented in the binary, TDD):

- `provision` subcommand: `--endpoint` / `--ca` flags, no-echo token entry (prefers `/dev/tty`
  so it works under `curl | sh`), atomic writes of the `env` file (`KEY=VALUE`, merged,
  idempotent) and `ca.crt` to the `os.UserConfigDir()` base.
- CA handling lives in the binary: `caTLSConfig` auto-discovers `<config>/ca.crt` and appends it
  to the system pool, wired through `otlploghttp.WithTLSClientConfig`. The
  `OTEL_EXPORTER_OTLP_CERTIFICATE` approach is dropped.
- The binary is self-sufficient: it reads the endpoint and token from the `env` file directly
  (env var still overrides), so it works when the skill calls it without the bootstrap.
- `status` reports provisioned/not and the next step; `selftest` sends one marked probe and
  confirms it was accepted and left the spool.

Remaining:

- The provisioning skill: discovery, interaction, calling the `provision` core, verifying with
  `status` / `selftest`. It runs the local-stand CA extraction and never routes the token
  through the model.
- The committed `bootstrap.sh` / `bootstrap.ps1` only need to accept a `provision` pass-through
  argument. They no longer need to parse the `env` file, since the binary reads it directly.
- `bootstrap.sh` / `bootstrap.ps1` need a stable published URL so the one-liner can fetch them.
- Collector side: filter probe events (`skill.name="__selftest__"`) out of real skill-usage
  metrics.
