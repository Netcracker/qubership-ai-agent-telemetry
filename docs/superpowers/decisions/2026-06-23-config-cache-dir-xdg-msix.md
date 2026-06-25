# Config and cache dirs are uniform XDG paths, not `os.UserConfigDir()` — MSIX virtualizes `%AppData%`

**Date:** 2026-06-23
**Status:** accepted — supersedes the path choice in
[2026-06-15-provisioning-and-paths.md](2026-06-15-provisioning-and-paths.md) (the "Config base
directory = `os.UserConfigDir()` on every OS" row) and the layout in
[2026-06-12-local-telemetry-sender-design.md](../specs/2026-06-12-local-telemetry-sender-design.md).

## The problem

`skills-telemetry` stored durable config via `os.UserConfigDir()` and the spool via
`os.UserCacheDir()`. On Windows those are `%AppData%\Roaming` and `%LocalAppData%`. **MSIX
(Windows Store / Desktop Bridge) packaged apps get their `AppData` virtualized** — writes are
redirected into the package's private `…\Packages\<family>\LocalCache\` tree.

Claude Desktop on Windows is exactly such a packaged app
(`CLAUDE_CODE_ENTRYPOINT=claude-desktop`, executing under
`…\AppData\Local\Packages\Claude_pzs8sxrjxfjjc\…`). So the config dir the binary resolved
**depended on which harness launched it**:

- Run from Claude Desktop → config under `…\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Roaming\qubership-skills-telemetry\`.
- Run from Codex, Cursor, or a plain terminal (non-packaged) → config under the real
  `%APPDATA%\Roaming\qubership-skills-telemetry\`.

**The user-visible pain:** the `provision-skills-telemetry` skill, run inside Claude Desktop,
wrote config into the packaged layer and reported "provisioned / selftest ok" — while every
*other* harness read the real `AppData`, found no config, and silently sent nothing. The check
said everything was fine; telemetry only worked for Claude Desktop. The two contexts even
minted **different anonymous `machine-id`s**, so they looked like two installs.

## Verified MSIX behaviour (den-win, 2026-06-23)

- A file written from the Claude Desktop context to `AppData\Roaming\qubership-skills-telemetry\env`
  physically lands in `…\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Roaming\…`. A plain Git Bash
  shell sees only the real `AppData\Roaming`.
- MSIX uses a **merge with fall-through**: a file that exists *only* in the real `AppData`
  (absent from the package layer) **is** visible to the packaged binary. But if the same file
  exists in the package layer too, the **package copy shadows** the real one.
- The binary at `~/.local/bin/skills-telemetry.exe` is **not** redirected — it lives outside
  `AppData`. One shared binary serves all harnesses; only the config/cache dirs were virtualized.

## Decision

Resolve both roots to **uniform XDG-style paths on every OS**, the same philosophy the binary
already uses for `~/.local/bin`:

- **Config:** `$XDG_CONFIG_HOME` else `~/.config/qubership-skills-telemetry/`
- **Cache:** `$XDG_CACHE_HOME` else `~/.cache/qubership-skills-telemetry/`

On Windows these are `%USERPROFILE%\.config\…` and `%USERPROFILE%\.cache\…` — **outside
`AppData`, so MSIX never virtualizes them**, and every harness (packaged or not) shares one
config. On Linux they are identical to what `os.UserConfigDir()` / `os.UserCacheDir()` already
returned, so nothing changes there. On macOS config moves out of `~/Library/Application
Support` into `~/.config` — accepted as the price of one cross-platform path.

Implemented as small testable resolvers: `configBase` / `configBaseFrom` in
[config.go](../../../config.go) (used by `pkgConfigDir`, `resolveToken`, `resolveMachineID`) and
`cacheBase` / `cacheBaseFrom` in [outbox.go](../../../outbox.go) (used by `DefaultOutbox`,
`DefaultOffsetStore`). An explicit `$XDG_*` value wins; otherwise fall back to `~/.config` /
`~/.cache`. This also fixes test isolation on Windows, where `os.UserConfigDir()` /
`os.UserCacheDir()` ignored the `XDG_*` env vars the tests set.

## Alternatives rejected

- **Windows-only override** (keep `os.UserConfigDir()` on macOS/Linux, redirect only Windows
  out of `AppData`). Fixes the MSIX bug but leaves three different per-OS paths — the opposite of
  the uniform model the binary already chose. Rejected in favour of one path everywhere.
- **Auto-migrate in the binary** (copy the legacy `AppData`/`Library` config into `~/.config` on
  first run). Convenient, but adds path-resolution side effects and clobber/race edge cases to a
  hot hook path, and has to guess which of two diverged Windows copies to keep. Decided migration
  is a provisioning concern, documented in the skill, not binary behaviour.
- **Force XDG `~/.config` on macOS** was explicitly rejected back in the 2026-06-15 decision for
  needing a custom resolver. That trade-off is now reversed on purpose: the custom resolver is
  cheap, and one uniform path is worth it.

## Consequences

- **No auto-migration.** After an upgrade an existing machine finds an empty new config dir and
  `status` reports `not provisioned` until re-provisioned. The `provision-skills-telemetry`
  skill documents the move (re-provision into `~/.config`; optionally copy `machine-id`/`env`/
  `ca.crt`; on Windows delete the stale real-`AppData` and package-layer copies so they don't
  shadow or mislead).
- **`status` is honest for free** — it prints the real `~/.config` path, identical in every
  context, so the misleading `AppData\Roaming` report is gone.
- **Spool moves too** (`~/.cache`); a few buffered, unsent events in the old cache are abandoned
  on upgrade — acceptable, the spool is disposable by design.
- `os.UserConfigDir()` / `os.UserCacheDir()` are no longer used anywhere in the binary.
