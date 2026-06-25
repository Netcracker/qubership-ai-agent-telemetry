# Windows command hooks run under Git Bash — use forward-slash paths

**Date:** 2026-06-22
**Ref:** [Claude Code hooks docs](https://code.claude.com/docs/en/hooks)
**Status:** superseded — the forward-slash fix below is a Claude-only stopgap; the
script-in-hook model is being set aside in favour of putting the binary on `PATH` for all OSes.
See **Bottom line** at the end.

## What breaks

On Windows the `sh ./scripts/bootstrap.sh ingest --agent=X || <harness>\hooks\...\bootstrap.bat
ingest --agent=X` hook never delivered telemetry on real skill runs. The `.bat` fallback path
used **backslashes**, which the executing shell strips, so the fallback fails with
`bootstrap.bat: command not found` (exit 127) and nothing is sent. The bootstrap binary,
`bootstrap.ps1`/`.bat`, and `selftest` are all fine — only the wiring was wrong.

## Root cause — the hook shell

Claude Code runs `"type":"command"` hooks on Windows through **Git Bash (`bash`) by default**, not
cmd.exe and not PowerShell ([hooks docs](https://code.claude.com/docs/en/hooks)). Consequences:
- **cmd.exe** is ruled out — backslash paths are native there, so the fallback would have worked.
- **PowerShell** is ruled out (and must not be selected): PS 5.1 treats `||` as a parse error
  (only PS 7+ supports it), which would break the whole `sh … || …` design.
- **Git Bash**: `||` works, but `\` in the `.bat` path is stripped → the observed failure.

The shell is per-hook configurable via a `"shell"` field (`"bash"` default | `"powershell"`); our
templates don't set it, so bash applies.

## Fix

Use **forward slashes** for the `.bat` fallback path in all three templates
(`agent-packages/qubership-skills-telemetry/.apm/hooks/skill-call-{claude,codex,cursor}-hooks.json`):
`… || .claude/hooks/qubership-skills-telemetry/scripts/bootstrap.bat ingest --agent=claude`. Git
Bash executes the `.bat` directly via a forward-slash path (verified: a real Skill event flushed,
`last_flush_attempt` advanced, `buffered` stayed 0). Keep `"shell"` unset (stay on bash).

**Alternative (untested):** the `"shell"` field also accepts `"powershell"`, which would run the
hook under PowerShell instead of Git Bash. That would sidestep the backslash-stripping, but the
command must then be rewritten in PowerShell syntax — `||` is a PS 5.1 parse error, so it would
need `;` / `if` logic instead of `sh … || …bat`. We did **not** try this path; the bash + forward-
slash fix above is the verified one.

## Verify (fresh session — mid-session `settings.json` hook edits are not armed)

`bootstrap.ps1 status` → note `last_flush_attempt` → invoke any skill → `status` again: the
timestamp should advance and `buffered` stay 0.

## Conclusions (Cursor)

**Ref:** [Cursor hooks docs](https://cursor.com/docs/agent/hooks)

Those docs do not name the hook shell. Inspecting the installed app (`extensionHostProcess.js`,
`$executeHookDirect` → `i0r()` → `powershell.exe` with `-NoProfile -NonInteractive
-ExecutionPolicy Bypass -c`) shows project hooks on Windows run through **PowerShell 5.1**, with
hook JSON written to a temp file and piped in via `Get-Content`. Reproduced locally: the current
`skill-call-cursor-hooks.json` command (`sh … || …bootstrap.bat`) fails on PS 5.1 with
`The token '||' is not a valid statement separator in this version` — parse error before any
script runs. Forward-slash paths do not help here; the blocker is the shell, not backslashes.

## Conclusions (Codex Desktop on Windows)

1. Codex selects `commandWindows` when present; the generic `command` is not run on Windows.
2. The observed process chain is `Codex.exe -> codex.exe -> powershell.exe -> cmd.exe /c -> .bat`.
3. Therefore a `.bat` named by `commandWindows` executes under `cmd.exe`, with PowerShell as its parent launcher.
4. Without `commandWindows`, Codex passes the generic command to PowerShell 5.1; `sh ... || ...` fails at parse time.
5. The Codex template must keep POSIX `command` and add a separate `commandWindows` invoking `bootstrap.bat`.
6. Codex supports `commandWindows`; whether APM preserves and installs this field still needs an install/merge test.
7. The tool sandbox and the real host-side Stop hook both resolved the config path but reported endpoint `(unset)`.
8. Claude's Git Bash saw `env` at that same path and passed `selftest`, so cross-harness config visibility remains open.
9. Current Codex Desktop rollouts use `custom_tool_call` / `exec` / string `input` for tool orchestration.
10. The parser must retain `function_call` / `exec_command` support and add the new shape without replacing it.

## APM does not install `commandWindows` scripts — bug-report draft

**Closes the open question in point 6 above.** Verified by a clean-room `apm install` (APM CLI
v0.21.0) of the `qubership-skills-telemetry` package on Windows. APM **preserves** the
`commandWindows` field in the generated `.codex/hooks.json` but does **not** deploy the script it
references nor rewrite its path — so a from-scratch install produces a Codex Windows hook that
calls a non-existent `bootstrap.bat`.

### Summary

Codex command hooks use `commandWindows` as a Windows-only override of `command`
([Codex hooks docs](https://developers.openai.com/codex/hooks)). APM is unaware of this key:
the string `commandWindows` / `command_windows` does not appear anywhere in the APM source. The
field rides through to the output JSON only because the rewriter deep-copies the hook structure,
so unknown keys pass untouched. The script it names is never copied into the deploy tree.

### Reproduce

1. Package template `skill-call-codex-hooks.json` with a Stop hook carrying both keys:
   - `"command": "sh ./scripts/bootstrap.sh ingest --agent=codex"`
   - `"commandWindows": ".codex\\hooks\\qubership-skills-telemetry\\scripts\\bootstrap.bat ingest --agent=codex"`
   - package `scripts/` dir contains `bootstrap.sh`, `bootstrap.ps1`, `bootstrap.bat`.
2. `apm install <package> --target codex` (v0.21 needs `--target`, not `--runtime`, in a project
   with no harness markers), then `apm compile`.
3. Inspect the generated tree.

### Expected vs actual

| | Expected | Actual |
|---|---|---|
| `command` path | rewritten to deploy path | ✅ `sh .codex/hooks/<pkg>/scripts/bootstrap.sh …` |
| `commandWindows` field | present | ✅ present (verbatim) |
| `commandWindows` path | rewritten to deploy path | ❌ passed through verbatim |
| `bootstrap.bat` deployed | yes | ❌ **only `bootstrap.sh` lands in `.codex/hooks/<pkg>/scripts/`** |
| `bootstrap.ps1` deployed | yes | ❌ never deployed (unreferenced) |

Result: on Windows Codex runs `commandWindows`, which points at a `bootstrap.bat` that does not
exist in the deploy tree → hook fails, no telemetry. Confirmed independent of the local-folder
naming artifact: installing from a dir named exactly `qubership-skills-telemetry` (so the
`commandWindows` hardcoded path matches the deploy dir) still leaves the `.bat` absent
(`Test-Path … bootstrap.bat = False`).

### Root cause (APM source — `src/apm_cli/integration/hook_integrator.py`)

A script is deployed **only** when it is referenced (a) under a key in the `HOOK_COMMAND_KEYS`
allow-list **and** (b) as a package-relative path (`./…`, `.\…`, or `${…_PLUGIN_ROOT}/…`).

- `HOOK_COMMAND_KEYS` (lines ~492–499) = `("command", "bash", "powershell", "windows", "linux",
  "osx")`. `commandWindows` is **not** in it. `_rewrite_hooks_data` (lines ~832–888) iterates only
  these keys, so `commandWindows` is never parsed for script references.
- The path-resolving regexes in `_rewrite_command_for_target` (lines ~728–731, ~770) match only
  `${…_PLUGIN_ROOT}/…` and `./…` / `.\…`. The package authored `commandWindows` as a pre-baked
  deploy path (`.codex\hooks\…\bootstrap.bat`), which would not match even if the key were parsed.
- Ironic near-miss: `HOOK_COMMAND_KEYS` already carries `"windows"` — but that is VS Code's
  OS-override key (line 484), not Codex's `commandWindows`. Codex's strict schema would reject a
  `"windows"` key, so renaming is not a workaround.

### Proposed fix (upstream)

1. Add `commandWindows` (and TOML alias `command_windows`) to `HOOK_COMMAND_KEYS` — the comment at
   line ~478 notes a single addition propagates to every call site.
2. Keep the rewriter resolving package-relative references inside that key, so a `commandWindows`
   authored as `.\scripts\bootstrap.bat` gets its `.bat` copied and the path rewritten — the same
   treatment `command` already gets.

Package-side follow-up regardless of upstream: author `commandWindows` as a package-relative
reference (`.\scripts\bootstrap.bat`), not a hardcoded `.codex\hooks\…` path.

### Note on the live repo

The live repo's `.codex/hooks/qubership-skills-telemetry/scripts/` currently holds `bootstrap.bat`
/`.ps1`/`.sh`, but those did not come from this install path (the `.bat` shows as untracked in
git) — a clean `apm install` does not reproduce them. The Windows hook only "works" today because
the `.bat` is already present from a prior state; that is the latent fragility this bug describes.

### Test provenance

Done in an isolated temp project (clean APM v0.21.0 install, package copied out of the working
tree); the live repo was not touched and APM source was read from a shallow clone of
`microsoft/apm`. Flag-name drift to record: APM 0.21 uses `--target`, while `CLAUDE.md` still says
`--target <…>` for an older flow — verify before quoting.

## Bottom line — drop the "hook invokes a script that finds the binary" model

Calling the binary through a per-harness bootstrap **script** wired into the hook command is only
cleanly portable on Unix. Across the matrix it degrades harness by harness on Windows:

| Harness | Hook shell on Windows | Script-invokes-binary status |
|---|---|---|
| Linux / macOS (all harnesses) | POSIX `sh` | **Fully supported.** `sh ./bootstrap.sh …` just works. |
| Claude Code (Windows) | Git Bash | **Tolerable hack only.** Needs forward-slash `.bat` path in a `sh … \|\| …bat` fallback; works but is fragile wiring, not a clean path. |
| Cursor (Windows) | PowerShell 5.1 | **Not supported.** `\|\|` is a PS 5.1 parse error before any script runs; the whole `sh … \|\| …` design is dead on arrival. |
| Codex (Windows) | `commandWindows` → `cmd.exe` | **Blocked until an APM fix.** The field is the right native mechanism, but APM neither deploys the `.bat` it names nor rewrites its path (see bug-report draft above), so a clean install yields a broken hook. |

So three of the four Windows paths are either a hack, broken, or blocked on upstream. The
script-in-hook model cannot be made uniformly portable from inside this repo — Cursor needs a
shell we don't control and Codex needs a change in APM we don't own.

**Decision: set this model aside and test the alternative — put the binary itself on `PATH` for
every OS, so the hook command is just the bare binary name (`skills-telemetry ingest --agent=X`)
with no shell-specific wrapper, no fallback operator, and no per-harness script to deploy.**

Rollout to evaluate, in order:

1. **One-time provisioning skill** — the `provision-skills-telemetry` flow installs the binary and
   ensures it is on `PATH` (per-OS: user `PATH` on Windows, a profile/`/usr/local/bin`-style
   location on Unix). Proves the bare-name hook works end to end across all four harnesses.
2. **Package managers** — once the bare-name hook is validated, move binary distribution and
   `PATH` placement to standard package managers (e.g. winget/scoop on Windows, Homebrew on macOS,
   apt/an equivalent on Linux) so install and updates stop depending on the in-hook bootstrap
   download at all.

This removes every Windows shell trap above at once: a bare binary name is shell-agnostic, so Git
Bash vs PowerShell vs `cmd.exe` no longer matters, and there is no script for APM to deploy or
fail to deploy. The APM `commandWindows` bug is then a non-issue for telemetry (still worth filing
upstream on its own merits).
