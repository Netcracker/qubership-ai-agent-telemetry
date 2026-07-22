package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type telemetryDeps struct {
	Home           func() string
	ConfigDir      func() string
	CacheDir       func() string
	Endpoint       func() string
	Token          func() string
	PromptEndpoint func(context.Context) (string, error)
	PromptToken    func(context.Context) (string, error)
	Configure      func(string, string, string, string, string, deliverySettingOverrides) error
	InstallHooks   func(string, []hookTarget, io.Writer) []hookInstallResult
	UninstallHooks func(string, []hookTarget, io.Writer) []hookInstallResult
	Warnings       io.Writer
}

func newTelemetryComponent(deps telemetryDeps) componentOps {
	deps = normalizeTelemetryDeps(deps)
	var prepared bool
	var endpoint, token string

	preflight := func(ctx context.Context, opts lifecycleOptions) error {
		if opts.Action == actionUninstall {
			return nil
		}
		endpoint = strings.TrimSpace(deps.Endpoint())
		token = deps.Token()
		if endpoint == "" && !opts.NonInteractive {
			var err error
			endpoint, err = deps.PromptEndpoint(ctx)
			endpoint = strings.TrimSpace(endpoint)
			if err != nil {
				return fmt.Errorf("cannot read collector endpoint: %w", err)
			}
			if endpoint != "" && token == "" {
				token, err = deps.PromptToken(ctx)
				if err != nil {
					return fmt.Errorf("cannot read collector token: %w", err)
				}
			}
		}
		if endpoint == "" {
			return errors.New("collector endpoint is required")
		}
		if strings.TrimSpace(deps.ConfigDir()) == "" {
			return errors.New("no user config directory available")
		}
		if strings.TrimSpace(deps.Home()) == "" {
			return errUserHomeUnavailable
		}
		prepared = true
		return nil
	}

	apply := func(_ context.Context, opts lifecycleOptions) operationResult {
		if !prepared {
			return telemetryFailure("telemetry preflight did not complete", errors.New("run telemetry preflight before mutation"))
		}
		if err := deps.Configure(deps.ConfigDir(), endpoint, "", token, "", deliverySettingOverrides{}); err != nil {
			return telemetryFailure("cannot apply telemetry configuration", err)
		}
		results := deps.InstallHooks(deps.Home(), opts.Harnesses, deps.Warnings)
		if err := hookInstallError(results); err != nil {
			return telemetryFailure("cannot install telemetry hooks", err)
		}
		return operationResult{Name: string(componentTelemetry), State: operationOK, Detail: hookResultDetail(results, "installed")}
	}

	uninstall := func(_ context.Context, opts lifecycleOptions) operationResult {
		results := deps.UninstallHooks(deps.Home(), append([]hookTarget(nil), allHookTargets...), deps.Warnings)
		if err := hookInstallError(results); err != nil {
			return telemetryFailure("cannot remove telemetry hooks", err)
		}
		if opts.Purge {
			if err := purgeTelemetryDirs(deps.ConfigDir(), deps.CacheDir()); err != nil {
				return telemetryFailure("cannot purge telemetry data", err)
			}
			return operationResult{Name: string(componentTelemetry), State: operationOK, Detail: "hooks removed; configuration and cache purged"}
		}
		return operationResult{Name: string(componentTelemetry), State: operationOK, Detail: hookResultDetail(results, "removed")}
	}

	return componentOps{Preflight: preflight, Install: apply, Update: apply, Uninstall: uninstall}
}

func normalizeTelemetryDeps(deps telemetryDeps) telemetryDeps {
	if deps.Home == nil {
		deps.Home = userHomeDir
	}
	if deps.ConfigDir == nil {
		deps.ConfigDir = pkgConfigDir
	}
	if deps.CacheDir == nil {
		deps.CacheDir = func() string {
			base := cacheBase()
			if base == "" {
				return ""
			}
			return filepath.Join(base, pkgName)
		}
	}
	if deps.Endpoint == nil {
		deps.Endpoint = func() string { return resolveEndpoint("") }
	}
	if deps.Token == nil {
		deps.Token = resolveToken
	}
	if deps.PromptEndpoint == nil {
		deps.PromptEndpoint = func(context.Context) (string, error) {
			return readLine("Collector endpoint: "), nil
		}
	}
	if deps.PromptToken == nil {
		deps.PromptToken = func(context.Context) (string, error) {
			return readSecret("Collector token (leave blank to skip): "), nil
		}
	}
	if deps.Configure == nil {
		deps.Configure = applyConfigure
	}
	if deps.InstallHooks == nil {
		deps.InstallHooks = installManagedHooks
	}
	if deps.UninstallHooks == nil {
		deps.UninstallHooks = uninstallHooks
	}
	if deps.Warnings == nil {
		deps.Warnings = io.Discard
	}
	return deps
}

func telemetryFailure(detail string, err error) operationResult {
	return operationResult{Name: string(componentTelemetry), State: operationFailed, Detail: detail, Err: err}
}

func hookResultDetail(results []hookInstallResult, verb string) string {
	changed := false
	for _, result := range results {
		changed = changed || result.Changed
	}
	if changed {
		return verb
	}
	return "unchanged"
}

func purgeTelemetryDirs(paths ...string) error {
	var errs []error
	for _, path := range paths {
		clean := filepath.Clean(path)
		if strings.TrimSpace(path) == "" || filepath.Base(clean) != pkgName || filepath.Dir(clean) == clean {
			errs = append(errs, fmt.Errorf("refusing to remove non-package path %q", path))
			continue
		}
		if err := os.RemoveAll(clean); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", clean, err))
		}
	}
	return errors.Join(errs...)
}
