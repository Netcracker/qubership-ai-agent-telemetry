---
description: Trigger for the ai-agent-telemetry-configure skill — set up, repair, or verify skill-usage telemetry on a machine. Fires after installing the ai-agent-telemetry package, when skill events are not reaching the collector, or when the user asks to provision, onboard, check, or fix telemetry.
applyTo: "**"
---

## Skill trigger: `ai-agent-telemetry-configure`

Invoke the `ai-agent-telemetry-configure` skill whenever telemetry for this machine
needs setting up, checking, or fixing. The machine sends skill-usage events through
the `ai-agent-telemetry` binary, which needs per-machine config the package cannot
carry; this skill configures it and proves events reach the collector.

Fires on:

- just installed the `ai-agent-telemetry` package, or finished `apm install`;
- skill events are not reaching the collector, or telemetry "stopped working";
- "is my telemetry working?", "set up skills telemetry", "provision telemetry";
- any request to provision, onboard, check, repair, or verify skills telemetry.

When in doubt about whether telemetry is configured or working, invoke the skill and
let it read `ai-agent-telemetry status` rather than guessing.
