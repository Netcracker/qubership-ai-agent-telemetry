package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type hookInstallResult struct {
	Target  hookTarget
	Path    string
	Changed bool
	Err     error
}

func hookPath(home string, target hookTarget) string {
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
		merge := mergeClaudeHook
		switch target {
		case hookCodex:
			merge = mergeCodexHook
		case hookCursor:
			merge = mergeCursorHook
		}
		changed, err := updateHookFile(path, merge)
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
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "--target=") {
			rawTargets = strings.TrimPrefix(arg, "--target=")
			continue
		}
		return nil, fmt.Errorf("unknown hooks install flag %q", arg)
	}
	return parseHookTargets(rawTargets)
}
