package main

import (
	"encoding/json"
	"fmt"
	"reflect"
)

const cursorHookCommand = "ai-agent-telemetry ingest --agent=cursor"

func mergeCursorHook(root map[string]any) (bool, error) {
	hooks, afterResponse, hasVersion, err := validateCursorHooks(root)
	if err != nil {
		return false, err
	}

	newAfterResponse := make([]any, 0, len(afterResponse)+1)
	canonicalPlaced := false
	for _, value := range afterResponse {
		entry := value.(map[string]any)
		if isOwnedCursorHook(entry) {
			if !canonicalPlaced {
				newAfterResponse = append(newAfterResponse, newCanonicalCursorHook())
				canonicalPlaced = true
			}
			continue
		}
		newAfterResponse = append(newAfterResponse, entry)
	}
	if !canonicalPlaced {
		newAfterResponse = append(newAfterResponse, newCanonicalCursorHook())
	}

	if hasVersion && hooks != nil && reflect.DeepEqual(afterResponse, newAfterResponse) {
		return false, nil
	}
	if !hasVersion {
		root["version"] = json.Number("1")
	}
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	hooks["afterAgentResponse"] = newAfterResponse
	return true, nil
}

func inspectCursorHook(root map[string]any) bool {
	copyRoot := cloneJSONObject(root)
	changed, err := mergeCursorHook(copyRoot)
	return err == nil && !changed
}

func validateCursorHooks(root map[string]any) (map[string]any, []any, bool, error) {
	version, hasVersion := root["version"]
	if hasVersion && !isJSONNumericValue(version) {
		return nil, nil, false, fmt.Errorf("version must be a number")
	}

	hooksValue, hasHooks := root["hooks"]
	if !hasHooks {
		return nil, nil, hasVersion, nil
	}
	hooks, ok := hooksValue.(map[string]any)
	if !ok {
		return nil, nil, false, fmt.Errorf("hooks must be an object")
	}
	afterValue, hasAfter := hooks["afterAgentResponse"]
	if !hasAfter {
		return hooks, nil, hasVersion, nil
	}
	afterResponse, ok := afterValue.([]any)
	if !ok {
		return nil, nil, false, fmt.Errorf("hooks.afterAgentResponse must be an array")
	}
	for i, value := range afterResponse {
		if _, ok := value.(map[string]any); !ok {
			return nil, nil, false, fmt.Errorf("hooks.afterAgentResponse[%d] must be an object", i)
		}
	}
	return hooks, afterResponse, hasVersion, nil
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
	return entry["command"] == cursorHookCommand || entry["_apm_source"] == hookAPMSource
}

func newCanonicalCursorHook() map[string]any {
	return map[string]any{"command": cursorHookCommand}
}
