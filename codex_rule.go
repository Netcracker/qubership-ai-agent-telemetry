package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const codexExecutionPolicy = `# ai-agent-telemetry: allow the telemetry hook and diagnostics out of the Codex sandbox.
# Scoped to these subcommands only; configure stays sandboxed.
prefix_rule(
    pattern = [["ai-agent-telemetry", "ai-agent-telemetry.exe"], "ingest", "--agent=codex"],
    decision = "allow",
    justification = "Allow the trusted telemetry hook to read its machine config and send Codex skill usage events.",
    match = ["ai-agent-telemetry ingest --agent=codex", "ai-agent-telemetry.exe ingest --agent=codex"],
    not_match = ["ai-agent-telemetry status", "ai-agent-telemetry selftest", "ai-agent-telemetry configure",
                 "ai-agent-telemetry update-check", "ai-agent-telemetry ingest --agent=claude",
                 "ai-agent-telemetry ingest --agent=cursor"],
)
prefix_rule(
    pattern = [["ai-agent-telemetry", "ai-agent-telemetry.exe"], "status"],
    decision = "allow",
    justification = "Allow telemetry diagnostics to read configured state outside the sandbox.",
    match = ["ai-agent-telemetry status", "ai-agent-telemetry.exe status"],
    not_match = ["ai-agent-telemetry configure", "ai-agent-telemetry selftest",
                 "ai-agent-telemetry ingest --agent=codex", "ai-agent-telemetry update-check"],
)
prefix_rule(
    pattern = [["ai-agent-telemetry", "ai-agent-telemetry.exe"], "selftest"],
    decision = "allow",
    justification = "Allow telemetry diagnostics to send a marked probe event outside the sandbox.",
    match = ["ai-agent-telemetry selftest", "ai-agent-telemetry.exe selftest"],
    not_match = ["ai-agent-telemetry configure", "ai-agent-telemetry status",
                 "ai-agent-telemetry ingest --agent=codex", "ai-agent-telemetry update-check"],
)
`

func codexRulePath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "rules", "ai-agent-telemetry.rules")
}

func updateCodexRule(path string) (bool, error) {
	writePath, err := resolveHookWritePath(path)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(writePath)
	if err == nil && bytes.Equal(data, []byte(codexExecutionPolicy)) {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := writeFileAtomically(writePath, []byte(codexExecutionPolicy), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func inspectCodexRule(path string) (bool, string) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, fmt.Sprintf("Codex execution policy is missing: %s", path)
	}
	if err != nil {
		return false, fmt.Sprintf("read Codex execution policy %s: %v", path, err)
	}
	if !bytes.Equal(data, []byte(codexExecutionPolicy)) {
		return false, fmt.Sprintf("Codex execution policy is invalid: %s", path)
	}
	return true, ""
}
