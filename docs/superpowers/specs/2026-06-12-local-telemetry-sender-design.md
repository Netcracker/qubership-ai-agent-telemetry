# Local telemetry sender (otel sender) — design

Working design document. It fixes the language and the high-level architecture of the
local component that takes skill-execution events from the hook and sends them over the
OpenTelemetry protocol to a shared collector.

Context: the hook (see `agent-packages/qubership-skills-telemetry/`) already detects skill
execution and writes records to `.skill-call-hooks/*.jsonl`. This document covers the next
element of the diagram in the root `README.md` — the "otel agent (with buffer)".

Scope: the local sender only. The collector, gateway, and storage (VictoriaMetrics,
VictoriaLogs, Grafana) are infrastructure and are not designed here.

## Decisions

| Decision | Choice |
|---|---|
| Hook → sender model | Hybrid: the hook drops the event into a buffer fast and never blocks the turn; a short-lived process does the sending |
| Language | Go, a single static binary |
| Binary delivery | Decoupled through a per-machine cache: one binary per machine, not a copy in every repository |
| Binary source | Release artifact; the URL and version are baked into the package, the checksum is pinned |
| Bootstrap | `sh` + PowerShell, OS built-in tools only, no Python |
| Binary shape | CLI: `ingest` (primary mode) plus `flush` / `status` / `version` |
| Buffer | Spool directory, one file per event; machine-global |
| Concurrency | Writers never share a file (atomic temp + rename); flush is serialized by a single lock file |
| What leaves the machine | A normalized OTLP event (logs), not raw input; metrics are derived at the collector |
| Collector address | A `--endpoint=` flag in the hook registration template; visible in the hook text, set by the package, not user-configurable |
| Transport security | TLS required: the sender sends only over `https://`, never plaintext. CA trust is hybrid — an optional CA file takes precedence, else the system trust store; if neither validates the collector, the send fails rather than downgrading |

## Why Go

- **A single static binary with no runtime** — nothing to install on the user's machine.
- **Cheap cold start (single-digit milliseconds)** — this matters: the hybrid launches a
  process on every event.
- **Cross-compilation for three operating systems** with one command (`GOOS`/`GOARCH`),
  across darwin/linux/windows × amd64/arm64.
- **A cross-platform cache out of the box** — `os.UserCacheDir()` returns the correct
  directory per OS (`~/.cache` on Linux, `~/Library/Caches` on macOS, `%LOCALAPPDATA%` on
  Windows).
- **A mature official OTel SDK** and mainstream adoption.

Rust would yield a smaller binary, but with decoupled delivery the size no longer matters,
and its adoption is narrower. Python and Node are rejected: both need a runtime on the
machine and have a slow cold start.

## Components and boundaries

### 1. Hook (per-repo, from the package)

Registered by APM at the repository level, harness-specific (its own JSON for
Claude / Codex / Cursor / OpenCode). It does one thing: on a skill-activation event it calls
the bootstrap and passes the hook input through stdin. The harness identity is set by a flag
in the registration template (`--agent=codex` and so on) — this is the only way to know the
agent, since it is not present in the input.

### 2. Bootstrap (per-repo, kilobytes)

A tiny launcher, one script per OS family, both trivial (~30 lines). It carries no parsing
logic — it forwards the raw stdin and all flags (`--agent`, `--endpoint`, …) to the binary
unchanged.

| Step | macOS / Linux (`sh`) | Windows (PowerShell) |
|---|---|---|
| Resolve the cache directory | `$XDG_CACHE_HOME` / `~/.cache`, `~/Library/Caches` | `$env:LOCALAPPDATA` |
| Download the binary (if the right version is missing) | `curl` | `Invoke-WebRequest` |
| Verify the checksum | `shasum -a 256` / `sha256sum` | `Get-FileHash` |
| Run with stdin forwarding | `exec "$bin"` | `& $bin` |

OS built-in tools only — no third-party packages. `sh` is guaranteed on macOS and Linux,
and PowerShell 5.1 ships with every Windows 10/11. Python is rejected as the bootstrap: it
is absent on a fresh macOS or Windows (only stubs that trigger an install).

The bootstrap checks for the binary at the **required version**; the download happens
exactly once per machine per version. Later invocations and other repositories simply
`exec` from the cache.

### 3. Sender binary (one per machine, in the cache) — CLI

A short-lived command-line utility. Not a daemon and not a server: it starts, does its work,
and exits.

```
skills-telemetry ingest --agent=codex --event=Stop   < (stdin: raw hook input)
skills-telemetry flush     # push the buffer now
skills-telemetry status    # buffer size, last flush time, receiver address
skills-telemetry version   # binary version (the bootstrap checks it)
```

CLI in character: input on stdin, diagnostics on stderr, an exit code. No background state
in memory — all state is on disk. It exits fast and **never hangs the agent turn**.

The primary `ingest` mode does two steps per invocation:

1. **enqueue** — the adapter selected by `--agent` extracts the fields, normalizes them into
   one `SkillEvent`, and writes it to the spool directory as a separate file.
2. **opportunistic flush** — if a condition holds (≥ N events accumulated OR ≥ T elapsed
   since the last attempt) and the flush lock is free, it tries to send a batch over OTLP
   with a hard short timeout. There is no daemon and no separate scheduler — the binary
   decides on its own when to send.

## Input parsing: an adapter layer inside the binary

All parsing logic lives in Go, not in the bootstrap (parsing JSON in shell is painful and
means two divergent implementations). What leaves the machine is the normalized event, not
raw input; the raw stdin lives only during parsing and is not stored afterward.

```
adapter/claude.go    — command_name | tool_input (native activation event)
adapter/codex.go     — Stop: scan the marker [skill-called] skill=… source=…
adapter/cursor.go    — afterAgentResponse.text: the same marker
adapter/opencode.go  — use_skill args (native)
      ↓ each one yields a single normalized SkillEvent
shared pipeline: enqueue → opportunistic flush over OTLP
```

The current `detect_skill_call.py` logic (finding the marker
`[skill-called] skill=<…> source=<…>`) is ported into Go, into the Codex and Cursor adapters.
After that, `detect_skill_call.py` is removed.

The event field sources (`agent`, `agent.version`, `session.id`, `repo`, `skill.name`,
`skill.version`, and the universal attributes) follow the "Sourcing the telemetry data" table
in the root `README.md`.

## Buffer and concurrency

Several agents or sessions call the CLI at the same time — these are independent processes
writing to a shared store. The solution is a **spool directory** in a machine-global state
directory (`~/.local/state` / `~/Library/Application Support` / `%LOCALAPPDATA%`), not in the
repository's `.skill-call-hooks/`, so that the buffer and rotation are shared rather than
per-repo.

- **Write:** each event is a separate file (`<ts>-<pid>-<rand>.json`), written to a temporary
  name and atomically renamed (atomic rename on the same filesystem is guaranteed on every
  OS). Writers **never share a file** → zero write contention, zero line corruption.
- **Flush:** serialized by a single advisory lock (`gofrs/flock`: `flock` on Unix,
  `LockFileEx` on Windows). If another process holds the lock, this one simply skips the send
  (the opportunistic model allows it); it does not wait and does not hang the turn.
- **Rotation:** a ring buffer, cap ~100 events. When sending is impossible, events
  accumulate; on overflow the flush owner deletes the oldest files. This guards against the
  scenario "the receiver was down for months → a flood of spam on recovery".
- **Crash safety:** an unfinished temporary file is recognized and ignored (only a file that
  completed its rename is visible).

Contention is negligible in practice: a skill activation is a human-paced event.

## Data flow

```
session in the agent
  → hook (skill activation, --agent=…)
  → bootstrap (resolve the cache; fetch the binary once per machine/version)
  → binary ingest:
       adapter[agent] → SkillEvent → write a file to the spool
       opportunistic flush over OTLP (short timeout, under the flush lock)
         ↑ if the send failed — it stays in the spool, rotated at >100
  → ingress gateway → shared OTel collector → VictoriaMetrics / VictoriaLogs / Grafana
```

We emit **events (OTLP logs)**; metrics are derived at the collector — as in `README.md`.

## Collector address

The receiver address is set by a **`--endpoint=` flag in the hook registration template** and
reaches the binary through the bootstrap (which forwards all flags unchanged):

```
bootstrap --agent=codex --endpoint=https://otel.<infra>/v1/logs   < stdin
```

This keeps the README principle under the decoupled architecture:

- **Visible where it goes** — the address sits in plain text in the registered hook, not
  hidden inside a downloaded binary.
- **Not user-configurable** — the package sets the value; the binary does not read an env
  override for the endpoint. Installing the package is consent to send to this one place.

The concrete host value is an infrastructure concern and is not chosen here (scope boundary).
The protocol is OTLP/HTTP (simpler for a short-lived CLI and friendlier to gateways and
proxies than gRPC). The access token is supplied separately (see "Open questions"), not
through this flag.

Update: the endpoint is no longer a hook flag. It is delivered per machine through the
provisioned config file as `SKILLS_TELEMETRY_ENDPOINT`, mirroring the token. The hook command
carries no endpoint. See "Provisioning".

## Transport security

The sender never emits telemetry over plaintext. The endpoint is always `https://`, and
the sender does not set `OTEL_EXPORTER_OTLP_INSECURE` or skip certificate verification. If
the collector cannot be reached over TLS, the send fails and the event stays in the spool —
it is not downgraded to cleartext, so the access token and the event never travel unencrypted.

CA trust is hybrid, resolved in the sender's own code:

1. If a CA certificate exists at the well-known path `<config>/ca.crt`, the sender adds it to
   the system trust pool (`x509.SystemCertPool` + `AppendCertsFromPEM`) and passes the combined
   pool through `otlploghttp.WithTLSClientConfig`.
2. Otherwise the sender uses the system trust store alone.
3. If neither validates the collector's certificate, the send fails.

The CA file is therefore optional, and trust is additive — the machine keeps trusting public
and corporate roots and adds the private CA on top. A deployment whose collector presents a
publicly trusted certificate, or one signed by a corporate CA already in the machine's trust
store (for example pushed through MDM), needs no CA file at all. The CA file stays for local
development against a self-signed collector, and for any deployment that does not issue a
trusted certificate.

## Provisioning

The sender needs three things per machine that cannot ship in the public package because they
are deployment-specific or secret: the collector endpoint, the access token, and — for a
self-signed collector — the CA certificate. A `provision` step sets them once per machine,
separate from the per-repository `apm install`.

### Mechanic

The deterministic core is `provision`, a subcommand of the sender binary
(`skills-telemetry provision`). It performs the security-sensitive work — atomic writes of the
`env` file and `ca.crt` with the right permissions, validation, and idempotency — and reads the
token with no echo (`golang.org/x/term`) so the secret never reaches a terminal scrollback or a
caller's context. There is no parallel `provision.sh` / `provision.ps1` to keep in sync.

Two front-ends produce the same files through that core:

1. **A provisioning skill (primary, agent-driven).** Because the project already runs inside a
   skill-capable harness, a provisioning skill orchestrates onboarding in natural language: it
   discovers the endpoint, extracts or locates the CA, asks the user for anything missing, calls
   `skills-telemetry provision` for the actual writes, and finishes with `skills-telemetry
   status` to verify. The model handles discovery and interaction; the binary handles the
   correct write and the verification. The token is the exception — the skill defers it to the
   binary's no-echo prompt, so it does not pass through the model's context.
2. **A `curl | sh` one-liner (headless / CI).** For non-agent contexts, a one-liner reuses
   `bootstrap.sh` as the binary fetcher so provisioning does not wait for the first skill event:

   ```
   # macOS / Linux
   curl -fsSL <url>/bootstrap.sh | sh -s -- provision
   # Windows
   iex "& { $(irm <url>/bootstrap.ps1) } provision"
   ```

   `bootstrap` ensures the binary is in the cache (the same logic and pinned version it uses for
   events, so both share one cache entry) and runs `skills-telemetry provision`. Because stdin
   is the curl pipe, the subcommand reads its prompts from the controlling terminal (`/dev/tty`
   on Unix, the console on Windows). It also accepts flags for fully non-interactive runs.

### The CA certificate

The sender locates the CA by auto-discovery: it loads `<config>/ca.crt` if the file exists and
adds it to the system trust pool (see "Transport security"). Provisioning therefore only has to
place that file. `provision` takes a local path and copies the certificate to the canonical
`<config>/ca.crt`; it does not fetch the certificate itself.

Where the source file comes from depends on the deployment, not on `provision`:

- A publicly trusted certificate, or a corporate CA already in the system trust store (for
  example via MDM), needs no file at all — auto-discovery finds nothing and the system store
  validates the collector.
- A corporate CA that is not in the trust store is downloaded by the user from the internal
  distribution point, then passed to `provision`.
- The local self-signed stand's CA is extracted from the cluster with a documented command; the
  provisioning skill can run that extraction. `provision` stays deployment-agnostic and only
  copies the resulting file.

### File layout

The split already in the code is kept: disposable data in the cache, durable data in the config
directory.

| Data | Location | Lifetime |
|---|---|---|
| Binary, spool | `os.UserCacheDir()/qubership-skills-telemetry/` | Disposable — re-fetched or rebuilt |
| `env` (endpoint, token), CA certificate, `machine.id` | `os.UserConfigDir()/qubership-skills-telemetry/` | Durable — survives a cache wipe |

The config directory resolves to `~/Library/Application Support/qubership-skills-telemetry/` on
macOS, `~/.config/qubership-skills-telemetry/` on Linux, and
`%AppData%\qubership-skills-telemetry\` on Windows. Provisioning state must not live in the
cache: the cache is disposable by design — the binary is there because it re-downloads — and
macOS purges `~/Library/Caches` under disk pressure, which would silently drop the token and
endpoint and stop telemetry.

The config file is `KEY=VALUE`, parsed (not sourced) by both bootstraps so the format is
identical on every OS: `bootstrap.sh` reads and exports each line, `bootstrap.ps1` sets the
matching `$env:` variables.

### Order and discoverability

Provisioning (once per machine) and `apm install` / `apm compile` (per repository) are
independent. Either order works: an unprovisioned send is a no-op (the endpoint is empty) and
the event waits in the spool. The onboarding document lists provisioning — the skill, or the
one-liner for headless contexts — as step 1 and `apm install` / `apm compile` as step 2.
`skills-telemetry status` reports whether the machine is provisioned and prints the next step
when it is not.

## Build and release

- Cross-compile for darwin/linux/windows × amd64/arm64; binaries built with
  `-ldflags="-s -w"`.
- Artifacts and checksums are published in a release; the version is pinned by the package
  and verified by the bootstrap on first run.

## Error handling

- Any failure to parse, write, or send **must not crash the agent turn**: the binary logs to
  stderr and exits without blocking the harness.
- Flush has a hard short timeout; anything undelivered stays in the buffer until next time.
- Buffer overflow is not an error but normal rotation (drop oldest).

## Testing

- **Adapters:** table-driven tests on fixed input samples for each harness → the expected
  `SkillEvent`. Port the cases from the current `detect_skill_call.py` checks.
- **Buffer/concurrency:** run many `ingest` processes against one spool in parallel — check
  for the absence of corruption and the correctness of rotation and the cap.
- **Flush:** a mock OTLP receiver; check batching, the timeout, keeping the buffer on
  failure, and serialization through the lock.
- **Bootstrap:** cache resolution and checksum verification on each OS; behavior with a
  missing binary, a broken checksum, and an already-cached correct version.

## Open questions (outside this design)

- Gateway authentication and delivering the token to the machine outside git — see the
  "Open points" section in `README.md`. The binary reads the token from an environment
  variable or a local secret file; the issuance mechanism is decided separately.
- The concrete thresholds `N` (event count) and `T` (interval) for the opportunistic flush —
  tuned during implementation.
