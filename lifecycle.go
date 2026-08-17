package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	Action          lifecycleAction
	Components      []componentName
	Harnesses       []hookTarget
	ForceGitHooks   bool
	NonInteractive  bool
	Purge           bool
	RemoveCLI       bool
	CLIOnly         bool
	RepoScopeChange repoScopeChange
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
	Preflight       func(lifecycleOptions) error
	PreflightRemove func(lifecycleOptions) error
}

type lifecycleDeps struct {
	ManagedCLI           managedCLIService
	ManagedInstallSource func() (string, error)
	Components           map[componentName]componentOps
	RepoScope            repoScopeUpdateService
}

func defaultLifecycleDeps(home string, warnings io.Writer) lifecycleDeps {
	return lifecycleDeps{
		ManagedCLI:           defaultManagedCLIService(home),
		ManagedInstallSource: os.Executable,
		Components: map[componentName]componentOps{
			componentAPM:       newAPMComponent(apmDeps{Home: home}),
			componentTelemetry: newTelemetryComponent(telemetryDeps{Home: func() string { return home }, Warnings: warnings}),
			componentGitHooks:  newGitHooksComponent(gitHooksDeps{Home: home, Warn: warnings}),
		},
	}
}

func runLifecycle(ctx context.Context, opts lifecycleOptions, deps lifecycleDeps) lifecycleSummary {
	normalized, err := normalizeLifecycleOptions(opts)
	if err != nil {
		return lifecycleSummary{Err: err}
	}
	opts = normalized
	if err := preflightLifecycle(ctx, opts, deps); err != nil {
		return lifecycleSummary{Err: err}
	}
	return executePreparedLifecycle(ctx, opts, deps)
}

func preflightLifecycle(ctx context.Context, opts lifecycleOptions, deps lifecycleDeps) error {
	removeCLI := opts.Action == actionUninstall && (opts.RemoveCLI || isCompleteSelection(opts.Components))
	var preflightErrors []error
	if opts.Action == actionUpdate && deps.RepoScope.Prepare != nil {
		if err := deps.RepoScope.Prepare(opts); err != nil {
			preflightErrors = append(preflightErrors, fmt.Errorf("repository scope preflight: %w", err))
		}
	}
	if err := preflightManagedCLI(opts, deps.ManagedCLI); err != nil {
		preflightErrors = append(preflightErrors, err)
	}
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
		return err
	}
	return nil
}

func preflightManagedCLI(opts lifecycleOptions, service managedCLIService) error {
	touched := opts.Action == actionInstall || opts.Action == actionUpdate ||
		(opts.Action == actionUninstall && (opts.RemoveCLI || isCompleteSelection(opts.Components)))
	if !touched || service.Preflight == nil {
		return nil
	}
	if err := service.Preflight(opts); err != nil {
		return fmt.Errorf("managed CLI preflight: %w", err)
	}
	return nil
}

func executePreparedLifecycle(ctx context.Context, opts lifecycleOptions, deps lifecycleDeps) lifecycleSummary {
	removeCLI := opts.Action == actionUninstall && (opts.RemoveCLI || isCompleteSelection(opts.Components))
	results := make([]operationResult, 0, len(opts.Components)+1)
	switch opts.Action {
	case actionInstall, actionUpdate:
		managedResult := runManagedInstall(deps.ManagedCLI, deps.ManagedInstallSource)
		results = append(results, managedResult)
		if !opts.CLIOnly {
			for _, component := range opts.Components {
				if component == componentTelemetry && managedResult.State != operationOK {
					results = append(results, operationResult{
						Name: string(componentTelemetry), State: operationSkipped,
						Detail: "managed CLI prerequisite was not installed; native hooks were preserved",
					})
					continue
				}
				results = append(results, runComponent(ctx, opts, component, deps.Components[component]))
			}
		}
		if opts.Action == actionUpdate && failedResultError(results) == nil && deps.RepoScope.Apply != nil {
			if result, ok := deps.RepoScope.Apply(); ok {
				results = append(results, normalizeOperationResult(result, "repo-policy"))
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

func runManagedInstall(service managedCLIService, source func() (string, error)) operationResult {
	if service.Install == nil {
		return operationResult{Name: "managed-cli", State: operationSkipped, Detail: "managed CLI operation is unavailable"}
	}
	if source == nil {
		source = os.Executable
	}
	path, err := source()
	if err != nil {
		return operationResult{
			Name: "managed-cli", State: operationFailed,
			Detail: "cannot resolve the running executable", Err: err,
		}
	}
	return normalizeOperationResult(service.Install(path), "managed-cli")
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
