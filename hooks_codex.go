package main

import (
	"encoding/json"
	"fmt"
	"reflect"
)

const codexHookCommand = "ai-agent-telemetry ingest --agent=codex"

var legacyCodexHookCommands = map[string]bool{
	`sh "$(git rev-parse --show-toplevel)/apm_modules/_local/qubership-skills-telemetry/.apm/hooks/scripts/bootstrap.sh" ingest --agent=codex --endpoint=https://REPLACE_ME/v1/logs`: true,
	"sh ./scripts/bootstrap.sh ingest --agent=codex": true,
}

func mergeCodexHook(root map[string]any) (bool, error) {
	hooks, stop, err := validateCodexHooks(root)
	if err != nil {
		return false, err
	}

	newStop := make([]any, len(stop))
	canonicalPlaced := false
	for i, value := range stop {
		group := value.(map[string]any)
		newGroup := cloneJSONObject(group)
		handlers, hasHandlers := group["hooks"].([]any)
		ownedGroup := group["_apm_source"] == hookAPMSource
		filtered := make([]any, 0, len(handlers)+1)
		foundOwned := ownedGroup
		for _, handlerValue := range handlers {
			handler := handlerValue.(map[string]any)
			if isOwnedCodexHandler(handler) {
				foundOwned = true
				continue
			}
			filtered = append(filtered, handler)
		}
		if ownedGroup {
			delete(newGroup, "_apm_source")
		}
		if foundOwned && !canonicalPlaced {
			filtered = append(filtered, newCanonicalCodexHandler())
			canonicalPlaced = true
		}
		if hasHandlers || foundOwned {
			newGroup["hooks"] = filtered
		}
		newStop[i] = newGroup
	}
	if !canonicalPlaced {
		newStop = append(newStop, map[string]any{"hooks": []any{newCanonicalCodexHandler()}})
	}

	if hooks != nil && reflect.DeepEqual(stop, newStop) {
		return false, nil
	}
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	hooks["Stop"] = newStop
	return true, nil
}

func inspectCodexHook(root map[string]any) bool {
	copyRoot := cloneJSONObject(root)
	changed, err := mergeCodexHook(copyRoot)
	return err == nil && !changed
}

func validateCodexHooks(root map[string]any) (map[string]any, []any, error) {
	hooksValue, hasHooks := root["hooks"]
	if !hasHooks {
		return nil, nil, nil
	}
	hooks, ok := hooksValue.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("hooks must be an object")
	}
	stopValue, hasStop := hooks["Stop"]
	if !hasStop {
		return hooks, nil, nil
	}
	stop, ok := stopValue.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("hooks.Stop must be an array")
	}
	for i, groupValue := range stop {
		group, ok := groupValue.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("hooks.Stop[%d] must be an object", i)
		}
		handlersValue, hasHandlers := group["hooks"]
		if !hasHandlers {
			continue
		}
		handlers, ok := handlersValue.([]any)
		if !ok {
			return nil, nil, fmt.Errorf("hooks.Stop[%d].hooks must be an array", i)
		}
		for j, handler := range handlers {
			if _, ok := handler.(map[string]any); !ok {
				return nil, nil, fmt.Errorf("hooks.Stop[%d].hooks[%d] must be an object", i, j)
			}
		}
	}
	return hooks, stop, nil
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
		"statusMessage": "Recording skill telemetry",
	}
}
