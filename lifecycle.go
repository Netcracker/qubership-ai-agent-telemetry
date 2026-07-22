package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

type lifecycleAction string

const (
	actionInstall   lifecycleAction = "install"
	actionUpdate    lifecycleAction = "update"
	actionUninstall lifecycleAction = "uninstall"
)

type componentName string

const (
	componentAPM       componentName = "apm"
	componentTelemetry componentName = "telemetry"
	componentGitHooks  componentName = "git-hooks"
)

type lifecycleOptions struct {
	Action         lifecycleAction
	Components     []componentName
	Harnesses      []hookTarget
	ForceGitHooks  bool
	NonInteractive bool
	Purge          bool
	RemoveCLI      bool
	CLIOnly        bool
}

type operationState string

const (
	operationOK      operationState = "OK"
	operationSkipped operationState = "SKIPPED"
	operationFailed  operationState = "FAILED"
)

type operationResult struct {
	Name   string
	State  operationState
	Detail string
	Err    error
}

type lifecycleSummary struct {
	Results []operationResult
	Err     error
}

type componentOps struct {
	Preflight func(context.Context, lifecycleOptions) error
	Install   func(context.Context, lifecycleOptions) operationResult
	Update    func(context.Context, lifecycleOptions) operationResult
	Uninstall func(context.Context, lifecycleOptions) operationResult
}

// managedCLIService is completed by the managed-path implementation. The
// lifecycle orchestrator depends only on these injectable operations.
type managedCLIService struct {
	Install         func(source string) operationResult
	Remove          func() operationResult
	PreflightRemove func(lifecycleOptions) error
}

type lifecycleDeps struct {
	ManagedCLI managedCLIService
	Components map[componentName]componentOps
}

func runLifecycle(ctx context.Context, opts lifecycleOptions, deps lifecycleDeps) lifecycleSummary {
	normalized, err := normalizeLifecycleOptions(opts)
	if err != nil {
		return lifecycleSummary{Err: err}
	}
	opts = normalized

	removeCLI := opts.Action == actionUninstall && (opts.RemoveCLI || isCompleteSelection(opts.Components))
	var preflightErrors []error
	if !opts.CLIOnly {
		for _, component := range opts.Components {
			operation := deps.Components[component]
			if operation.Preflight == nil {
				continue
			}
			if err := operation.Preflight(ctx, opts); err != nil {
				preflightErrors = append(preflightErrors, fmt.Errorf("%s preflight: %w", component, err))
			}
		}
	}
	if removeCLI && deps.ManagedCLI.PreflightRemove != nil {
		if err := deps.ManagedCLI.PreflightRemove(opts); err != nil {
			preflightErrors = append(preflightErrors, fmt.Errorf("managed CLI removal preflight: %w", err))
		}
	}
	if err := errors.Join(preflightErrors...); err != nil {
		return lifecycleSummary{Err: err}
	}

	results := make([]operationResult, 0, len(opts.Components)+1)
	switch opts.Action {
	case actionInstall, actionUpdate:
		results = append(results, runManagedInstall(deps.ManagedCLI))
		if !opts.CLIOnly {
			for _, component := range opts.Components {
				results = append(results, runComponent(ctx, opts, component, deps.Components[component]))
			}
		}
	case actionUninstall:
		telemetryFailed := false
		for _, component := range opts.Components {
			result := runComponent(ctx, opts, component, deps.Components[component])
			results = append(results, result)
			if component == componentTelemetry && result.State == operationFailed {
				telemetryFailed = true
			}
		}
		if removeCLI {
			if telemetryFailed {
				results = append(results, operationResult{
					Name: "managed-cli", State: operationSkipped,
					Detail: "telemetry cleanup failed; managed CLI was preserved",
				})
			} else {
				results = append(results, runManagedRemove(deps.ManagedCLI))
			}
		} else {
			results = append(results, operationResult{
				Name: "managed-cli", State: operationSkipped,
				Detail: "preserved for partial uninstall",
			})
		}
	}

	return lifecycleSummary{Results: results, Err: failedResultError(results)}
}

func runManagedInstall(service managedCLIService) operationResult {
	if service.Install == nil {
		return operationResult{Name: "managed-cli", State: operationSkipped, Detail: "managed CLI operation is unavailable"}
	}
	source, err := os.Executable()
	if err != nil {
		return operationResult{
			Name: "managed-cli", State: operationFailed,
			Detail: "cannot resolve the running executable", Err: err,
		}
	}
	return normalizeOperationResult(service.Install(source), "managed-cli")
}

func runManagedRemove(service managedCLIService) operationResult {
	if service.Remove == nil {
		return operationResult{Name: "managed-cli", State: operationSkipped, Detail: "managed CLI removal is unavailable"}
	}
	return normalizeOperationResult(service.Remove(), "managed-cli")
}

func runComponent(ctx context.Context, opts lifecycleOptions, name componentName, operations componentOps) operationResult {
	var operation func(context.Context, lifecycleOptions) operationResult
	switch opts.Action {
	case actionInstall:
		operation = operations.Install
	case actionUpdate:
		operation = operations.Update
	case actionUninstall:
		operation = operations.Uninstall
	}
	if operation == nil {
		return operationResult{Name: string(name), State: operationSkipped, Detail: "component operation is unavailable"}
	}
	return normalizeOperationResult(operation(ctx, opts), string(name))
}

func normalizeOperationResult(result operationResult, fallback string) operationResult {
	if result.Name == "" {
		result.Name = fallback
	}
	switch result.State {
	case operationOK, operationSkipped, operationFailed:
		return result
	default:
		detail := fmt.Sprintf("invalid operation state %q; report OK, SKIPPED, or FAILED", result.State)
		result.State = operationFailed
		result.Detail = detail
		result.Err = errors.New(detail)
	}
	return result
}

func failedResultError(results []operationResult) error {
	var failures []error
	for _, result := range results {
		if result.State != operationFailed {
			continue
		}
		err := result.Err
		if err == nil {
			detail := result.Detail
			if detail == "" {
				detail = "operation failed"
			}
			err = errors.New(detail)
		}
		failures = append(failures, fmt.Errorf("%s: %w", result.Name, err))
	}
	return errors.Join(failures...)
}

func formatLifecycleSummary(summary lifecycleSummary) string {
	var output strings.Builder
	for _, result := range summary.Results {
		_, _ = fmt.Fprintf(&output, "%-12s %-8s %s\n", result.Name, result.State, result.Detail)
	}
	return output.String()
}
