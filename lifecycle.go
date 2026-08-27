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

const componentTelemetry = "telemetry"

type lifecycleOptions struct {
	Action         lifecycleAction
	Harnesses      []hookTarget
	NonInteractive bool
	Purge          bool
}

type operationState string

const (
	operationOK      operationState = "OK"
	operationSkipped operationState = "SKIPPED"
	operationWarn    operationState = "WARN"
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
	Telemetry            componentOps
	ConfigureSkill       configureSkillService
}

func defaultLifecycleDeps(home string, warnings io.Writer) lifecycleDeps {
	return lifecycleDeps{
		ManagedCLI:           defaultManagedCLIService(home),
		ManagedInstallSource: os.Executable,
		Telemetry:            newTelemetryComponent(telemetryDeps{Home: func() string { return home }, Warnings: warnings}),
		ConfigureSkill:       newConfigureSkillService(configureSkillDeps{Home: home, Version: version}),
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
	var preflightErrors []error
	if err := preflightManagedCLI(opts, deps.ManagedCLI); err != nil {
		preflightErrors = append(preflightErrors, err)
	}
	if deps.Telemetry.Preflight != nil {
		if err := deps.Telemetry.Preflight(ctx, opts); err != nil {
			preflightErrors = append(preflightErrors, fmt.Errorf("telemetry preflight: %w", err))
		}
	}
	if opts.Action == actionUninstall && deps.ManagedCLI.PreflightRemove != nil {
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
	if service.Preflight == nil {
		return nil
	}
	if err := service.Preflight(opts); err != nil {
		return fmt.Errorf("managed CLI preflight: %w", err)
	}
	return nil
}

func executePreparedLifecycle(ctx context.Context, opts lifecycleOptions, deps lifecycleDeps) lifecycleSummary {
	results := make([]operationResult, 0, 3)
	switch opts.Action {
	case actionInstall, actionUpdate:
		managedResult := runManagedInstall(deps.ManagedCLI, deps.ManagedInstallSource)
		results = append(results, managedResult)
		if managedResult.State != operationOK {
			results = append(results, operationResult{
				Name: componentTelemetry, State: operationSkipped,
				Detail: "managed CLI prerequisite was not installed; native hooks were preserved",
			})
		} else {
			telemetryResult := runComponent(ctx, opts, componentTelemetry, deps.Telemetry)
			results = append(results, telemetryResult)
			if telemetryResult.State == operationOK {
				if result, ok := runConfigureSkill(ctx, opts, deps.ConfigureSkill); ok {
					results = append(results, result)
				}
			}
		}
	case actionUninstall:
		telemetryResult := runComponent(ctx, opts, componentTelemetry, deps.Telemetry)
		results = append(results, telemetryResult)
		if result, ok := runConfigureSkill(ctx, opts, deps.ConfigureSkill); ok {
			results = append(results, result)
		}
		if telemetryResult.State == operationFailed {
			results = append(results, operationResult{
				Name: "managed-cli", State: operationSkipped,
				Detail: "telemetry cleanup failed; managed CLI was preserved",
			})
		} else {
			results = append(results, runManagedRemove(deps.ManagedCLI))
		}
	}

	return lifecycleSummary{Results: results, Err: failedResultError(results)}
}

func runConfigureSkill(ctx context.Context, opts lifecycleOptions, service configureSkillService) (operationResult, bool) {
	var operation func(context.Context, lifecycleOptions) operationResult
	switch opts.Action {
	case actionInstall:
		operation = service.Install
	case actionUpdate:
		operation = service.Update
	case actionUninstall:
		operation = service.Uninstall
	}
	if operation == nil {
		return operationResult{}, false
	}
	return normalizeOperationResult(operation(ctx, opts), "configure-skill"), true
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

func runComponent(ctx context.Context, opts lifecycleOptions, name string, operations componentOps) operationResult {
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
		return operationResult{Name: name, State: operationSkipped, Detail: "component operation is unavailable"}
	}
	return normalizeOperationResult(operation(ctx, opts), name)
}

func normalizeOperationResult(result operationResult, fallback string) operationResult {
	if result.Name == "" {
		result.Name = fallback
	}
	switch result.State {
	case operationOK, operationSkipped, operationWarn, operationFailed:
		return result
	default:
		detail := fmt.Sprintf("invalid operation state %q; report OK, SKIPPED, WARN, or FAILED", result.State)
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
