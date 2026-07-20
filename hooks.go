package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type hookSpec struct {
	event   string
	matcher string
}

var claudeHookSpecs = []hookSpec{
	{event: "PreToolUse", matcher: "Skill"},
	{event: "UserPromptExpansion"},
	{event: "PostToolUse", matcher: "mcp__.*"},
	{event: "PostToolUseFailure", matcher: "mcp__.*"},
}

var codexHookSpecs = []hookSpec{
	{event: "Stop"},
	{event: "PostToolUse", matcher: "mcp__.*"},
}

var cursorHookEvents = []string{
	"afterAgentResponse", "afterMCPExecution",
}

type hookState string

const (
	hookInstalled hookState = "installed"
	hookMissing   hookState = "missing"
	hookInvalid   hookState = "invalid"
)

type hookStatus struct {
	Target hookTarget
	Path   string
	State  hookState
	Detail string
}

type hookInstallResult struct {
	Target  hookTarget
	Path    string
	Changed bool
	Err     error
}

var errUserHomeUnavailable = errors.New("user home directory unavailable; set HOME or USERPROFILE")

func hookPath(home string, target hookTarget) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	switch target {
	case hookClaude:
		return filepath.Join(home, ".claude", "settings.json")
	case hookCodex:
		return filepath.Join(home, ".codex", "hooks.json")
	case hookCursor:
		return filepath.Join(home, ".cursor", "hooks.json")
	default:
		return ""
	}
}

func installHooks(home string, targets []hookTarget) []hookInstallResult {
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
		merge := mergeClaudeHook
		switch target {
		case hookCodex:
			merge = mergeCodexHook
		case hookCursor:
			merge = mergeCursorHook
		}
		changed, err := updateHookFile(path, merge)
		if target == hookCodex {
			ruleChanged, ruleErr := updateCodexRule(codexRulePath(home))
			changed = changed || ruleChanged
			err = errors.Join(err, ruleErr)
		}
		results = append(results, hookInstallResult{Target: target, Path: path, Changed: changed, Err: err})
	}
	return results
}

func installManagedHooks(
	home string,
	targets []hookTarget,
	warnings io.Writer,
) ([]hookInstallResult, error) {
	return installManagedHooksWith(
		home, targets, warnings, invalidateHookReceipt, cleanupLegacyTelemetryAPM, installHooks,
	)
}

func installManagedHooksWith(
	home string,
	targets []hookTarget,
	warnings io.Writer,
	invalidate func(string) error,
	cleanup func(string, io.Writer),
	install func(string, []hookTarget) []hookInstallResult,
) ([]hookInstallResult, error) {
	if len(targets) > 0 {
		if err := invalidate(home); err != nil {
			return nil, fmt.Errorf("invalidate hook removal receipt: %w", err)
		}
		cleanup(home, warnings)
	}
	return install(home, targets), nil
}

func hookInstallError(results []hookInstallResult) error {
	var errs []error
	for _, result := range results {
		if result.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", result.Target, result.Err))
		}
	}
	return errors.Join(errs...)
}

func gatherHookStatus(home string) []hookStatus {
	statuses := make([]hookStatus, 0, len(allHookTargets))
	if strings.TrimSpace(home) == "" {
		for _, target := range allHookTargets {
			statuses = append(statuses, hookStatus{
				Target: target,
				State:  hookInvalid,
				Detail: errUserHomeUnavailable.Error(),
			})
		}
		return statuses
	}
	for _, target := range allHookTargets {
		path := hookPath(home, target)
		status := hookStatus{Target: target, Path: path, State: hookMissing}
		root, err := readHookRoot(path)
		if errors.Is(err, os.ErrNotExist) {
			statuses = append(statuses, status)
			continue
		}
		if err != nil {
			status.State = hookInvalid
			status.Detail = err.Error()
			statuses = append(statuses, status)
			continue
		}

		valid, detail := inspectHookTarget(root, target)
		if detail != "" {
			status.State = hookInvalid
			status.Detail = detail
		} else if valid {
			if target == hookCodex {
				if _, detail := inspectCodexRule(codexRulePath(home)); detail != "" {
					status.State = hookInvalid
					status.Detail = detail
					statuses = append(statuses, status)
					continue
				}
			}
			status.State = hookInstalled
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func readHookRoot(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parse %s: root must be an object", path)
	}
	return root, nil
}

func inspectHookTarget(root map[string]any, target hookTarget) (bool, string) {
	copyRoot := cloneJSONObject(root)
	var changed bool
	var err error
	var installed bool
	switch target {
	case hookClaude:
		changed, err = mergeClaudeHook(copyRoot)
		installed = inspectClaudeHook(root)
	case hookCodex:
		changed, err = mergeCodexHook(copyRoot)
		installed = inspectCodexHook(root)
	case hookCursor:
		changed, err = mergeCursorHook(copyRoot)
		installed = inspectCursorHook(root)
	default:
		return false, fmt.Sprintf("unknown hook target %q", target)
	}
	if err != nil {
		return false, err.Error()
	}
	return installed && !changed, ""
}

func mergeGroupedHooks(
	root map[string]any,
	specs []hookSpec,
	newHandler func() map[string]any,
	isOwned func(map[string]any) bool,
) (bool, error) {
	hooks, events, err := validateGroupedHooks(root, specs)
	if err != nil {
		return false, err
	}

	merged := make(map[string][]any, len(specs))
	changed := hooks == nil
	for _, spec := range specs {
		groups := events[spec.event]
		newGroups := make([]any, len(groups))
		firstMatching := -1
		preferred := -1
		for i, value := range groups {
			group := value.(map[string]any)
			newGroup := cloneJSONObject(group)
			matcher, hasMatcher := group["matcher"].(string)
			matching := hasMatcher && matcher == spec.matcher
			if spec.matcher == "" {
				matching = !hasMatcher
			}
			if matching && firstMatching < 0 {
				firstMatching = i
			}

			handlers, hasHandlers := group["hooks"].([]any)
			filtered := make([]any, 0, len(handlers)+1)
			removedOwned := false
			for _, handlerValue := range handlers {
				handler := handlerValue.(map[string]any)
				if isOwned(handler) {
					removedOwned = true
					continue
				}
				filtered = append(filtered, handler)
			}
			ownedGroup := group["_apm_source"] == hookAPMSource
			if ownedGroup {
				delete(newGroup, "_apm_source")
			}
			if matching && preferred < 0 && (removedOwned || ownedGroup) {
				preferred = i
			}
			if hasHandlers || removedOwned {
				newGroup["hooks"] = filtered
			}
			newGroups[i] = newGroup
		}

		if preferred < 0 && spec.matcher != "" {
			preferred = firstMatching
		}
		if preferred < 0 {
			group := map[string]any{"hooks": []any{newHandler()}}
			if spec.matcher != "" {
				group["matcher"] = spec.matcher
			}
			newGroups = append(newGroups, group)
		} else {
			group := newGroups[preferred].(map[string]any)
			handlers, _ := group["hooks"].([]any)
			group["hooks"] = append(handlers, newHandler())
		}
		merged[spec.event] = newGroups
		changed = changed || !reflect.DeepEqual(groups, newGroups)
	}

	if !changed {
		return false, nil
	}
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	for _, spec := range specs {
		hooks[spec.event] = merged[spec.event]
	}
	return true, nil
}

func validateGroupedHooks(root map[string]any, specs []hookSpec) (map[string]any, map[string][]any, error) {
	hooksValue, hasHooks := root["hooks"]
	if !hasHooks {
		return nil, map[string][]any{}, nil
	}
	hooks, ok := hooksValue.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("hooks must be an object")
	}
	events := make(map[string][]any, len(specs))
	for _, spec := range specs {
		value, exists := hooks[spec.event]
		if !exists {
			continue
		}
		groups, ok := value.([]any)
		if !ok {
			return nil, nil, fmt.Errorf("hooks.%s must be an array", spec.event)
		}
		for i, groupValue := range groups {
			group, ok := groupValue.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("hooks.%s[%d] must be an object", spec.event, i)
			}
			if matcher, exists := group["matcher"]; exists {
				if _, ok := matcher.(string); !ok {
					return nil, nil, fmt.Errorf("hooks.%s[%d].matcher must be a string", spec.event, i)
				}
			}
			handlersValue, hasHandlers := group["hooks"]
			if !hasHandlers {
				continue
			}
			handlers, ok := handlersValue.([]any)
			if !ok {
				return nil, nil, fmt.Errorf("hooks.%s[%d].hooks must be an array", spec.event, i)
			}
			for j, handler := range handlers {
				if _, ok := handler.(map[string]any); !ok {
					return nil, nil, fmt.Errorf("hooks.%s[%d].hooks[%d] must be an object", spec.event, i, j)
				}
			}
		}
		events[spec.event] = groups
	}
	return hooks, events, nil
}

func codexHookChanged(results []hookInstallResult) bool {
	for _, result := range results {
		if result.Target == hookCodex && result.Changed && result.Err == nil {
			return true
		}
	}
	return false
}

type hookTarget string

const (
	hookClaude hookTarget = "claude"
	hookCodex  hookTarget = "codex"
	hookCursor hookTarget = "cursor"
)

var allHookTargets = []hookTarget{hookClaude, hookCodex, hookCursor}

type configureOptions struct {
	Endpoint  string
	CAPath    string
	RepoAllow string
	Hooks     []hookTarget
	Delivery  deliverySettingOverrides
}

type deliverySettingOverrides struct {
	BufferCap    string
	FlushTimeout string
}

func parseHookTargets(raw string) ([]hookTarget, error) {
	if raw == "" || raw == "all" {
		return append([]hookTarget(nil), allHookTargets...), nil
	}
	if raw == "none" {
		return []hookTarget{}, nil
	}

	requested := map[hookTarget]bool{}
	for _, value := range strings.Split(raw, ",") {
		target := hookTarget(strings.TrimSpace(value))
		switch target {
		case hookClaude, hookCodex, hookCursor:
			requested[target] = true
		default:
			return nil, fmt.Errorf("unknown hook target %q", value)
		}
	}

	var targets []hookTarget
	for _, target := range allHookTargets {
		if requested[target] {
			targets = append(targets, target)
		}
	}
	return targets, nil
}

type hooksAction string

const (
	hooksInstall   hooksAction = "install"
	hooksUninstall hooksAction = "uninstall"
)

type hooksCommand struct {
	Action  hooksAction
	Targets []hookTarget
}

func parseHooksCommand(args []string) (hooksCommand, error) {
	if len(args) == 0 {
		return hooksCommand{}, fmt.Errorf("missing hooks action")
	}
	action := hooksAction(args[0])
	if action != hooksInstall && action != hooksUninstall {
		return hooksCommand{}, fmt.Errorf("unknown hooks action %q", args[0])
	}

	rawTargets := ""
	targetSet := false
	for _, arg := range args[1:] {
		if !strings.HasPrefix(arg, "--target=") {
			return hooksCommand{}, fmt.Errorf("unknown hooks %s flag %q", action, arg)
		}
		if targetSet {
			return hooksCommand{}, fmt.Errorf("hook target flag may be specified only once")
		}
		rawTargets = strings.TrimPrefix(arg, "--target=")
		if rawTargets == "" {
			return hooksCommand{}, fmt.Errorf("hook target value must not be empty")
		}
		targetSet = true
	}
	if targetSet && (rawTargets == "all" || rawTargets == "none") {
		return hooksCommand{}, fmt.Errorf(
			"hook target %q is not valid here; omit --target to process all hooks", rawTargets,
		)
	}
	targets, err := parseHookTargets(rawTargets)
	return hooksCommand{Action: action, Targets: targets}, err
}

func fullHookTargetSet(targets []hookTarget) bool {
	if len(targets) != len(allHookTargets) {
		return false
	}
	requested := make(map[hookTarget]bool, len(targets))
	for _, target := range targets {
		requested[target] = true
	}
	for _, target := range allHookTargets {
		if !requested[target] {
			return false
		}
	}
	return true
}
