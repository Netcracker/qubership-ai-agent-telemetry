package main

import (
	"encoding/json"
)

const codexHookCommand = "ai-agent-telemetry ingest --agent=codex"

var legacyCodexHookCommands = map[string]bool{
	`sh "$(git rev-parse --show-toplevel)/apm_modules/_local/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh" ingest --agent=codex --endpoint=https://REPLACE_ME/v1/logs`: true,
	"sh ./scripts/bootstrap.sh ingest --agent=codex": true,
}

func mergeCodexHook(root map[string]any) (bool, error) {
	return mergeGroupedHooks(root, codexHookSpecs, newCanonicalCodexHandler, isOwnedCodexHandler)
}

func inspectCodexHook(root map[string]any) bool {
	copyRoot := cloneJSONObject(root)
	changed, err := mergeCodexHook(copyRoot)
	return err == nil && !changed
}

func isOwnedCodexHandler(handler map[string]any) bool {
	command, _ := handler["command"].(string)
	return command == codexHookCommand || legacyCodexHookCommands[command] || handler["_apm_source"] == hookAPMSource
}

func newCanonicalCodexHandler() map[string]any {
	return map[string]any{
		"type":          "command",
		"command":       codexHookCommand,
		"timeout":       json.Number("30"),
		"statusMessage": "Recording agent telemetry",
	}
}
