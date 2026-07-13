package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

func canonicalHookCommand(target hookTarget) string {
	switch target {
	case hookClaude:
		return claudeHookCommand
	case hookCodex:
		return codexHookCommand
	case hookCursor:
		return cursorHookCommand
	default:
		return ""
	}
}

type configureOptions struct {
	Endpoint  string
	CAPath    string
	RepoAllow string
	Hooks     []hookTarget
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

func parseHooksCommand(args []string) ([]hookTarget, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("missing hooks action")
	}
	if args[0] != "install" {
		return nil, fmt.Errorf("unknown hooks action %q", args[0])
	}

	rawTargets := ""
	targetFlagSet := false
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "--target=") {
			if targetFlagSet {
				return nil, fmt.Errorf("hook target flag may be specified only once")
			}
			rawTargets = strings.TrimPrefix(arg, "--target=")
			if rawTargets == "" {
				return nil, fmt.Errorf("hook target value must not be empty")
			}
			targetFlagSet = true
			continue
		}
		return nil, fmt.Errorf("unknown hooks install flag %q", arg)
	}
	return parseHookTargets(rawTargets)
}
