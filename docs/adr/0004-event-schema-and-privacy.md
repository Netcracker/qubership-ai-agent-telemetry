# Event schema: minimum data and no personal information

## Status

Accepted

**Date:** 2026-07-09

**Last updated:** 2026-08-13

**Owner:** denifilatoff

**Participants and approvers:** Denis Filatov (@denifilatoff)

**Related ADRs:**

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
identifies the user or the hardware. The full field list is in the README "Data" section; this ADR records the
privacy boundaries and the design choices behind them.

Each delivered OpenTelemetry log record has body `skill_executed` and these log attributes:

- `event.id`
- `agent`
- `session.id`
- `repo.remote`
- `skill.name`

The process also emits these resource attributes:

- `service.name`
- `service.version`
- `os.type`
- `machine.id`, when the CLI can read or create the anonymous install ID

The outbox persists the same event content in local JSON files before flush. It stores `event.id` as `event_id` when
the event is enqueued. Local-only helper fields, such as the working directory used for repository policy checks, are
never serialized.

### Fields excluded

| Field | Why excluded |
| --- | --- |
| `repo.path` | The local working directory leaks the username. Repositories are identified by `repo.remote` alone. A non-git checkout has no repo label unless policy can resolve an allowed remote from the working tree. |
| `turn.id` | Finer than `session.id` and supplied by the harness. Delivery deduplication uses the CLI-generated `event.id` instead. |
| `user_email` | Cursor's hook payload carries it. We do not read it — skill-usage counts do not require user identity, and collecting email would cross the "no personal data" line. |
| `skill.source` | Originally carried by the `[skill-called]` marker; retired when the marker was removed (see [ADR 0001](0001-skill-detection-via-hooks-and-transcripts.md)). |

### `repo.remote`: normalized repository identity

`repo.remote` is the only repository label that leaves the machine. Before an event is sent, the CLI normalizes git
remote URLs into lowercase identities such as `github.com/netcracker/project`, strips a trailing `.git`, and removes
URL userinfo such as usernames, passwords, or OAuth tokens.

When the hook runs in a personal fork, repository policy checks every configured git remote in the working tree. If an
allowed upstream remote matches, the event records the allowed organization remote instead of the personal fork remote.

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

### `event.id`: stable delivery identity

The CLI generates a UUID v7 each time it enqueues an event and persists it with the event. Every delivery attempt for
that outbox file carries the same `event.id`. Different events receive different identifiers. UUID v7 embeds the event
time in milliseconds, which makes identifiers time-sortable without adding a second source of user data.

The random portion is generated locally with `crypto/rand`. The identifier contains no user, machine, repository,
session, or event payload data. The CLI validates persisted identifiers before export. An outbox entry created by an
older version, or one containing a malformed identifier, receives a deterministic UUID v7 fallback. The fallback uses
the persisted event timestamp and derives its random portion only from the opaque outbox filename. This keeps retries
stable without transmitting arbitrary stored content.

`event.id` provides a key that a backend or query can use for deduplication. VictoriaLogs does not automatically
deduplicate log records by this attribute, so duplicate records remain possible until the backend or analytics query
uses `event.id` explicitly.

### Justification

The guiding principle is **no personal data leaves the machine**. A repository is identified by its normalized remote
identity (public information for public repos; for private repos, the URL alone does not grant access). The install is
identified by a random UUID that cannot be traced back to a person or a device. Everything else the CLI processes
(local paths, transcript content, user email) stays in-process and is never serialized into the outbox.

This is a deliberate minimum, not a starting point to extend. Adding a field that crosses the personal-data
line requires a new ADR.

## Consequences

- **Repos without an allowed remote are dropped unless an explicit path rule authorizes collection.** A non-git checkout
  or one with no matching remote produces an empty repository identity and is filtered when only the default
  `github.com/Netcracker/*,*netcracker*/**` repository allowlist applies. The host wildcard avoids publishing a specific
  corporate host, but it can also match an unrelated host with the same substring. A deliberately unscoped repository
  policy or an explicit `path-allow` match can retain an event with an empty `repo.remote`; the local path remains
  local-only policy input.
- **No per-user analytics.** Without `user_email` or any user identifier, the backend cannot break down
  usage by person. This is intentional: the metric is "which skills are used, where," not "who uses them."
- **`machine.id` resets on re-configure.** Deleting the config directory (or reconfiguring onto a new
  XDG path, per [ADR 0003](0003-config-cache-dirs-xdg.md)) mints a new UUID. The backend sees a "new"
  install. This is a minor analytics discontinuity, not a correctness issue.
- **Retries can be identified but are not removed automatically.** A retry carries the same `event.id`, but storage
  and queries must use that attribute to collapse duplicates.
