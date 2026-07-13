package main

import (
	"fmt"
	"strings"
)

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
