# Telemetry installer scope and lifecycle coverage

## Status

Proposed

#### Date

#### Owner

@denifilatoff

#### Participants and approvers

Qubership AI Agent Telemetry maintainers; approval pending.

#### Related ADRs

- [0002: CLI invocation via bare binary name on PATH](0002-bare-binary-on-path.md)
- [0005: CLI-managed global hooks](0005-cli-managed-global-hooks.md), superseded by this ADR when accepted
- [0008: Cline hook installation and removal](0008-cline-hook-installation-and-removal.md)

## Context

The telemetry installer currently manages the `ai-agent-telemetry` CLI and native telemetry hooks together with an
unrelated developer baseline: the APM CLI, `qubership-global-essentials`, global Git hooks, CyberFerret, and its Java
prerequisites. This makes a telemetry installation change machine-wide developer tooling that is not required to
collect or deliver telemetry.

CLI-managed telemetry hooks remain necessary. The CLI, endpoint, repository policy, outbox, and hook registration are
machine-level resources. Earlier repository-scoped APM hook delivery required a separate installation in every
repository, could leave supported harnesses unconfigured, and could not reliably refresh hooks during a machine-level
update. The native CLI adapters also provide ownership-aware updates of user hook files across POSIX and Windows.

Two APM use cases remain related to telemetry:

- `agent-packages/ai-agent-telemetry` is a compatibility package for existing repository-local consumers.
- `agent-packages/ai-agent-telemetry-configure` delivers an optional setup and repair skill.

An update must move a legacy global telemetry hook registration from APM to native CLI-managed hooks. It must not
remove unrelated tooling installed by an older lifecycle version. It must also preserve the machine identity,
collector settings, repository policy, certificates, delivery settings, and buffered telemetry.

## Decision

We will limit the telemetry installer to the following lifecycle coverage:

| Lifecycle action | Required behavior | Optional APM integration |
| --- | --- | --- |
| `install` | Install the managed CLI, configure telemetry, and install native hooks for the selected harnesses | If APM is already on `PATH`, install the configure skill globally for the same harnesses |
| `update` | Install the verified CLI update, strictly migrate a legacy global telemetry APM registration, refresh native hooks, and preserve all telemetry state | If APM is already on `PATH`, update the global configure skill to the same release as the CLI |
| `uninstall` | Remove native telemetry hooks and the managed CLI while preserving telemetry state | If APM is available, best-effort remove the global configure skill |
| `uninstall --purge` | Apply normal uninstall and also remove telemetry configuration, machine ID, cache, and outbox | Same as normal uninstall |

The installer will not install, update, remove, or configure:

- the APM CLI;
- `qubership-global-essentials`;
- global Git hooks or `core.hooksPath`;
- CyberFerret; or
- Java prerequisites for non-telemetry tools.

Artifacts left by an older installer remain unchanged. Documentation may provide a separate explicit cleanup command,
but telemetry `install`, `update`, and `uninstall` do not own that cleanup.

There will be one update command. We will not add an `upgrade` command. We will remove component selection,
`--force-git-hooks`, and `update --cli-only`. `--harnesses` remains and controls both native telemetry hooks and the APM
targets used for the configure skill. Omitting it selects every supported harness.

Update will inspect the global APM manifest for the exact legacy telemetry hook package. If that dependency is absent,
APM is not required. If it is present, update must remove it through APM before installing native hooks. A missing APM
executable, an unreadable or invalid global manifest, or a failed uninstall stops the migration with a nonzero result.
The error names the blocking state and prints the exact recovery and retry commands. Update must not create concurrent
legacy and native telemetry registrations.

The configure skill will recognize this migration failure when the user explicitly requests an update. It may remove
only the exact global legacy telemetry dependency, rerun `ai-agent-telemetry update`, and verify the resulting native
hooks. It must not modify repository-local APM manifests or unrelated global packages.

Configure skill installation, update, and removal are secondary outcomes. Their failure is reported prominently but
does not fail an otherwise working CLI and native-hook lifecycle. The installer never installs APM to obtain the skill.

The retained repository-local compatibility package remains supported. The CLI migration inspects only the global APM
manifest and does not rewrite consumer repositories.

### Justification

This boundary keeps telemetry installation limited to artifacts required for telemetry while retaining the native hook
ownership needed for reliable cross-platform installation and updates. It preserves existing machine state and avoids
silently removing developer tools that users may still use independently.

A separate `upgrade` command would duplicate the update lifecycle and create two migration paths. Returning native hook
ownership to APM would reintroduce project-scoped setup, incomplete machine upgrades, and the cross-platform delivery
problems that led to CLI-managed hooks. Keeping the full developer baseline in this installer would continue coupling
unrelated products.

Strict legacy migration is preferable to the previous best-effort cleanup. Continuing after cleanup failure can leave
both APM-managed and CLI-managed hooks active, producing duplicate telemetry and ambiguous ownership.

## Consequences

- A fresh telemetry installation no longer installs APM, Essentials, CyberFerret, Java, or global Git hooks.
- Updating telemetry can change only telemetry-owned hooks, the managed CLI, and the optional configure skill.
- Existing unrelated baseline artifacts remain on the machine until the user removes them explicitly.
- Machines with the exact legacy global telemetry APM dependency require a working APM CLI for one migration.
- A malformed global APM manifest can block update until the user repairs it, preventing ambiguous hook ownership.
- APM remains an optional distribution mechanism for the configure skill and a compatibility mechanism for old
  repository-local consumers, but it is not a runtime dependency of telemetry.
- Normal uninstall preserves machine identity and delivery state so a later reinstall resumes the same configuration.
- Purge is the only lifecycle operation that removes telemetry configuration, identity, and buffered data.
- When accepted, this ADR supersedes ADR 0005. It retains CLI-managed global hooks but replaces its installer details
  and best-effort legacy-cleanup rule with the lifecycle boundary above.
