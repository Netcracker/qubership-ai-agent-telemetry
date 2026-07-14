# Qubership developer installer design

## Summary

Add `global-scripts/qubership-dev-install.sh` and `global-scripts/qubership-dev-install.ps1` as an additional developer
workstation bootstrap. The scripts do not replace or modify the existing AI agent telemetry installers. This
repository is a temporary home; the `global-scripts` directory and its dedicated workflow must remain easy to move.

## Command contract

All components and all supported harnesses are selected by default.

```text
qubership-dev-install.sh
  [--components <apm,telemetry,git-hooks|all>]
  [--skip <apm,telemetry,git-hooks>]
  [--harnesses <claude,codex,cursor|all>]
  [--force-git-hooks]
  [--force-update]
  [--non-interactive]
  [-h|--help]
```

PowerShell exposes `-Components`, `-Skip`, `-Harnesses`, `-ForceGitHooks`, `-ForceUpdate`, `-NonInteractive`, and
`-Help`.

Unknown components, unknown harnesses, and empty selections fail before installation begins. `--skip` removes entries
from the `--components` selection. Harness selection applies to APM targets and telemetry hooks.

## Component lifecycle

Each platform script keeps component metadata in one registry and drives the same install, configure, and verify
lifecycle. A component failure does not prevent independent components from running. The command exits with `1` if any
component failed, `2` for a command-line or platform error, and `0` otherwise. A skipped Git hook installation is not a
failure.

### APM baseline

Install a missing APM CLI with the official platform bootstrap. Register the `Netcracker/qubership-ai-packages`
marketplace, install `qubership-global-essentials` globally for the selected harnesses, and run `apm compile -g`.

`--force-update` also runs `apm self-update`, refreshes the marketplace, and updates the installed global umbrella
package with consent supplied non-interactively.

### AI agent telemetry

Run the published telemetry release installer and pass the selected hooks. The telemetry installer remains owned and
tested by its existing files and workflow; this change treats it as an external component contract. Verify the
installed CLI with `ai-agent-telemetry status` and `ai-agent-telemetry selftest`.

`--force-update` maps to the telemetry installer's `--force` or `-Force` option.

### Global Git hooks

Check Git and Java only when `git-hooks` is selected. When either command is missing, interactive mode asks once
whether the user installed the missing tools, then checks again. A negative answer or failed second check stops the
bootstrap before any component changes. Non-interactive mode fails immediately.

Clone `https://github.com/exadmin/pre-commit-global.git` into the platform data directory. Without
`--force-git-hooks`, an unrelated existing global `core.hooksPath` causes this component to be skipped. With the flag,
the installer prints the old and new values before replacing the setting. A missing `CYBER_FERRET_PASSWORD` produces
a warning but does not fail installation.

`--force-update` fast-forwards an existing managed clone. Local changes or a divergent clone fail the component
instead of being discarded.

## Portability and testing

The implementation keeps the installers, tests, and their README under `global-scripts/`. A standalone GitHub Actions
workflow tests Linux, macOS, and Windows with local fixtures and no external installer calls. Environment overrides
provide test URLs and a managed Git-hooks directory without test-only branches in production code.

Publish the new scripts alongside the existing release assets under their distinct names. Do not rename, replace, or
change `install.sh`, `install.ps1`, or `.github/workflows/installer-tests.yaml`.
