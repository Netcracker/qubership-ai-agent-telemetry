package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
)

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
		if target == hookCline {
			changed, err := removeClineHook(path, runtime.GOOS, warnings)
			if errors.Is(err, errClineHookOwnershipConflict) {
				err = fmt.Errorf(
					"%w; managed CLI remains installed at %s; telemetry configuration and cache were preserved; "+
						"if --purge was requested, it did not run; remove the telemetry invocation and ownership comment, "+
						"then rerun the original uninstall command; manual instructions: %s",
					err, managedCLIPath(home, runtime.GOOS), clineManualUninstall)
			}
			results = append(results, hookInstallResult{Target: target, Path: path, Changed: changed, Err: err})
			continue
		}
		remove := removeClaudeHook
		switch target {
		case hookCodex:
			remove = removeCodexHook
		case hookCursor:
			remove = removeCursorHook
		}
		changed, err := updateHookFile(path, remove)
		if target == hookCodex && err == nil {
			ruleChanged, ruleErr := removeCodexRule(codexRulePath(home), warnings)
			changed = changed || ruleChanged
			err = ruleErr
		}
		results = append(results, hookInstallResult{Target: target, Path: path, Changed: changed, Err: err})
	}
	return results
}

func removeClaudeHook(root map[string]any) (bool, error) {
	return removeGroupedHooks(root, claudeHookSpecs, isRemovableClaudeHandler)
}

func removeCodexHook(root map[string]any) (bool, error) {
	return removeGroupedHooks(root, codexHookSpecs, isRemovableCodexHandler)
}

func removeGroupedHooks(root map[string]any, specs []hookSpec, isOwned func(map[string]any) bool) (bool, error) {
	hooks, events, err := validateGroupedHooks(root, specs)
	if err != nil || hooks == nil {
		return false, err
	}

	merged := make(map[string][]any, len(specs))
	eventChanged := make(map[string]bool, len(specs))
	changed := false
	for _, spec := range specs {
		groups := events[spec.event]
		newGroups := make([]any, 0, len(groups))
		for _, value := range groups {
			group := cloneJSONObject(value.(map[string]any))
			ownedGroup := group["_apm_source"] == hookAPMSource
			removed := ownedGroup
			handlers, hasHandlers := group["hooks"].([]any)
			if hasHandlers {
				filtered := make([]any, 0, len(handlers))
				for _, handlerValue := range handlers {
					handler := handlerValue.(map[string]any)
					if isOwned(handler) {
						removed = true
						continue
					}
					filtered = append(filtered, handler)
				}
				group["hooks"] = filtered
			}
			if ownedGroup {
				delete(group, "_apm_source")
			}
			if !removed || !removableEmptyHookGroup(group) {
				newGroups = append(newGroups, group)
			}
		}
		merged[spec.event] = newGroups
		eventChanged[spec.event] = !reflect.DeepEqual(groups, newGroups)
		changed = changed || eventChanged[spec.event]
	}
	if !changed {
		return false, nil
	}
	for _, spec := range specs {
		if !eventChanged[spec.event] {
			continue
		}
		if len(merged[spec.event]) == 0 {
			delete(hooks, spec.event)
		} else {
			hooks[spec.event] = merged[spec.event]
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	return true, nil
}

func removableEmptyHookGroup(group map[string]any) bool {
	if handlers, ok := group["hooks"].([]any); ok && len(handlers) != 0 {
		return false
	}
	for key := range group {
		switch key {
		case "matcher", "hooks":
		default:
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
	merged := make(map[string][]any, len(cursorHookEvents))
	eventChanged := make(map[string]bool, len(cursorHookEvents))
	changed := false
	for _, event := range cursorHookEvents {
		entries := events[event]
		filtered := make([]any, 0, len(entries))
		for _, value := range entries {
			entry := value.(map[string]any)
			if isRemovableCursorHook(entry) {
				continue
			}
			filtered = append(filtered, entry)
		}
		merged[event] = filtered
		eventChanged[event] = !reflect.DeepEqual(entries, filtered)
		changed = changed || eventChanged[event]
	}
	if !changed {
		return false, nil
	}
	for _, event := range cursorHookEvents {
		if !eventChanged[event] {
			continue
		}
		if len(merged[event]) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = merged[event]
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	return true, nil
}

func isRemovableClaudeHandler(handler map[string]any) bool {
	return handler["command"] == claudeHookCommand || handler["_apm_source"] == hookAPMSource
}

func isRemovableCodexHandler(handler map[string]any) bool {
	return handler["command"] == codexHookCommand || handler["_apm_source"] == hookAPMSource
}

func isRemovableCursorHook(entry map[string]any) bool {
	return entry["command"] == cursorHookCommand || entry["_apm_source"] == hookAPMSource
}

func removeCodexRule(path string, warnings io.Writer) (bool, error) {
	resolved, err := resolveHookWritePath(path)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !bytes.Equal(data, []byte(codexExecutionPolicy)) {
		if warnings != nil {
			_, _ = fmt.Fprintf(warnings, "warning: preserved modified Codex execution policy: %s\n", path)
		}
		return false, nil
	}
	if err := os.Remove(resolved); err != nil {
		return false, err
	}
	return true, nil
}
