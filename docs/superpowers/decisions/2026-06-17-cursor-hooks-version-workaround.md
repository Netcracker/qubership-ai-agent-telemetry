# Cursor hooks need a top-level `version`; APM strips it

**Date:** 2026-06-17
**Upstream issue:** [microsoft/apm#1823](https://github.com/microsoft/apm/issues/1823)
**Status:** workaround in place, fix pending upstream

## What breaks

Cursor's `.cursor/hooks.json` requires a numeric top-level `version`. Without it,
Cursor rejects the whole project hook config and loads no hooks at all — silently,
with only this line in its hook log:

```
ERROR: Invalid project config: Config version must be a number
ERROR: Failed to parse project hooks configuration
No project hooks configuration found
```

So the package's `afterAgentResponse` hook never fires, the sender never runs, and
no telemetry leaves the machine. Cursor's docs show `version` with a default, which
makes the requirement easy to miss. Confirmed on Cursor 3.7.42.

## Root cause

`apm install` drops the field. Our package template
(`agent-packages/qubership-skills-telemetry/.apm/hooks/skill-call-cursor-hooks.json`)
declares `"version": 1`, but APM's hook integrator
(`src/apm_cli/integration/hook_integrator.py`, `_integrate_merged_hooks()`) builds the
output from an empty dict, seeds only the `hooks` container, and never emits a
top-level `version`. The template's `version` is discarded at parse time. Verified on
APM 0.14.1 and 0.20.0. Full analysis and a proposed fix are in microsoft/apm#1823.

A reinstall over a file that already has `version` preserves it — only a fresh install
drops it.

## Workaround

After any fresh `apm install` for the Cursor target, re-add the field to
`.cursor/hooks.json`:

```json
{
  "version": 1,
  "hooks": { ... }
}
```

Drop the workaround once microsoft/apm#1823 ships.
