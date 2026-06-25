# Config and cache directories use uniform XDG paths

## Status
Accepted
#### Date
2026-06-23
#### Owner
denifilatoff
#### Participants and approvers
Denis Filatov (@denifilatoff)
#### Related ADRs
- [0002-bare-binary-on-path.md](0002-bare-binary-on-path.md) — the binary lives at `~/.local/bin`, already
  outside `AppData`

## Context

The CLI originally resolved its config and cache directories through Go's `os.UserConfigDir()` and
`os.UserCacheDir()`. On each OS these returned:

| OS | Config | Cache |
|---|---|---|
| macOS | `~/Library/Application Support/` | `~/Library/Caches/` |
| Linux | `~/.config/` | `~/.cache/` |
| Windows | `%AppData%\Roaming\` | `%LocalAppData%\` |

This worked until the CLI ran under **Claude Desktop on Windows**. Claude Desktop is distributed as an MSIX
package (Windows Store / Desktop Bridge). MSIX-packaged applications get their `AppData` directories
**virtualized**: writes to `%AppData%` are silently redirected into the package's private tree at
`…\AppData\Local\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Roaming\`.

The result was that the config directory the CLI resolved **depended on which harness launched it**:

- **Claude Desktop** (MSIX-packaged) wrote config into the virtualized
  `…\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Roaming\qubership-ai-agent-telemetry\`.
- **Codex, Cursor, and a plain terminal** (non-packaged) read the real
  `%AppData%\Roaming\qubership-ai-agent-telemetry\`.

The user-visible failure: the `ai-agent-telemetry-configure` skill, run inside Claude Desktop, wrote config into
the virtualized layer and reported "configured / selftest ok." Every other harness read the real `AppData`,
found no config, and silently sent nothing. Telemetry appeared to work from Claude Desktop but was dead
everywhere else. The two contexts even minted **different `machine-id` files**, so they looked like two separate
installs in the backend.

Verified behavior on a Windows test machine (2026-06-23):

- A file written from the Claude Desktop context to `AppData\Roaming\qubership-ai-agent-telemetry\env`
  physically landed in `…\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Roaming\…`. A plain Git Bash shell saw
  only the real `AppData\Roaming\`.
- MSIX uses a merge-with-fall-through model: a file that exists only in the real `AppData` is visible to the
  packaged binary, but if the same file exists in the package layer too, the package copy shadows the real one.
- The binary at `~/.local/bin/ai-agent-telemetry.exe` was not redirected — it lives outside `AppData`.

The original design (decided on
[2026-06-15](../superpowers/decisions/2026-06-15-provisioning-and-paths.md)) explicitly rejected `~/.config` on
macOS because it diverged from `os.UserConfigDir()` and required a custom path resolver. That tradeoff was
reversed by this decision.

The full analysis is in
[the config-dir decision](../superpowers/decisions/2026-06-23-config-cache-dir-xdg-msix.md).

## Decision

We will resolve both directories to **uniform XDG-style paths on every OS**:

- **Config:** `$XDG_CONFIG_HOME` else `~/.config/ai-agent-telemetry/`
- **Cache:** `$XDG_CACHE_HOME` else `~/.cache/ai-agent-telemetry/`

On Windows these are `%USERPROFILE%\.config\…` and `%USERPROFILE%\.cache\…` — outside `AppData`, so MSIX never
virtualizes them. On Linux the paths are identical to what `os.UserConfigDir()` and `os.UserCacheDir()` already
returned. On macOS, config moves from `~/Library/Application Support/` into `~/.config/`.

This mirrors the binary's own location at `~/.local/bin/` — a home-relative path outside any OS-managed
application directory.

Implemented as `configBase` / `configBaseFrom` in `config.go` and `cacheBase` / `cacheBaseFrom` in `outbox.go`.
An explicit `$XDG_CONFIG_HOME` or `$XDG_CACHE_HOME` value wins; otherwise the fallback is `~/.config` or
`~/.cache`.

### Justification

Three alternatives were considered:

1. **Windows-only override** — keep `os.UserConfigDir()` on macOS and Linux, redirect only Windows out of
   `AppData`. This fixes the MSIX bug but leaves three different per-OS path schemes. Rejected in favor of one
   uniform path everywhere.
2. **Auto-migrate in the binary** — copy the old `AppData` or `~/Library` config into `~/.config` on first
   run. Convenient, but adds path-resolution side effects and race conditions to a hot hook path. On Windows
   there are two diverged copies (real `AppData` and virtualized package layer) and the binary would have to
   guess which to keep. Rejected — adds complexity for a one-time edge case.
3. **Keep `os.UserConfigDir()` everywhere and document the MSIX limitation.** The limitation is invisible: the
   user configures successfully from Claude Desktop, sees "ok," and has no reason to suspect other harnesses
   are broken. Documentation does not fix an invisible problem.

The XDG-style path was originally rejected on 2026-06-15 because it required a custom resolver. That tradeoff
was reversed: the resolver is a few lines of Go, and one uniform path across all OSes and all harnesses is
worth it. Other CLI tools installed into `~/.local/bin` already use `~/.config` on macOS (for example,
Homebrew-installed tools that follow the XDG convention), so the path is not alien to macOS users.

## Consequences

- **`status` is honest.** The printed config path (`~/.config/ai-agent-telemetry/`) is the same in
  every context — Claude Desktop, Codex, Cursor, plain terminal. The misleading `AppData\Roaming` report is
  gone.
- **Spool moves to `~/.cache`.** A few buffered, unsent events in the old cache directory are abandoned on
  upgrade. This is acceptable — the spool is disposable by design and re-fills on the next agent turn.
- **`os.UserConfigDir()` and `os.UserCacheDir()` are no longer used anywhere** in the binary. Test isolation
  on Windows also improves: the old stdlib calls ignored the `XDG_*` environment variables that tests set.
