package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const configureSkillPackage = "Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry-configure"

type configureSkillDeps struct {
	Home     string
	Version  string
	LookPath func(string) (string, error)
	Run      func(context.Context, string, ...string) (string, error)
}

type configureSkillService struct {
	Install   func(context.Context, lifecycleOptions) operationResult
	Update    func(context.Context, lifecycleOptions) operationResult
	Uninstall func(context.Context, lifecycleOptions) operationResult
}

func newConfigureSkillService(deps configureSkillDeps) configureSkillService {
	deps = normalizeConfigureSkillDeps(deps)
	apply := func(ctx context.Context, opts lifecycleOptions, verb string) operationResult {
		tag := strings.TrimSpace(deps.Version)
		if !isConfigureSkillReleaseTag(tag) {
			return configureSkillSkipped("release tag is unavailable")
		}
		apm, err := deps.LookPath("apm")
		if err != nil {
			return configureSkillSkipped("apm is not on PATH")
		}
		source := configureSkillPackage + "#" + tag
		if _, err := runConfigureSkillCommand(ctx, deps, apm, "install", source, "-g", "--target", joinConfigureSkillTargets(opts.Harnesses)); err != nil {
			return configureSkillWarning("cannot "+verb+" optional configure skill", err)
		}
		if _, err := runConfigureSkillCommand(ctx, deps, apm, "compile", "-g"); err != nil {
			return configureSkillWarning("cannot compile global APM primitives after "+verb+" configure skill", err)
		}
		return operationResult{Name: "configure-skill", State: operationOK, Detail: verb + " optional configure skill"}
	}

	return configureSkillService{
		Install: func(ctx context.Context, opts lifecycleOptions) operationResult { return apply(ctx, opts, "install") },
		Update:  func(ctx context.Context, opts lifecycleOptions) operationResult { return apply(ctx, opts, "update") },
		Uninstall: func(ctx context.Context, _ lifecycleOptions) operationResult {
			tag := strings.TrimSpace(deps.Version)
			if !isConfigureSkillReleaseTag(tag) {
				return configureSkillSkipped("release tag is unavailable")
			}
			if strings.TrimSpace(deps.Home) == "" {
				return configureSkillWarning("cannot inspect global APM manifest", errors.New("user home is unavailable"))
			}
			manifestPath := filepath.Join(deps.Home, ".apm", "apm.yml")
			data, err := os.ReadFile(manifestPath)
			if errors.Is(err, os.ErrNotExist) {
				return configureSkillSkipped("global APM manifest is absent")
			}
			if err != nil {
				return configureSkillWarning("cannot read global APM manifest", err)
			}
			installed, err := hasGlobalAPMDependency(data, configureSkillPackage)
			if err != nil {
				return configureSkillWarning("cannot parse global APM manifest", fmt.Errorf("parse %s: %w", manifestPath, err))
			}
			if !installed {
				return configureSkillSkipped("optional configure skill package is absent")
			}
			apm, err := deps.LookPath("apm")
			if err != nil {
				return configureSkillSkipped("apm is not on PATH")
			}
			if _, err := runConfigureSkillCommand(ctx, deps, apm, "uninstall", "-g", configureSkillPackage); err != nil {
				return configureSkillWarning("cannot uninstall optional configure skill", err)
			}
			return operationResult{Name: "configure-skill", State: operationOK, Detail: "optional configure skill removed"}
		},
	}
}

func normalizeConfigureSkillDeps(deps configureSkillDeps) configureSkillDeps {
	if deps.LookPath == nil {
		deps.LookPath = exec.LookPath
	}
	if deps.Run == nil {
		deps.Run = func(ctx context.Context, name string, args ...string) (string, error) {
			output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return string(output), err
		}
	}
	return deps
}

func isConfigureSkillReleaseTag(tag string) bool {
	if !strings.HasPrefix(tag, "v") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func joinConfigureSkillTargets(targets []hookTarget) string {
	values := make([]string, 0, len(targets))
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		value := string(target)
		if target == hookCline {
			value = "agent-skills"
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	return strings.Join(values, ",")
}

func runConfigureSkillCommand(ctx context.Context, deps configureSkillDeps, apm string, args ...string) (string, error) {
	output, err := deps.Run(ctx, apm, args...)
	if err == nil {
		return output, nil
	}
	diagnostic, truncated := limitAPMDiagnostic(output)
	message := fmt.Sprintf("%s %s: %v", apm, strings.Join(args, " "), err)
	if diagnostic != "" {
		message += "\napm output:\n" + diagnostic
	}
	if truncated {
		message += "\n[apm output truncated]"
	}
	return output, errors.New(message)
}

func configureSkillSkipped(detail string) operationResult {
	return operationResult{Name: "configure-skill", State: operationSkipped, Detail: detail}
}

func configureSkillWarning(detail string, err error) operationResult {
	return operationResult{Name: "configure-skill", State: operationWarn, Detail: detail, Err: err}
}
