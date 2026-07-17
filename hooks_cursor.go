package main

import (
	"encoding/json"
	"fmt"
	"reflect"
)

const cursorHookCommand = "ai-agent-telemetry ingest --agent=cursor"

var legacyCursorHookCommands = map[string]bool{
	"sh ./scripts/bootstrap.sh ingest --agent=cursor": true,
}

func mergeCursorHook(root map[string]any) (bool, error) {
	hooks, events, hasVersion, err := validateCursorHooks(root)
	if err != nil {
		return false, err
	}

	merged := make(map[string][]any, len(cursorHookEvents))
	changed := !hasVersion || hooks == nil
	for _, event := range cursorHookEvents {
		entries := events[event]
		newEntries := make([]any, 0, len(entries)+1)
		canonicalPlaced := false
		for _, value := range entries {
			entry := value.(map[string]any)
			if isOwnedCursorHook(entry) {
				if !canonicalPlaced {
					newEntries = append(newEntries, newCanonicalCursorHook())
					canonicalPlaced = true
				}
				continue
			}
			newEntries = append(newEntries, entry)
		}
		if !canonicalPlaced {
			newEntries = append(newEntries, newCanonicalCursorHook())
		}
		merged[event] = newEntries
		changed = changed || !reflect.DeepEqual(entries, newEntries)
	}

	if !changed {
		return false, nil
	}
	if !hasVersion {
		root["version"] = json.Number("1")
	}
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	for _, event := range cursorHookEvents {
		hooks[event] = merged[event]
	}
	return true, nil
}

func inspectCursorHook(root map[string]any) bool {
	copyRoot := cloneJSONObject(root)
	changed, err := mergeCursorHook(copyRoot)
	return err == nil && !changed
}

func validateCursorHooks(root map[string]any) (map[string]any, map[string][]any, bool, error) {
	version, hasVersion := root["version"]
	if hasVersion && !isJSONNumericValue(version) {
		return nil, nil, false, fmt.Errorf("version must be a number")
	}

	hooksValue, hasHooks := root["hooks"]
	if !hasHooks {
		return nil, map[string][]any{}, hasVersion, nil
	}
	hooks, ok := hooksValue.(map[string]any)
	if !ok {
		return nil, nil, false, fmt.Errorf("hooks must be an object")
	}
	events := make(map[string][]any, len(cursorHookEvents))
	for _, event := range cursorHookEvents {
		value, exists := hooks[event]
		if !exists {
			continue
		}
		entries, ok := value.([]any)
		if !ok {
			return nil, nil, false, fmt.Errorf("hooks.%s must be an array", event)
		}
		for i, value := range entries {
			if _, ok := value.(map[string]any); !ok {
				return nil, nil, false, fmt.Errorf("hooks.%s[%d] must be an object", event, i)
			}
		}
		events[event] = entries
	}
	return hooks, events, hasVersion, nil
}

func isJSONNumericValue(value any) bool {
	switch value.(type) {
	case json.Number, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		_, err := json.Marshal(value)
		return err == nil
	default:
		return false
	}
}

func isOwnedCursorHook(entry map[string]any) bool {
	command, _ := entry["command"].(string)
	return command == cursorHookCommand || legacyCursorHookCommands[command] || entry["_apm_source"] == hookAPMSource
}

func newCanonicalCursorHook() map[string]any {
	return map[string]any{"command": cursorHookCommand}
}
