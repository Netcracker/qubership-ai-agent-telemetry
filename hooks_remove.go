package main

import (
	"errors"
	"io"
	"reflect"
)

func removeGroupedHooks(
	root map[string]any,
	specs []hookSpec,
	isOwned func(map[string]any) bool,
) (bool, error) {
	hooks, events, err := validateGroupedHooks(root, specs)
	if err != nil || hooks == nil {
		return false, err
	}
	changed := false
	for _, spec := range specs {
		groups := events[spec.event]
		keptGroups := make([]any, 0, len(groups))
		for _, value := range groups {
			group := cloneJSONObject(value.(map[string]any))
			handlers, hasHandlers := group["hooks"].([]any)
			keptHandlers := make([]any, 0, len(handlers))
			removed := group["_apm_source"] == hookAPMSource
			for _, handlerValue := range handlers {
				handler := handlerValue.(map[string]any)
				if isOwned(handler) {
					removed = true
					continue
				}
				keptHandlers = append(keptHandlers, handler)
			}
			if removed {
				delete(group, "_apm_source")
				if hasHandlers {
					group["hooks"] = keptHandlers
				}
			}
			if removed && len(keptHandlers) == 0 && onlyHookGroupFields(group) {
				changed = true
				continue
			}
			keptGroups = append(keptGroups, group)
			changed = changed || removed
		}
		if len(keptGroups) == 0 {
			delete(hooks, spec.event)
		} else if !reflect.DeepEqual(groups, keptGroups) {
			hooks[spec.event] = keptGroups
		}
	}
	if changed && len(hooks) == 0 {
		delete(root, "hooks")
	}
	return changed, nil
}

func onlyHookGroupFields(group map[string]any) bool {
	for key := range group {
		if key != "matcher" && key != "hooks" {
			return false
		}
	}
	return true
}

func removeCursorHook(root map[string]any) (bool, error) {
	hooks, events, _, err := validateCursorHooks(root)
	if err != nil || hooks == nil {
		return false, err
	}
	changed := false
	for _, event := range cursorHookEvents {
		entries := events[event]
		kept := make([]any, 0, len(entries))
		for _, value := range entries {
			entry := value.(map[string]any)
			if isOwnedCursorHook(entry) {
				changed = true
				continue
			}
			kept = append(kept, entry)
		}
		if len(kept) == 0 && len(entries) > 0 {
			delete(hooks, event)
		} else if !reflect.DeepEqual(entries, kept) {
			hooks[event] = kept
		}
	}
	if changed && len(hooks) == 0 {
		delete(root, "hooks")
	}
	return changed, nil
}

func uninstallHooks(home string, targets []hookTarget, warnings io.Writer) []hookInstallResult {
	requested := make(map[hookTarget]bool, len(targets))
	for _, target := range targets {
		requested[target] = true
	}
	results := make([]hookInstallResult, 0, len(requested))
	for _, target := range allHookTargets {
		if !requested[target] {
			continue
		}
		path := hookPath(home, target)
		if path == "" {
			results = append(results, hookInstallResult{Target: target, Err: errUserHomeUnavailable})
			continue
		}
		merge := removeClaudeHook
		if target == hookCodex {
			merge = removeCodexHook
		} else if target == hookCursor {
			merge = removeCursorHook
		}
		changed, err := updateHookFile(path, merge)
		if target == hookCodex {
			ruleChanged, ruleErr := removeCodexRule(codexRulePath(home), warnings)
			changed = changed || ruleChanged
			err = errors.Join(err, ruleErr)
		}
		results = append(results, hookInstallResult{Target: target, Path: path, Changed: changed, Err: err})
	}
	return results
}
