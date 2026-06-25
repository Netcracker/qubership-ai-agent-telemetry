# Event schema: minimum data and no personal information

## Status
Proposed
#### Date

#### Owner
denifilatoff
#### Participants and approvers
Denis Filatov (@denifilatoff)
#### Related ADRs
- [0001-skill-detection-via-hooks-and-transcripts.md](0001-skill-detection-via-hooks-and-transcripts.md) —
  `skill.source` was retired along with the marker

## Context

The first developer end-to-end run (2026-06-12, Codex harness) delivered a working event to the collector, but
the record carried a local filesystem path (`/Users/<username>/Repos/…`) that identified the developer by
name. The run also emitted a `turn.id` that was finer-grained than the session with no analytic use, and had no
stable way to tell installs apart.

The schema needed a privacy contract: what leaves the machine, what does not, and why.

Separately, Cursor's hook payload offers a `user_email` field. The question was whether to collect it.

## Decision

We will send the minimum set of fields needed for skill-usage analytics and deliberately exclude anything that
identifies the user or the machine. The full field list is in the README "Data" section; this ADR records only
the exclusions and the design choices behind them.

### Fields excluded

| Field | Why excluded |
|---|---|
| `repo.path` | The local working directory leaks the username. Repositories are identified by `repo.remote` alone. A non-git checkout has no repo label — accepted as a rare edge case not worth leaking a path for. |
| `turn.id` | Finer than `session.id` with no analytic value. The only theoretical use (server-side dedup of duplicate hook fires) was not needed. |
| `user_email` | Cursor's hook payload carries it. We do not read it — skill-usage counts do not require user identity, and collecting email would cross the "no personal data" line. |
| `skill.source` | Originally carried by the `[skill-called]` marker; retired when the marker was removed (see [ADR 0001](0001-skill-detection-via-hooks-and-transcripts.md)). |

### `machine.id`: anonymous install identity

The collector needs to tell installs apart (for example, to spot one install skewing counts), but the
identifier must not fingerprint the user or the hardware.

- **Source:** a random UUID v4 generated with `crypto/rand` on first run, stored at
  `<config>/machine-id` (mode 0600). The user can delete the file to reset it.
- **Not hardware-derived.** Hashing the OS or hardware machine ID was rejected: it collapses every account
  on a shared machine into one identifier and reads as a device fingerprint.
- **Custom key, not OTel `host.id`.** The OTel `host.id` semantic convention means the real hardware or
  cloud-instance ID — a semantic mismatch for a random UUID. `device.id` targets mobile and client apps.
  A custom `machine.id` names the concept without overloading a standard key.

### Justification

The guiding principle is **no personal data leaves the machine**. A repository is identified by its remote
URL (public information for public repos; for private repos, the URL alone does not grant access). The
install is identified by a random UUID that cannot be traced back to a person or a device. Everything else
the CLI processes (local paths, transcript content, user email) stays in-process and is never serialized
into the outbox.

This is a deliberate minimum, not a starting point to extend. Adding a field that crosses the personal-data
line requires a new ADR.

## Consequences

- **Repos without a remote are invisible.** A non-git checkout or one with no `origin` produces an event
  with an empty `repo.remote`. This is accepted — the alternative is leaking the local path.
- **No per-user analytics.** Without `user_email` or any user identifier, the backend cannot break down
  usage by person. This is intentional: the metric is "which skills are used, where," not "who uses them."
- **`machine.id` resets on re-configure.** Deleting the config directory (or reconfiguring onto a new
  XDG path, per [ADR 0003](0003-config-cache-dirs-xdg.md)) mints a new UUID. The backend sees a "new"
  install. This is a minor analytics discontinuity, not a correctness issue.
