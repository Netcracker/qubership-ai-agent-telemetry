package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	minimumJavaMajor          = 21
	defaultGitHooksRepository = "https://github.com/exadmin/pre-commit-global.git"
	gitHooksDirectoryEnv      = "QUBERSHIP_DEV_GIT_HOOKS_DIR"
	gitHooksRepositoryEnv     = "QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY"
)

var errGitConfigKeyNotFound = errors.New("git config key is not set")

type gitHooksDeps struct {
	Home     string
	DataHome string
	LookPath func(string) (string, error)
	Run      func(context.Context, string, ...string) (string, error)
	Confirm  func(string) (bool, error)
	Warn     io.Writer
	Lstat    func(string) (os.FileInfo, error)
}

func newGitHooksComponent(deps gitHooksDeps) componentOps {
	deps = normalizeGitHooksDeps(deps)
	repositoryDir, repositoryURL := gitHooksLocation(deps)
	hooksDir := filepath.Join(repositoryDir, "hooks-global")
	var prepared, skip bool
	var git string

	preflight := func(ctx context.Context, opts lifecycleOptions) error {
		prepared = false
		skip = false
		if opts.Action == actionUninstall {
			return nil
		}

		var err error
		git, err = requireGitHookPrerequisites(ctx, deps, opts.NonInteractive)
		if err != nil {
			return err
		}
		current, err := readGlobalHooksPath(ctx, deps, git)
		if err != nil {
			return fmt.Errorf("cannot read global core.hooksPath: %w", err)
		}
		if current != "" && !sameGitHooksPath(current, hooksDir, deps.Home) && !opts.ForceGitHooks {
			skip = true
			prepared = true
			return nil
		}
		repositoryExists, err := gitHooksRepositoryExists(deps, repositoryDir)
		if err != nil {
			return err
		}
		if repositoryExists {
			if err := validateGitHooksRepository(ctx, deps, git, repositoryDir, repositoryURL); err != nil {
				return err
			}
			if opts.Action == actionUpdate {
				if _, err := validateGitHooksUpdateTarget(ctx, deps, git, repositoryDir); err != nil {
					return err
				}
			}
		}
		prepared = true
		return nil
	}

	apply := func(ctx context.Context, opts lifecycleOptions) operationResult {
		if !prepared {
			return gitHooksFailure("Git hooks preflight did not complete", errors.New("run Git hooks preflight before mutation"))
		}
		if skip {
			return operationResult{
				Name: string(componentGitHooks), State: operationSkipped,
				Detail: "unrelated global core.hooksPath was preserved",
			}
		}
		if _, err := replaceableGlobalHooksPath(ctx, deps, git, hooksDir, opts.ForceGitHooks); err != nil {
			return gitHooksFailure("global core.hooksPath changed before Git hooks mutation; unrelated configuration was preserved", err)
		}

		existed, err := gitHooksRepositoryExists(deps, repositoryDir)
		if err != nil {
			return gitHooksFailure("cannot inspect global Git hooks repository", err)
		}
		if !existed {
			if err := os.MkdirAll(filepath.Dir(repositoryDir), 0o755); err != nil {
				return gitHooksFailure("cannot create Git hooks data directory", err)
			}
			if output, err := deps.Run(ctx, git, "clone", repositoryURL, repositoryDir); err != nil {
				return gitHooksFailure("cannot clone global Git hooks repository", gitCommandError(output, err))
			}
		}
		if err := validateGitHooksRepository(ctx, deps, git, repositoryDir, repositoryURL); err != nil {
			return gitHooksFailure("cannot validate global Git hooks repository", err)
		}
		if opts.Action == actionUpdate && existed {
			mergeRef, err := validateGitHooksUpdateTarget(ctx, deps, git, repositoryDir)
			if err != nil {
				return gitHooksFailure("cannot verify the global Git hooks update target", err)
			}
			if output, err := deps.Run(ctx, git, "-C", repositoryDir, "fetch", "--no-tags", "origin", mergeRef); err != nil {
				return gitHooksFailure("cannot fetch the global Git hooks update", gitCommandError(output, err))
			}
			if output, err := deps.Run(ctx, git, "-C", repositoryDir, "merge", "--ff-only", "FETCH_HEAD"); err != nil {
				return gitHooksFailure("cannot fast-forward global Git hooks repository", gitCommandError(output, err))
			}
			if err := validateGitHooksRepository(ctx, deps, git, repositoryDir, repositoryURL); err != nil {
				return gitHooksFailure("updated Git hooks repository is invalid", err)
			}
		}

		desired, err := canonicalGitHooksPath(hooksDir, deps.Home)
		if err != nil {
			return gitHooksFailure("cannot resolve managed Git hooks path", err)
		}
		current, err := readGlobalHooksPath(ctx, deps, git)
		if err != nil {
			return gitHooksFailure("cannot read global core.hooksPath", err)
		}
		if current != "" && !sameGitHooksPath(current, desired, deps.Home) && !opts.ForceGitHooks {
			return gitHooksFailure(
				"global core.hooksPath changed after preflight; unrelated configuration was preserved",
				errors.New("rerun with --force-git-hooks to replace the unrelated path"),
			)
		}
		if !sameGitHooksPath(current, desired, deps.Home) {
			if output, err := deps.Run(ctx, git, "config", "--global", "core.hooksPath", desired); err != nil {
				return gitHooksFailure("cannot configure global core.hooksPath", gitCommandError(output, err))
			}
		}
		warnMissingCyberFerretPassword(deps.Warn)
		detail := "installed"
		if opts.Action == actionUpdate && existed {
			detail = "updated"
		} else if existed {
			detail = "unchanged"
		}
		return operationResult{Name: string(componentGitHooks), State: operationOK, Detail: detail}
	}

	uninstall := func(ctx context.Context, _ lifecycleOptions) operationResult {
		git, err := deps.LookPath("git")
		if err != nil {
			return gitHooksFailure("cannot uninstall global Git hooks because git is not on PATH", err)
		}
		current, err := readGlobalHooksPath(ctx, deps, git)
		if err != nil {
			return gitHooksFailure("cannot read global core.hooksPath", err)
		}
		repositoryExists, err := gitHooksRepositoryExists(deps, repositoryDir)
		if err != nil {
			return gitHooksFailure("cannot inspect global Git hooks repository", err)
		}
		if !repositoryExists {
			if current != "" && sameGitHooksPath(current, hooksDir, deps.Home) {
				if err := requireFreshOwnedHooksPath(ctx, deps, git, hooksDir); err != nil {
					return gitHooksFailure("cannot clear global core.hooksPath because fresh ownership was not proven", err)
				}
				if output, err := deps.Run(ctx, git, "config", "--global", "--unset", "core.hooksPath"); err != nil {
					return gitHooksFailure("cannot clear owned global core.hooksPath", gitCommandError(output, err))
				}
				return operationResult{Name: string(componentGitHooks), State: operationOK, Detail: "configuration removed; repository was absent"}
			}
			return operationResult{Name: string(componentGitHooks), State: operationSkipped, Detail: "global Git hooks are absent"}
		}

		if err := validateGitHooksRepository(ctx, deps, git, repositoryDir, repositoryURL); err != nil {
			return gitHooksFailure("managed Git hooks repository was preserved because ownership is ambiguous", err)
		}
		if current != "" && sameGitHooksPath(current, hooksDir, deps.Home) {
			if err := requireFreshOwnedHooksPath(ctx, deps, git, hooksDir); err != nil {
				return gitHooksFailure("cannot clear global core.hooksPath because fresh ownership was not proven", err)
			}
			if output, err := deps.Run(ctx, git, "config", "--global", "--unset", "core.hooksPath"); err != nil {
				return gitHooksFailure("cannot clear owned global core.hooksPath", gitCommandError(output, err))
			}
		} else {
			fresh, err := readGlobalHooksPath(ctx, deps, git)
			if err != nil {
				return gitHooksFailure("cannot reread global core.hooksPath before repository removal", err)
			}
			if sameGitHooksPath(fresh, hooksDir, deps.Home) {
				if err := requireFreshOwnedHooksPath(ctx, deps, git, hooksDir); err != nil {
					return gitHooksFailure("cannot clear global core.hooksPath because fresh ownership was not proven", err)
				}
				if output, err := deps.Run(ctx, git, "config", "--global", "--unset", "core.hooksPath"); err != nil {
					return gitHooksFailure("cannot clear owned global core.hooksPath", gitCommandError(output, err))
				}
			}
		}
		if err := os.RemoveAll(repositoryDir); err != nil {
			return gitHooksFailure("cannot remove managed Git hooks repository", err)
		}
		return operationResult{Name: string(componentGitHooks), State: operationOK, Detail: "owned configuration and repository removed"}
	}

	return componentOps{Preflight: preflight, Install: apply, Update: apply, Uninstall: uninstall}
}

func normalizeGitHooksDeps(deps gitHooksDeps) gitHooksDeps {
	if strings.TrimSpace(deps.Home) == "" {
		deps.Home = userHomeDir()
	}
	if strings.TrimSpace(deps.DataHome) == "" {
		deps.DataHome = defaultGitHooksDataHome(deps.Home)
	}
	if deps.LookPath == nil {
		deps.LookPath = exec.LookPath
	}
	if deps.Run == nil {
		deps.Run = func(ctx context.Context, name string, args ...string) (string, error) {
			output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			if err != nil && isGlobalHooksPathLookup(args) {
				var exitError *exec.ExitError
				if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
					return string(output), errGitConfigKeyNotFound
				}
			}
			return string(output), err
		}
	}
	if deps.Confirm == nil {
		deps.Confirm = func(prompt string) (bool, error) {
			answer := strings.TrimSpace(readLine(prompt))
			return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), nil
		}
	}
	if deps.Warn == nil {
		deps.Warn = io.Discard
	}
	if deps.Lstat == nil {
		deps.Lstat = os.Lstat
	}
	return deps
}

func isGlobalHooksPathLookup(args []string) bool {
	return len(args) == 4 && args[0] == "config" && args[1] == "--global" &&
		args[2] == "--get" && args[3] == "core.hooksPath"
}

func defaultGitHooksDataHome(home string) string {
	if value := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); value != "" {
		return value
	}
	if runtime.GOOS == "windows" {
		if value := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); value != "" {
			return value
		}
	}
	return filepath.Join(home, ".local", "share")
}

func gitHooksLocation(deps gitHooksDeps) (string, string) {
	directory := strings.TrimSpace(os.Getenv(gitHooksDirectoryEnv))
	if directory == "" {
		directory = filepath.Join(deps.DataHome, "qubership", "pre-commit-global")
	}
	repository := strings.TrimSpace(os.Getenv(gitHooksRepositoryEnv))
	if repository == "" {
		repository = defaultGitHooksRepository
	}
	return directory, repository
}

func requireGitHookPrerequisites(ctx context.Context, deps gitHooksDeps, nonInteractive bool) (string, error) {
	git, firstErr := checkGitHookPrerequisites(ctx, deps)
	if firstErr == nil {
		return git, nil
	}
	if nonInteractive {
		return "", fmt.Errorf("required tools are missing: %w", firstErr)
	}
	confirmed, err := deps.Confirm("Install or update the required tools in another terminal. Have you installed or updated them? [y/N] ")
	if err != nil {
		return "", fmt.Errorf("cannot read prerequisite confirmation: %w", err)
	}
	if !confirmed {
		return "", fmt.Errorf("prerequisite installation was not confirmed: %w", firstErr)
	}
	git, secondErr := checkGitHookPrerequisites(ctx, deps)
	if secondErr != nil {
		return "", fmt.Errorf("required tools are still missing: %w", secondErr)
	}
	return git, nil
}

func checkGitHookPrerequisites(ctx context.Context, deps gitHooksDeps) (string, error) {
	git, gitErr := deps.LookPath("git")
	java, javaLookupErr := deps.LookPath("java")
	var failures []error
	if gitErr != nil {
		failures = append(failures, errors.New("git-hooks component requires Git; install it from https://git-scm.com/install/"))
	}
	if javaLookupErr != nil {
		failures = append(failures, fmt.Errorf("git-hooks component requires Java %d or newer; install a supported JRE or JDK", minimumJavaMajor))
	} else {
		settings, err := deps.Run(ctx, java, "-XshowSettings:properties", "-version")
		major, parseErr := parseJavaSpecificationMajor(settings)
		switch {
		case err != nil || parseErr != nil:
			failures = append(failures, fmt.Errorf("could not determine the Java version; git-hooks component requires Java %d or newer", minimumJavaMajor))
		case major < minimumJavaMajor:
			failures = append(failures, fmt.Errorf("detected Java %d; git-hooks component requires Java %d or newer", major, minimumJavaMajor))
		}
	}
	return git, errors.Join(failures...)
}

func parseJavaSpecificationMajor(settings string) (int, error) {
	value := ""
	for _, line := range strings.Split(settings, "\n") {
		keyValue := strings.SplitN(line, "=", 2)
		if len(keyValue) == 2 && strings.TrimSpace(keyValue[0]) == "java.specification.version" {
			value = strings.TrimSpace(keyValue[1])
			break
		}
	}
	value = strings.TrimPrefix(value, "1.")
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, errors.New("java.specification.version is missing or invalid")
	}
	major, err := strconv.Atoi(value[:end])
	if err != nil || major <= 0 {
		return 0, errors.New("java.specification.version is invalid")
	}
	return major, nil
}

func validateGitHooksRepository(
	ctx context.Context,
	deps gitHooksDeps,
	git, repositoryDir, repositoryURL string,
) error {
	inside, err := deps.Run(ctx, git, "-C", repositoryDir, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return fmt.Errorf("%s is not the managed Git repository", repositoryDir)
	}
	worktreeRoot, err := deps.Run(ctx, git, "-C", repositoryDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("cannot read the Git hooks worktree root: %w", err)
	}
	actualRoot, actualErr := canonicalGitHooksPath(strings.TrimSpace(worktreeRoot), deps.Home)
	expectedRoot, expectedErr := canonicalGitHooksPath(repositoryDir, deps.Home)
	if actualErr != nil || expectedErr != nil || !equalGitHooksPaths(actualRoot, expectedRoot) {
		return fmt.Errorf("git hooks worktree root %q does not match managed repository %q", strings.TrimSpace(worktreeRoot), repositoryDir)
	}
	origin, err := deps.Run(ctx, git, "-C", repositoryDir, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("cannot read the Git hooks repository origin: %w", err)
	}
	if strings.TrimSpace(origin) != repositoryURL {
		return errors.New("git hooks repository origin does not match the configured managed repository")
	}
	status, err := deps.Run(ctx, git, "-C", repositoryDir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("cannot inspect the Git hooks repository status: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("git hooks repository has local changes; refusing to activate, update, or remove it")
	}
	hooksDir := filepath.Join(repositoryDir, "hooks-global")
	info, err := os.Stat(hooksDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("hooks-global was not found in %s", repositoryDir)
	}
	return nil
}

func validateGitHooksUpdateTarget(
	ctx context.Context,
	deps gitHooksDeps,
	git, repositoryDir string,
) (string, error) {
	branchOutput, err := deps.Run(ctx, git, "-C", repositoryDir, "symbolic-ref", "--quiet", "--short", "HEAD")
	branch := strings.TrimSpace(branchOutput)
	if err != nil || branch == "" {
		return "", errors.New("current Git hooks branch is detached or unavailable; check out a branch that tracks origin")
	}
	remoteOutput, err := deps.Run(ctx, git, "-C", repositoryDir, "config", "--get", "branch."+branch+".remote")
	if err != nil {
		return "", fmt.Errorf("cannot read the current branch upstream remote: %w", err)
	}
	if strings.TrimSpace(remoteOutput) != "origin" {
		return "", errors.New("current branch upstream remote is not the verified origin; set its upstream to origin before updating")
	}
	mergeOutput, err := deps.Run(ctx, git, "-C", repositoryDir, "config", "--get", "branch."+branch+".merge")
	if err != nil {
		return "", fmt.Errorf("cannot read the current branch upstream merge ref: %w", err)
	}
	mergeRef := strings.TrimSpace(mergeOutput)
	if !strings.HasPrefix(mergeRef, "refs/heads/") || mergeRef == "refs/heads/" {
		return "", errors.New("current branch upstream merge ref is not a branch on the verified origin")
	}
	if _, err := deps.Run(ctx, git, "-C", repositoryDir, "check-ref-format", mergeRef); err != nil {
		return "", fmt.Errorf("current branch upstream merge ref is invalid: %w", err)
	}
	return mergeRef, nil
}

func readGlobalHooksPath(ctx context.Context, deps gitHooksDeps, git string) (string, error) {
	output, err := deps.Run(ctx, git, "config", "--global", "--get", "core.hooksPath")
	if errors.Is(err, errGitConfigKeyNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func replaceableGlobalHooksPath(
	ctx context.Context,
	deps gitHooksDeps,
	git, desired string,
	force bool,
) (string, error) {
	current, err := readGlobalHooksPath(ctx, deps, git)
	if err != nil {
		return "", fmt.Errorf("cannot read global core.hooksPath: %w", err)
	}
	if current != "" && !sameGitHooksPath(current, desired, deps.Home) && !force {
		return "", errors.New("core.hooksPath is unrelated; rerun with --force-git-hooks to replace it")
	}
	return current, nil
}

func requireFreshOwnedHooksPath(ctx context.Context, deps gitHooksDeps, git, desired string) error {
	current, err := readGlobalHooksPath(ctx, deps, git)
	if err != nil {
		return fmt.Errorf("cannot read global core.hooksPath: %w", err)
	}
	if !sameGitHooksPath(current, desired, deps.Home) {
		return errors.New("global core.hooksPath no longer proves managed ownership; preserve it and rerun uninstall")
	}
	return nil
}

func sameGitHooksPath(current, desired, home string) bool {
	if strings.TrimSpace(current) == "" {
		return false
	}
	currentPath, currentErr := canonicalGitHooksPath(current, home)
	desiredPath, desiredErr := canonicalGitHooksPath(desired, home)
	if currentErr != nil || desiredErr != nil {
		return false
	}
	return equalGitHooksPaths(currentPath, desiredPath)
}

func equalGitHooksPaths(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func canonicalGitHooksPath(path, home string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" {
		path = home
	} else if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("relative hooks path %q is not ownership evidence", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}
	return filepath.Clean(abs), nil
}

func gitHooksRepositoryExists(deps gitHooksDeps, path string) (bool, error) {
	info, err := deps.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("managed Git hooks repository path %s is a symbolic link; ownership is ambiguous", path)
		}
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("cannot inspect Git hooks repository %s: %w", path, err)
}

func warnMissingCyberFerretPassword(warnings io.Writer) {
	if strings.TrimSpace(os.Getenv("CYBER_FERRET_PASSWORD")) != "" {
		return
	}
	_, _ = fmt.Fprintln(warnings, "CYBER_FERRET_PASSWORD is not set; CyberFerret checks require it.")
	_, _ = fmt.Fprintln(warnings, "Set it in the environment that runs Git.")
	_, _ = fmt.Fprintln(warnings, "See docs/lifecycle-installer.md#git-and-java-prerequisites for setup guidance.")
}

func gitCommandError(output string, err error) error {
	diagnostic, truncated := limitAPMDiagnostic(output)
	if diagnostic == "" {
		return err
	}
	message := fmt.Sprintf("%v: %s", err, diagnostic)
	if truncated {
		message += "\n[git output truncated]"
	}
	return errors.New(message)
}

func gitHooksFailure(detail string, err error) operationResult {
	return operationResult{Name: string(componentGitHooks), State: operationFailed, Detail: detail, Err: err}
}
