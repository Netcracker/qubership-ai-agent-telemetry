package main

import (
	"encoding/json"
	"fmt"
	"reflect"
)

const (
	claudeHookCommand = "ai-agent-telemetry ingest --agent=claude"
	hookAPMSource     = "ai-agent-telemetry"
)

func mergeClaudeHook(root map[string]any) (bool, error) {
	hooks, preToolUse, err := validateClaudeHooks(root)
	if err != nil {
		return false, err
	}

	newPreToolUse := make([]any, len(preToolUse))
	firstSkill := -1
	for i, value := range preToolUse {
		group := value.(map[string]any)
		if group["matcher"] != "Skill" {
			newPreToolUse[i] = group
			continue
		}
		if firstSkill < 0 {
			firstSkill = i
		}

		newGroup := cloneJSONObject(group)
		handlers, _ := group["hooks"].([]any)
		ownedByAPM := group["_apm_source"] == hookAPMSource
		filtered := make([]any, 0, len(handlers)+1)
		for _, handlerValue := range handlers {
			handler := handlerValue.(map[string]any)
			if isOwnedClaudeHandler(handler) {
				continue
			}
			filtered = append(filtered, handler)
		}
		if ownedByAPM {
			delete(newGroup, "_apm_source")
		}
		newGroup["hooks"] = filtered
		newPreToolUse[i] = newGroup
	}

	if firstSkill < 0 {
		newPreToolUse = append(newPreToolUse, map[string]any{
			"matcher": "Skill",
			"hooks":   []any{newCanonicalClaudeHandler()},
		})
	} else {
		group := newPreToolUse[firstSkill].(map[string]any)
		group["hooks"] = append(group["hooks"].([]any), newCanonicalClaudeHandler())
	}

	if hooks != nil && reflect.DeepEqual(preToolUse, newPreToolUse) {
		return false, nil
	}
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	hooks["PreToolUse"] = newPreToolUse
	return true, nil
}

func inspectClaudeHook(root map[string]any) bool {
	copyRoot := cloneJSONObject(root)
	changed, err := mergeClaudeHook(copyRoot)
	return err == nil && !changed
}

func validateClaudeHooks(root map[string]any) (map[string]any, []any, error) {
	hooksValue, hasHooks := root["hooks"]
	if !hasHooks {
		return nil, nil, nil
	}
	hooks, ok := hooksValue.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("hooks must be an object")
	}
	preToolUseValue, hasPreToolUse := hooks["PreToolUse"]
	if !hasPreToolUse {
		return hooks, nil, nil
	}
	preToolUse, ok := preToolUseValue.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("hooks.PreToolUse must be an array")
	}
	for i, groupValue := range preToolUse {
		group, ok := groupValue.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("hooks.PreToolUse[%d] must be an object", i)
		}
		if matcher, exists := group["matcher"]; exists {
			if _, ok := matcher.(string); !ok {
				return nil, nil, fmt.Errorf("hooks.PreToolUse[%d].matcher must be a string", i)
			}
		}
		handlersValue, hasHandlers := group["hooks"]
		if !hasHandlers {
			continue
		}
		handlers, ok := handlersValue.([]any)
		if !ok {
			return nil, nil, fmt.Errorf("hooks.PreToolUse[%d].hooks must be an array", i)
		}
		for j, handler := range handlers {
			if _, ok := handler.(map[string]any); !ok {
				return nil, nil, fmt.Errorf("hooks.PreToolUse[%d].hooks[%d] must be an object", i, j)
			}
		}
	}
	return hooks, preToolUse, nil
}

func isOwnedClaudeHandler(handler map[string]any) bool {
	return handler["command"] == claudeHookCommand || handler["_apm_source"] == hookAPMSource
}

func newCanonicalClaudeHandler() map[string]any {
	return map[string]any{
		"type":          "command",
		"command":       claudeHookCommand,
		"timeout":       json.Number("30"),
		"statusMessage": "Recording skill telemetry",
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
