package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	apmMarketplaceName  = "qubership-ai-packages"
	apmMarketplace      = "Netcracker/qubership-ai-packages"
	apmBaselinePackage  = "qubership-global-essentials@qubership-ai-packages"
	apmUnixInstaller    = "https://aka.ms/apm-unix"
	apmWindowsInstaller = "https://aka.ms/apm-windows"
)

type apmDeps struct {
	Home     string
	LookPath func(string) (string, error)
	Download func(context.Context, string, string) error
	Run      func(context.Context, string, ...string) (string, error)
}

func newAPMComponent(deps apmDeps) componentOps {
	deps = normalizeAPMDeps(deps)

	install := func(ctx context.Context, opts lifecycleOptions) operationResult {
		apm, err := deps.LookPath("apm")
		if err != nil {
			if err := installAPMCLI(ctx, deps); err != nil {
				return apmFailure("cannot install APM CLI", err)
			}
			apm, err = deps.LookPath("apm")
			if err != nil {
				return apmFailure("APM installer completed, but apm is not on PATH", err)
			}
		}
		return installFreshAPMBaseline(ctx, deps, apm, opts.Harnesses)
	}

	update := func(ctx context.Context, opts lifecycleOptions) operationResult {
		apm, err := deps.LookPath("apm")
		if err != nil {
			if err := installAPMCLI(ctx, deps); err != nil {
				return apmFailure("cannot install APM CLI", err)
			}
			apm, err = deps.LookPath("apm")
			if err != nil {
				return apmFailure("APM installer completed, but apm is not on PATH", err)
			}
			return installFreshAPMBaseline(ctx, deps, apm, opts.Harnesses)
		}
		commands := [][]string{
			{"self-update"},
			{"marketplace", "update", apmMarketplaceName},
			{"install", "--update", apmBaselinePackage, "-g", "--target", joinAPMTargets(opts.Harnesses)},
		}
		for _, args := range commands {
			if _, err := runAPMCommand(ctx, deps, apm, args...); err != nil {
				return apmFailure("cannot update APM baseline", err)
			}
		}
		if result := compileAndVerifyAPM(ctx, deps, apm); result.State == operationFailed {
			return result
		}
		return operationResult{Name: string(componentAPM), State: operationOK, Detail: "baseline updated"}
	}

	uninstall := func(ctx context.Context, _ lifecycleOptions) operationResult {
		if strings.TrimSpace(deps.Home) == "" {
			return apmFailure("cannot inspect global APM manifest", errors.New("user home is unavailable"))
		}
		manifestPath := filepath.Join(deps.Home, ".apm", "apm.yml")
		data, err := os.ReadFile(manifestPath)
		if errors.Is(err, os.ErrNotExist) {
			return operationResult{Name: string(componentAPM), State: operationSkipped, Detail: "global APM manifest is absent"}
		}
		if err != nil {
			return apmFailure("cannot read global APM manifest", err)
		}
		installed, err := hasGlobalAPMDependency(data, apmBaselinePackage)
		if err != nil {
			return apmFailure("cannot parse global APM manifest", fmt.Errorf("parse %s: %w", manifestPath, err))
		}
		if !installed {
			return operationResult{Name: string(componentAPM), State: operationSkipped, Detail: "APM baseline package is absent"}
		}
		apm, err := deps.LookPath("apm")
		if err != nil {
			return apmFailure("cannot uninstall APM baseline because apm is not on PATH", err)
		}
		if _, err := runAPMCommand(ctx, deps, apm, "uninstall", "-g", apmBaselinePackage); err != nil {
			return apmFailure("cannot uninstall APM baseline package", err)
		}
		return operationResult{Name: string(componentAPM), State: operationOK, Detail: "baseline package removed; shared APM CLI and marketplace preserved"}
	}

	return componentOps{Install: install, Update: update, Uninstall: uninstall}
}

func installFreshAPMBaseline(
	ctx context.Context,
	deps apmDeps,
	apm string,
	targets []hookTarget,
) operationResult {
	marketplaces, err := runAPMCommand(ctx, deps, apm, "marketplace", "list")
	if err != nil {
		return apmFailure("cannot inspect APM marketplaces", err)
	}
	if !containsAPMMarketplace(marketplaces, apmMarketplaceName) {
		if _, err := runAPMCommand(ctx, deps, apm, "marketplace", "add", apmMarketplace); err != nil {
			return apmFailure("cannot add APM marketplace", err)
		}
	}
	if _, err := runAPMCommand(ctx, deps, apm, "install", apmBaselinePackage,
		"-g", "--target", joinAPMTargets(targets)); err != nil {
		return apmFailure("cannot install APM baseline package", err)
	}
	if result := compileAndVerifyAPM(ctx, deps, apm); result.State == operationFailed {
		return result
	}
	return operationResult{Name: string(componentAPM), State: operationOK, Detail: "baseline installed"}
}

func normalizeAPMDeps(deps apmDeps) apmDeps {
	if deps.LookPath == nil {
		deps.LookPath = exec.LookPath
	}
	if deps.Download == nil {
		deps.Download = downloadAPMInstaller
	}
	if deps.Run == nil {
		deps.Run = func(ctx context.Context, name string, args ...string) (string, error) {
			output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			return string(output), err
		}
	}
	return deps
}

func installAPMCLI(ctx context.Context, deps apmDeps) error {
	dir, err := os.MkdirTemp("", "apm-installer-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	url := apmUnixInstaller
	name := "install.sh"
	shell := "sh"
	var args []string
	if runtime.GOOS == "windows" {
		url = apmWindowsInstaller
		name = "install.ps1"
		shell = "powershell.exe"
		args = []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File"}
	}
	destination := filepath.Join(dir, name)
	if err := deps.Download(ctx, url, destination); err != nil {
		return fmt.Errorf("download vendor installer: %w", err)
	}
	args = append(args, destination)
	output, err := deps.Run(ctx, shell, args...)
	if err != nil {
		return commandAPMError(shell, args, output, err)
	}
	return nil
}

func downloadAPMInstaller(ctx context.Context, url, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func compileAndVerifyAPM(ctx context.Context, deps apmDeps, apm string) operationResult {
	if _, err := runAPMCommand(ctx, deps, apm, "compile", "-g"); err != nil {
		return apmFailure("cannot compile global APM primitives", err)
	}
	if _, err := runAPMCommand(ctx, deps, apm, "deps", "list", "-g"); err != nil {
		return apmFailure("cannot verify global APM dependencies", err)
	}
	return operationResult{Name: string(componentAPM), State: operationOK}
}

func runAPMCommand(ctx context.Context, deps apmDeps, apm string, args ...string) (string, error) {
	output, err := deps.Run(ctx, apm, args...)
	if err != nil {
		return output, commandAPMError(apm, args, output, err)
	}
	return output, nil
}

func commandAPMError(name string, args []string, output string, err error) error {
	diagnostic, truncated := limitAPMDiagnostic(output)
	message := fmt.Sprintf("%s %s: %v", name, strings.Join(args, " "), err)
	if diagnostic != "" {
		message += "\napm output:\n" + diagnostic
	}
	if truncated {
		message += "\n[apm output truncated]"
	}
	return errors.New(message)
}

func containsAPMMarketplace(output, name string) bool {
	for _, field := range strings.Fields(output) {
		if field == name {
			return true
		}
	}
	return false
}

func joinAPMTargets(targets []hookTarget) string {
	values := make([]string, len(targets))
	for index, target := range targets {
		values[index] = string(target)
	}
	return strings.Join(values, ",")
}

func apmFailure(detail string, err error) operationResult {
	return operationResult{Name: string(componentAPM), State: operationFailed, Detail: detail, Err: err}
}
