# On-`PATH` binary lifecycle — installer hardening, update-check, and two robustness fixes

**Date:** 2026-06-23
**Status:** accepted — builds on the direction set in
[2026-06-22-windows-hook-shell-and-paths.md](2026-06-22-windows-hook-shell-and-paths.md)
("drop the hook-invokes-a-script model; put the binary on `PATH` for every OS and call it by its
bare name"). That decision chose the model; this one records the four follow-up choices the model
forced. User-facing rationale lives in the maintained docs ([docs/cli.md](../../cli.md),
[README.md](../../../README.md), [docs/design-decisions.md](../../design-decisions.md)); this file
keeps the per-problem "why" for context recovery.

Once the bare-name hook calls a real binary on `PATH`, four gaps opened that the old
locate-or-download bootstrap wrapper had not faced. Each is recorded below as problem → options →
decision.

## 1. The installer downloads an executable — how is it trusted?

**Problem.** The bootstrap script is now an *installer*: it downloads the release binary into
`~/.local/bin` once and wires `PATH`, instead of locating-or-downloading on every hook call. A
download that lands on `PATH` and runs on every agent turn is a supply-chain surface — a corrupted
or tampered asset would execute unchecked. The old wrapper verified nothing.

**Options.**

- **Verify a published checksum.** Download `SHA256SUMS` from the same release, match the asset's
  hash, install only on a match. No new key material; rides the existing release assets.
- **Sign the binary and verify a signature.** Stronger (authenticity, not just integrity), but
  needs a signing key, a published public key, and per-OS verification tooling — heavier than the
  threat warrants for a single-maintainer release.
- **Trust the TLS transport alone.** What the old wrapper did. Catches nothing once the asset
  itself is wrong at the source.

**Decision.** Verify the SHA-256 checksum against the release's `SHA256SUMS` before installing; a
mismatch removes the temp file and aborts. On a host with no SHA-256 tool the POSIX installer
*warns and proceeds* rather than blocking install on a missing utility — integrity is best-effort,
availability is not sacrificed. Both installers also gain `--force`, which re-downloads over an
existing binary (the update path; see §2). Implemented in
[bootstrap.sh](../../../agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh)
and
[bootstrap.ps1](../../../agent-packages/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.ps1).

## 2. The binary no longer self-updates — how does a machine learn it is stale?

**Problem.** The retired wrapper pinned a release tag and re-downloaded into a per-version cache, so
bumping the tag effectively "updated" the install. The on-`PATH` binary is installed once and never
re-checked; a machine can silently run an old build indefinitely.

**Options.**

- **Auto-update in the hook path.** Convenient, but puts a network fetch and a binary swap on the
  hot path that must never fail an agent turn — the opposite of the install-once model. Rejected.
- **An advisory `update-check` subcommand.** Compares the installed version against the latest
  GitHub release and prints a verdict; applying the update stays a separate, explicit installer
  re-run. The skill calls it and offers the update with consent.
- **Nothing — rely on package managers later.** The 2026-06-22 decision names package managers
  (winget/scoop/Homebrew/apt) as the eventual distribution path, which would carry updates. True,
  but that work is not done, so machines would stay stale until then.

**Decision.** Add `update-check`: network, short timeout, **always exits 0** (an advisory check must
never become a reason a caller fails), printing stable `installed:` / `latest:` /
`update_available: yes|no|unknown` lines a skill or hook can grep without parsing prose. A fetch
error yields `unknown`, never a crash. Applying an update is re-running the latest installer with
`--force` (§1). The version comparison is numeric MAJOR.MINOR.PATCH with pre-release/build suffixes
stripped, so a `-dev` build never spuriously reports an update. Implemented in
[update.go](../../../update.go); documented under "Updating" in [docs/cli.md](../../cli.md).
Auto-triggering the check on a cadence is explicitly out of scope and not wired.

## 3. Telemetry could not tell the host OS apart

**Problem.** Each skill-run log record carried `service.name` / `service.version` but nothing about
the producing platform, so dashboards could not split skill usage by OS — a basic cut for a
cross-platform tool.

**Options.**

- **A custom `host.os` attribute.** Works, but invents a key where a standard one exists.
- **The OpenTelemetry `os.type` semantic-convention attribute.** `runtime.GOOS` already emits
  exactly the convention's values (`windows`, `linux`, `darwin`), so it maps for free.

**Decision.** Add `os.type` from `runtime.GOOS` to the OTLP **resource** attributes (it describes
the producing machine, so it belongs on the resource, not on each per-event record), alongside the
anonymous `machine.id`. Implemented in [flush.go](../../../flush.go) (`resourceAttrs`); listed in
the event schema in [README.md](../../../README.md). This extends the schema first recorded in
[2026-06-12-event-schema-and-privacy.md](2026-06-12-event-schema-and-privacy.md) and carries no new
personal data.

## 4. Cursor on Windows piped a UTF-8 BOM into the hook

**Problem.** Cursor runs project hooks on Windows through PowerShell 5.1, piping the hook payload in
via `Get-Content … | cmd` (established in
[2026-06-22-windows-hook-shell-and-paths.md](2026-06-22-windows-hook-shell-and-paths.md)). PowerShell
5.1 prepends a UTF-8 byte-order mark to that piped stream. Those three bytes are not valid JSON, so
`json.Unmarshal` fails and the event is silently dropped — a hook that ran and detected nothing.

**Options.**

- **Strip a leading BOM in the detector.** One `bytes.TrimPrefix` at the entry point, before
  routing to any per-harness adapter. Covers every current and future caller.
- **Fix the hook command to suppress the BOM.** Out of reach: the shell and its piping are Cursor's,
  not ours, which is the same constraint that drove the bare-name model.

**Decision.** Strip a single leading UTF-8 BOM in `detect` before routing
([detect.go](../../../detect.go)). It is harmless for callers that do not send one and fixes the
silent Cursor-on-Windows drop. This pairs with the Codex `shell_command` tool shape the same
session added to the parser (point 10 of the 2026-06-22 decision: "retain the existing shapes and
add the new one"); both are detection-robustness fixes, not model changes.
