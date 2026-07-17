package main

import (
	"encoding/json"
)

const (
	claudeHookCommand = "ai-agent-telemetry ingest --agent=claude"
	hookAPMSource     = "ai-agent-telemetry"
)

func mergeClaudeHook(root map[string]any) (bool, error) {
	return mergeGroupedHooks(root, claudeHookSpecs, newCanonicalClaudeHandler, isOwnedClaudeHandler)
}

func inspectClaudeHook(root map[string]any) bool {
	copyRoot := cloneJSONObject(root)
	changed, err := mergeClaudeHook(copyRoot)
	return err == nil && !changed
}

func isOwnedClaudeHandler(handler map[string]any) bool {
	return handler["command"] == claudeHookCommand || handler["_apm_source"] == hookAPMSource
}

func newCanonicalClaudeHandler() map[string]any {
	return map[string]any{
		"type":          "command",
		"command":       claudeHookCommand,
		"timeout":       json.Number("30"),
		"statusMessage": "Recording agent telemetry",
	}
}

func cloneJSONObject(value map[string]any) map[string]any {
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		switch typed := item.(type) {
		case map[string]any:
			cloned[key] = cloneJSONObject(typed)
		case []any:
			cloned[key] = cloneJSONArray(typed)
		default:
			cloned[key] = item
		}
	}
	return cloned
}

func cloneJSONArray(value []any) []any {
	cloned := make([]any, len(value))
	for i, item := range value {
		switch typed := item.(type) {
		case map[string]any:
			cloned[i] = cloneJSONObject(typed)
		case []any:
			cloned[i] = cloneJSONArray(typed)
		default:
			cloned[i] = item
		}
	}
	return cloned
}
