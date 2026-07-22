package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestJava21PreflightParsesSupportedAndUnsupportedVersions(t *testing.T) {
	tests := []struct {
		name      string
		settings  string
		wantError string
	}{
		{name: "Java 20", settings: "    java.specification.version = 20\n", wantError: "detected Java 20"},
		{name: "Java 21", settings: "Property settings:\n    java.specification.version = 21\n"},
		{name: "Java 22", settings: "    java.specification.version = 22.0.1\n"},
		{name: "legacy Java", settings: "    java.specification.version = 1.8\n", wantError: "detected Java 8"},
		{name: "missing property", settings: "java.vendor = Example\n", wantError: "could not determine"},
		{name: "malformed property", settings: "java.specification.version = current\n", wantError: "could not determine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newGitHooksFixture(t)
			fixture.javaSettings = tt.settings
			component := newGitHooksComponent(fixture.deps())
			err := component.Preflight(context.Background(), lifecycleOptions{
				Action: actionInstall, NonInteractive: true,
			})
			if tt.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) || !strings.Contains(err.Error(), "Java 21 or newer") {
				t.Fatalf("Preflight() error = %v, want %q and Java guidance", err, tt.wantError)
			}
		})
	}
}

func TestJava21PreflightRejectsFailedJavaCommand(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.javaErr = errors.New("java failed")
	component := newGitHooksComponent(fixture.deps())
	err := component.Preflight(context.Background(), lifecycleOptions{Action: actionInstall, NonInteractive: true})
	if err == nil || !strings.Contains(err.Error(), "could not determine") {
		t.Fatalf("Preflight() error = %v, want version detection failure", err)
	}
}

func TestGitHooksComponentPreflightRejectsMissingGitWithoutPromptingNoninteractive(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.gitMissing = true
	fixture.confirm = func(string) (bool, error) {
		t.Fatal("Confirm called in noninteractive mode")
		return false, nil
	}
	component := newGitHooksComponent(fixture.deps())
	err := component.Preflight(context.Background(), lifecycleOptions{Action: actionInstall, NonInteractive: true})
	if err == nil || !strings.Contains(err.Error(), "requires Git") {
		t.Fatalf("Preflight() error = %v, want missing Git failure", err)
	}
}

func TestGitHooksComponentPreflightRetriesCompleteCheckExactlyOnce(t *testing.T) {
	fixture := newGitHooksFixture(t)
	gitLookups := 0
	confirmations := 0
	deps := fixture.deps()
	deps.LookPath = func(name string) (string, error) {
		if name == "git" {
			gitLookups++
			if gitLookups == 1 {
				return "", errors.New("not found")
			}
		}
		return "/tools/" + name, nil
	}
	deps.Confirm = func(prompt string) (bool, error) {
		confirmations++
		if !strings.Contains(prompt, "installed or updated") {
			t.Fatalf("prompt = %q, want install-or-update wording", prompt)
		}
		return true, nil
	}
	component := newGitHooksComponent(deps)
	if err := component.Preflight(context.Background(), lifecycleOptions{Action: actionInstall}); err != nil {
		t.Fatal(err)
	}
	if gitLookups != 2 || confirmations != 1 || fixture.javaRuns != 2 {
		t.Fatalf("git lookups = %d, confirmations = %d, Java checks = %d; want 2, 1, 2", gitLookups, confirmations, fixture.javaRuns)
	}
}

func TestGitHooksComponentPreflightStopsAfterOneFailedRetry(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.gitMissing = true
	confirmations := 0
	fixture.confirm = func(string) (bool, error) { confirmations++; return true, nil }
	component := newGitHooksComponent(fixture.deps())
	err := component.Preflight(context.Background(), lifecycleOptions{Action: actionInstall})
	if err == nil || !strings.Contains(err.Error(), "still missing") {
		t.Fatalf("Preflight() error = %v, want failed recheck", err)
	}
	if confirmations != 1 {
		t.Fatalf("confirmations = %d, want exactly 1", confirmations)
	}
}

func TestGitHooksComponentPreflightStopsAfterNegativeConfirmation(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.gitMissing = true
	confirmations := 0
	fixture.confirm = func(string) (bool, error) { confirmations++; return false, nil }
	component := newGitHooksComponent(fixture.deps())
	err := component.Preflight(context.Background(), lifecycleOptions{Action: actionInstall})
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("Preflight() error = %v, want user-aborted prerequisite failure", err)
	}
	if confirmations != 1 {
		t.Fatalf("confirmations = %d, want exactly 1", confirmations)
	}
}

func TestGitHooksComponentSkippedSelectionDoesNotCheckPrerequisites(t *testing.T) {
	component := newGitHooksComponent(gitHooksDeps{
		LookPath: func(string) (string, error) {
			t.Fatal("git-hooks prerequisite checked for an unselected component")
			return "", nil
		},
	})
	summary := runLifecycle(context.Background(), lifecycleOptions{
		Action: actionInstall, Components: []componentName{componentAPM}, Harnesses: []hookTarget{hookCodex},
	}, lifecycleDeps{Components: map[componentName]componentOps{
		componentAPM:      {},
		componentGitHooks: component,
	}})
	if summary.Err != nil {
		t.Fatal(summary.Err)
	}
}

func TestGitHooksComponentInstallClonesValidatesConfiguresAndWarns(t *testing.T) {
	t.Setenv("CYBER_FERRET_PASSWORD", "")
	fixture := newGitHooksFixture(t)
	var warnings bytes.Buffer
	deps := fixture.deps()
	deps.Warn = &warnings
	component := newGitHooksComponent(deps)
	opts := lifecycleOptions{Action: actionInstall, NonInteractive: true}
	if err := component.Preflight(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	result := component.Install(context.Background(), opts)
	if result.State != operationOK {
		t.Fatalf("Install() = %#v", result)
	}
	if !sameGitHooksPath(fixture.configuredPath, filepath.Join(fixture.repoDir, "hooks-global"), fixture.home) {
		t.Fatalf("core.hooksPath = %q", fixture.configuredPath)
	}
	assertCommandContains(t, fixture.calls, "/tools/git clone https://github.com/exadmin/pre-commit-global.git "+fixture.repoDir)
	assertCommandContains(t, fixture.calls, "/tools/git -C "+fixture.repoDir+" status --porcelain --untracked-files=all")
	if !strings.Contains(warnings.String(), "CYBER_FERRET_PASSWORD") || !strings.Contains(warnings.String(), "docs/lifecycle-installer.md#git-and-java-prerequisites") {
		t.Fatalf("warning = %q, want password setup guidance", warnings.String())
	}
}

func TestGitHooksComponentPreflightValidatesExistingRepositoryReadOnly(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*gitHooksFixture)
		want      string
	}{
		{name: "origin", configure: func(f *gitHooksFixture) { f.origin = "https://example.test/fork.git" }, want: "origin does not match"},
		{name: "dirty", configure: func(f *gitHooksFixture) { f.dirty = " M hooks-global/pre-commit" }, want: "local changes"},
		{name: "worktree", configure: func(f *gitHooksFixture) { f.insideWorktree = false }, want: "not the managed Git repository"},
		{name: "nested worktree", configure: func(f *gitHooksFixture) { f.worktreeRoot = filepath.Dir(f.repoDir) }, want: "worktree root"},
		{name: "hooks directory", configure: func(f *gitHooksFixture) { _ = os.RemoveAll(filepath.Join(f.repoDir, "hooks-global")) }, want: "hooks-global was not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newGitHooksFixture(t)
			fixture.createRepository(t)
			tt.configure(fixture)
			component := newGitHooksComponent(fixture.deps())
			err := component.Preflight(context.Background(), lifecycleOptions{Action: actionInstall, NonInteractive: true})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Preflight() error = %v, want %q", err, tt.want)
			}
			for _, call := range fixture.calls {
				if strings.Contains(call, " clone ") || strings.Contains(call, " config --global core.hooksPath ") {
					t.Fatalf("preflight mutated state: %q", call)
				}
			}
		})
	}
}

func TestGitHooksComponentPreservesUnrelatedHooksPathUnlessForced(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%t", force), func(t *testing.T) {
			fixture := newGitHooksFixture(t)
			fixture.configuredPath = filepath.Join(t.TempDir(), "other-hooks")
			component := newGitHooksComponent(fixture.deps())
			opts := lifecycleOptions{Action: actionInstall, NonInteractive: true, ForceGitHooks: force}
			if err := component.Preflight(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			result := component.Install(context.Background(), opts)
			if force {
				if result.State != operationOK || !sameGitHooksPath(fixture.configuredPath, filepath.Join(fixture.repoDir, "hooks-global"), fixture.home) {
					t.Fatalf("forced Install() = %#v, path = %q", result, fixture.configuredPath)
				}
			} else if result.State != operationSkipped || sameGitHooksPath(fixture.configuredPath, filepath.Join(fixture.repoDir, "hooks-global"), fixture.home) {
				t.Fatalf("preserving Install() = %#v, path = %q", result, fixture.configuredPath)
			}
		})
	}
}

func TestGitHooksComponentRechecksUnrelatedHooksPathImmediatelyBeforeConfiguration(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%t", force), func(t *testing.T) {
			fixture := newGitHooksFixture(t)
			fixture.createRepository(t)
			unrelated := filepath.Join(t.TempDir(), "other-hooks")
			fixture.configReadValues = []string{"", unrelated}
			component := newGitHooksComponent(fixture.deps())
			opts := lifecycleOptions{Action: actionInstall, NonInteractive: true, ForceGitHooks: force}
			if err := component.Preflight(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			result := component.Install(context.Background(), opts)
			if force {
				if result.State != operationOK || !sameGitHooksPath(fixture.configuredPath, filepath.Join(fixture.repoDir, "hooks-global"), fixture.home) {
					t.Fatalf("forced Install() = %#v, path = %q", result, fixture.configuredPath)
				}
				return
			}
			if result.State != operationFailed || result.Err == nil || !strings.Contains(result.Detail, "core.hooksPath") {
				t.Fatalf("Install() = %#v, want TOCTOU conflict failure", result)
			}
			if fixture.configuredPath != unrelated {
				t.Fatalf("core.hooksPath = %q, want unrelated path preserved", fixture.configuredPath)
			}
			for _, call := range fixture.calls {
				const prefix = "/tools/git config --global core.hooksPath "
				if strings.HasPrefix(call, prefix) && sameGitHooksPath(strings.TrimPrefix(call, prefix), filepath.Join(fixture.repoDir, "hooks-global"), fixture.home) {
					t.Fatalf("TOCTOU conflict was overwritten: %q", call)
				}
			}
		})
	}
}

func TestGitHooksComponentRechecksHooksPathBeforeFirstApplyMutation(t *testing.T) {
	for _, action := range []lifecycleAction{actionInstall, actionUpdate} {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force=%t", action, force), func(t *testing.T) {
				fixture := newGitHooksFixture(t)
				if action == actionUpdate {
					fixture.createRepository(t)
				}
				unrelated := filepath.Join(t.TempDir(), "other-hooks")
				fixture.configReadValues = []string{"", unrelated, unrelated}
				component := newGitHooksComponent(fixture.deps())
				opts := lifecycleOptions{Action: action, NonInteractive: true, ForceGitHooks: force}
				if err := component.Preflight(context.Background(), opts); err != nil {
					t.Fatal(err)
				}
				var result operationResult
				if action == actionInstall {
					result = component.Install(context.Background(), opts)
				} else {
					result = component.Update(context.Background(), opts)
				}
				if force {
					if result.State != operationOK || !sameGitHooksPath(fixture.configuredPath, filepath.Join(fixture.repoDir, "hooks-global"), fixture.home) {
						t.Fatalf("forced apply = %#v, path = %q", result, fixture.configuredPath)
					}
					return
				}
				if result.State != operationFailed || result.Err == nil || !strings.Contains(result.Detail, "core.hooksPath") {
					t.Fatalf("apply = %#v, want pre-mutation drift failure", result)
				}
				if fixture.configuredPath != unrelated {
					t.Fatalf("core.hooksPath = %q, want unrelated path preserved", fixture.configuredPath)
				}
				for _, call := range fixture.calls {
					if strings.Contains(call, " clone ") || strings.Contains(call, " pull ") ||
						strings.Contains(call, " config --global core.hooksPath ") {
						t.Fatalf("mutation ran after hooksPath drift: %q", call)
					}
				}
			})
		}
	}
}

func TestGitHooksComponentTreatsCanonicalOwnedHooksPathAsEquivalent(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.createRepository(t)
	dataLink := filepath.Join(t.TempDir(), "linked-data")
	if err := os.Symlink(fixture.dataHome, dataLink); err != nil {
		t.Fatal(err)
	}
	fixture.configuredPath = filepath.Join(dataLink, "qubership", "pre-commit-global", "hooks-global")
	component := newGitHooksComponent(fixture.deps())
	opts := lifecycleOptions{Action: actionInstall, NonInteractive: true}
	if err := component.Preflight(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	result := component.Install(context.Background(), opts)
	if result.State != operationOK {
		t.Fatalf("Install() = %#v", result)
	}
	for _, call := range fixture.calls {
		if strings.Contains(call, "config --global core.hooksPath") {
			t.Fatalf("equivalent hooksPath was rewritten: %q", call)
		}
	}
}

func TestGitHooksComponentUpdateFetchesVerifiedCurrentBranchUpstreamAndFastForwards(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.createRepository(t)
	component := newGitHooksComponent(fixture.deps())
	opts := lifecycleOptions{Action: actionUpdate, NonInteractive: true}
	if err := component.Preflight(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	result := component.Update(context.Background(), opts)
	if result.State != operationOK {
		t.Fatalf("Update() = %#v", result)
	}
	fetch := "/tools/git -C " + fixture.repoDir + " fetch --no-tags origin refs/heads/main"
	merge := "/tools/git -C " + fixture.repoDir + " merge --ff-only FETCH_HEAD"
	assertCommandContains(t, fixture.calls, fetch)
	assertCommandContains(t, fixture.calls, merge)
	if commandIndex(fixture.calls, fetch) >= commandIndex(fixture.calls, merge) {
		t.Fatalf("commands = %q, want verified fetch before fast-forward merge", fixture.calls)
	}
	for _, call := range fixture.calls {
		if strings.Contains(call, " pull ") {
			t.Fatalf("update used implicit pull: %q", call)
		}
	}
}

func TestGitHooksComponentUpdateRejectsCurrentBranchTrackingAnotherRemoteBeforeFetch(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.createRepository(t)
	fixture.upstreamRemote = "fork"
	component := newGitHooksComponent(fixture.deps())
	opts := lifecycleOptions{Action: actionUpdate, NonInteractive: true}

	err := component.Preflight(context.Background(), opts)

	if err == nil || !strings.Contains(err.Error(), "current branch upstream remote") ||
		!strings.Contains(err.Error(), "origin") {
		t.Fatalf("Preflight() error = %v, want verified-origin upstream failure", err)
	}
	for _, call := range fixture.calls {
		if strings.Contains(call, " fetch ") || strings.Contains(call, " merge ") || strings.Contains(call, " pull ") {
			t.Fatalf("repository content changed after upstream rejection: %q", call)
		}
	}
}

func TestGitHooksComponentUpdateRejectsNonBranchUpstreamMergeRefBeforeFetch(t *testing.T) {
	tests := []struct {
		name, mergeRef string
		valid          bool
	}{
		{name: "tag", mergeRef: "refs/tags/v1.0.0", valid: true},
		{name: "refspec", mergeRef: "refs/heads/main:refs/heads/unrelated", valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newGitHooksFixture(t)
			fixture.createRepository(t)
			fixture.upstreamMergeRef = tt.mergeRef
			fixture.upstreamMergeRefValid = tt.valid
			component := newGitHooksComponent(fixture.deps())
			opts := lifecycleOptions{Action: actionUpdate, NonInteractive: true}

			err := component.Preflight(context.Background(), opts)

			if err == nil || !strings.Contains(err.Error(), "current branch upstream merge ref") {
				t.Fatalf("Preflight() error = %v, want branch merge-ref failure", err)
			}
			for _, call := range fixture.calls {
				if strings.Contains(call, " fetch ") || strings.Contains(call, " merge ") || strings.Contains(call, " pull ") {
					t.Fatalf("repository content changed after merge-ref rejection: %q", call)
				}
			}
		})
	}
}

func TestGitHooksComponentUpdateInstallsMissingCloneWithoutFetch(t *testing.T) {
	fixture := newGitHooksFixture(t)
	component := newGitHooksComponent(fixture.deps())
	opts := lifecycleOptions{Action: actionUpdate, NonInteractive: true}
	if err := component.Preflight(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	result := component.Update(context.Background(), opts)
	if result.State != operationOK {
		t.Fatalf("Update() = %#v", result)
	}
	assertCommandContains(t, fixture.calls, "/tools/git clone "+defaultGitHooksRepository+" "+fixture.repoDir)
	for _, call := range fixture.calls {
		if strings.Contains(call, " fetch ") || strings.Contains(call, " merge ") || strings.Contains(call, " pull ") {
			t.Fatalf("missing-clone update fetched after cloning: %q", call)
		}
	}
}

func TestGitHooksComponentRejectsOperationalConfigReadErrorsBeforeInstallMutation(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.configReadErr = errors.New("permission denied")
	component := newGitHooksComponent(fixture.deps())
	err := component.Preflight(context.Background(), lifecycleOptions{Action: actionInstall, NonInteractive: true})
	if err == nil || !strings.Contains(err.Error(), "core.hooksPath") {
		t.Fatalf("Preflight() error = %v, want config read failure", err)
	}
	assertNoGitHooksMutation(t, fixture.calls)
	if _, err := os.Stat(fixture.repoDir); !os.IsNotExist(err) {
		t.Fatalf("repository changed after config read failure: %v", err)
	}
}

func TestGitHooksComponentRejectsOperationalConfigReadErrorsBeforeUninstallMutation(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.createRepository(t)
	fixture.configuredPath = filepath.Join(fixture.repoDir, "hooks-global")
	fixture.configReadErr = errors.New("malformed config")
	component := newGitHooksComponent(fixture.deps())
	result := component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
	if result.State != operationFailed || result.Err == nil || !strings.Contains(result.Detail, "core.hooksPath") {
		t.Fatalf("Uninstall() = %#v, want config read failure", result)
	}
	if _, err := os.Stat(fixture.repoDir); err != nil {
		t.Fatalf("repository removed after config read failure: %v", err)
	}
	if fixture.configuredPath == "" {
		t.Fatal("configuration cleared after config read failure")
	}
}

func TestGitHooksComponentRejectsAmbiguousRepositoryStateBeforeMutation(t *testing.T) {
	for _, action := range []lifecycleAction{actionInstall, actionUpdate, actionUninstall} {
		t.Run(string(action), func(t *testing.T) {
			fixture := newGitHooksFixture(t)
			fixture.lstatErr = errors.New("permission denied")
			if action == actionUninstall {
				fixture.createRepository(t)
				fixture.configuredPath = filepath.Join(fixture.repoDir, "hooks-global")
			}
			component := newGitHooksComponent(fixture.deps())
			opts := lifecycleOptions{Action: action, NonInteractive: true}
			if action == actionUninstall {
				result := component.Uninstall(context.Background(), opts)
				if result.State != operationFailed || result.Err == nil || !strings.Contains(result.Err.Error(), "permission denied") {
					t.Fatalf("Uninstall() = %#v, want ambiguous repository failure", result)
				}
				if fixture.configuredPath == "" {
					t.Fatal("owned hooksPath was cleared after ambiguous clone inspection")
				}
				if _, err := os.Stat(fixture.repoDir); err != nil {
					t.Fatalf("repository changed after ambiguous inspection: %v", err)
				}
				return
			}
			err := component.Preflight(context.Background(), opts)
			if err == nil || !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("Preflight() error = %v, want ambiguous repository failure", err)
			}
			assertNoGitHooksMutation(t, fixture.calls)
		})
	}
}

func TestGitHooksComponentBoundsFailedGitCommandOutput(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.createRepository(t)
	fixture.fetchOutput = strings.Repeat("x", (4<<10)+100)
	fixture.fetchErr = errors.New("exit status 1")
	component := newGitHooksComponent(fixture.deps())
	opts := lifecycleOptions{Action: actionUpdate, NonInteractive: true}
	if err := component.Preflight(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	result := component.Update(context.Background(), opts)
	if result.State != operationFailed || result.Err == nil {
		t.Fatalf("Update() = %#v, want fetch failure", result)
	}
	message := result.Err.Error()
	if !strings.Contains(message, "[git output truncated]") || strings.Contains(message, strings.Repeat("x", (4<<10)+1)) {
		t.Fatalf("unbounded or unmarked Git diagnostic: %q", message)
	}
}

func TestGitHooksComponentDoesNotExposeOriginCredentials(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.createRepository(t)
	secret := "super-secret-token"
	fixture.origin = "https://user:" + secret + "@example.test/hooks.git"
	component := newGitHooksComponent(fixture.deps())
	err := component.Preflight(context.Background(), lifecycleOptions{Action: actionInstall, NonInteractive: true})
	if err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("Preflight() error = %v, want origin mismatch", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "user:") {
		t.Fatalf("origin mismatch exposed credentials: %q", err)
	}
}

func TestGitHooksComponentUninstallRemovesOnlyCleanOwnedRepository(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.createRepository(t)
	fixture.configuredPath = filepath.Join(fixture.repoDir, "hooks-global")
	component := newGitHooksComponent(fixture.deps())
	result := component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
	if result.State != operationOK {
		t.Fatalf("Uninstall() = %#v", result)
	}
	if fixture.configuredPath != "" {
		t.Fatalf("core.hooksPath = %q, want unset", fixture.configuredPath)
	}
	if _, err := os.Stat(fixture.repoDir); !os.IsNotExist(err) {
		t.Fatalf("repository still exists: %v", err)
	}
	result = component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
	if result.State != operationSkipped {
		t.Fatalf("repeated Uninstall() = %#v, want SKIPPED", result)
	}
}

func TestGitHooksComponentRechecksOwnedHooksPathImmediatelyBeforeUnset(t *testing.T) {
	for _, repositoryPresent := range []bool{false, true} {
		for _, drift := range []string{"unrelated", "relative", "read failure"} {
			t.Run(fmt.Sprintf("repository=%t/%s", repositoryPresent, drift), func(t *testing.T) {
				fixture := newGitHooksFixture(t)
				if repositoryPresent {
					fixture.createRepository(t)
				}
				owned := filepath.Join(fixture.repoDir, "hooks-global")
				fixture.configReadValues = []string{owned}
				switch drift {
				case "unrelated":
					fixture.configReadValues = append(fixture.configReadValues, filepath.Join(t.TempDir(), "other-hooks"))
				case "relative":
					fixture.configReadValues = append(fixture.configReadValues, "relative/hooks")
				case "read failure":
					fixture.configReadErr = errors.New("permission denied")
					fixture.configReadErrAt = 2
				}
				component := newGitHooksComponent(fixture.deps())
				result := component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
				if result.State != operationFailed || result.Err == nil || !strings.Contains(result.Detail, "core.hooksPath") {
					t.Fatalf("Uninstall() = %#v, want fresh ownership failure", result)
				}
				for _, call := range fixture.calls {
					if strings.Contains(call, " config --global --unset ") {
						t.Fatalf("drifted hooksPath was unset: %q", call)
					}
				}
				if repositoryPresent {
					if _, err := os.Stat(fixture.repoDir); err != nil {
						t.Fatalf("repository deleted after hooksPath drift: %v", err)
					}
				}
			})
		}
	}
}

func TestGitHooksComponentClearsReverseDriftBeforeDeletingRepository(t *testing.T) {
	for _, initial := range []string{"", filepath.Join(t.TempDir(), "unrelated-hooks")} {
		t.Run(fmt.Sprintf("initial=%q", initial), func(t *testing.T) {
			fixture := newGitHooksFixture(t)
			fixture.createRepository(t)
			owned := filepath.Join(fixture.repoDir, "hooks-global")
			fixture.configReadValues = []string{initial, owned, owned}
			component := newGitHooksComponent(fixture.deps())

			result := component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})

			if result.State != operationOK {
				t.Fatalf("Uninstall() = %#v, want reverse drift cleared", result)
			}
			if fixture.configuredPath != "" {
				t.Fatalf("core.hooksPath = %q, want unset before repository deletion", fixture.configuredPath)
			}
			if fixture.configReads < 3 {
				t.Fatalf("core.hooksPath reads = %d, want reverse-drift read and ownership recheck", fixture.configReads)
			}
			if _, err := os.Stat(fixture.repoDir); !os.IsNotExist(err) {
				t.Fatalf("repository still exists: %v", err)
			}
		})
	}
}

func TestGitHooksComponentUninstallPreservesModifiedOrAmbiguousClone(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*gitHooksFixture)
	}{
		{name: "modified", configure: func(f *gitHooksFixture) { f.dirty = "?? local.txt" }},
		{name: "origin", configure: func(f *gitHooksFixture) { f.origin = "https://example.test/fork.git" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newGitHooksFixture(t)
			fixture.createRepository(t)
			fixture.configuredPath = filepath.Join(fixture.repoDir, "hooks-global")
			tt.configure(fixture)
			component := newGitHooksComponent(fixture.deps())
			result := component.Uninstall(context.Background(), lifecycleOptions{Action: actionUninstall})
			if result.State != operationFailed || result.Err == nil {
				t.Fatalf("Uninstall() = %#v, want ownership failure", result)
			}
			if _, err := os.Stat(fixture.repoDir); err != nil {
				t.Fatalf("repository was not preserved: %v", err)
			}
			if fixture.configuredPath == "" {
				t.Fatal("owned hooksPath was cleared before clone ownership was proven")
			}
		})
	}
}

func TestGitHooksComponentRejectsLeafSymlinkAtManagedRepositoryPath(t *testing.T) {
	for _, action := range []lifecycleAction{actionInstall, actionUpdate, actionUninstall} {
		t.Run(string(action), func(t *testing.T) {
			fixture := newGitHooksFixture(t)
			target := filepath.Join(t.TempDir(), "actual-repository")
			if err := os.MkdirAll(filepath.Join(target, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(target, "hooks-global"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(fixture.repoDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, fixture.repoDir); err != nil {
				t.Fatal(err)
			}
			fixture.worktreeRoot = target
			if action == actionUninstall {
				fixture.configuredPath = filepath.Join(fixture.repoDir, "hooks-global")
			}
			component := newGitHooksComponent(fixture.deps())
			opts := lifecycleOptions{Action: action, NonInteractive: true}
			if action == actionUninstall {
				result := component.Uninstall(context.Background(), opts)
				if result.State != operationFailed || result.Err == nil || !strings.Contains(result.Err.Error(), "symbolic link") {
					t.Fatalf("Uninstall() = %#v, want leaf-symlink ownership failure", result)
				}
				if fixture.configuredPath == "" {
					t.Fatal("owned hooksPath was cleared for a leaf symlink")
				}
			} else {
				err := component.Preflight(context.Background(), opts)
				if err == nil || !strings.Contains(err.Error(), "symbolic link") {
					t.Fatalf("Preflight() error = %v, want leaf-symlink ownership failure", err)
				}
			}
			info, err := os.Lstat(fixture.repoDir)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("managed leaf symlink changed: info=%v err=%v", info, err)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("symlink target changed: %v", err)
			}
			assertNoGitHooksMutation(t, fixture.calls)
		})
	}
}

func TestGitHooksComponentUsesEnvironmentOverrides(t *testing.T) {
	overrideDir := filepath.Join(t.TempDir(), "custom-hooks")
	t.Setenv("QUBERSHIP_DEV_GIT_HOOKS_DIR", overrideDir)
	t.Setenv("QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY", "https://example.test/hooks.git")
	fixture := newGitHooksFixture(t)
	fixture.repoDir = overrideDir
	fixture.worktreeRoot = overrideDir
	fixture.origin = "https://example.test/hooks.git"
	component := newGitHooksComponent(fixture.deps())
	opts := lifecycleOptions{Action: actionInstall, NonInteractive: true}
	if err := component.Preflight(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	result := component.Install(context.Background(), opts)
	if result.State != operationOK {
		t.Fatalf("Install() = %#v", result)
	}
	assertCommandContains(t, fixture.calls, "/tools/git clone https://example.test/hooks.git "+overrideDir)
}

type gitHooksFixture struct {
	t                     *testing.T
	home                  string
	dataHome              string
	repoDir               string
	origin                string
	configuredPath        string
	dirty                 string
	insideWorktree        bool
	worktreeRoot          string
	gitMissing            bool
	javaMissing           bool
	javaSettings          string
	javaErr               error
	javaRuns              int
	fetchErr              error
	fetchOutput           string
	currentBranch         string
	upstreamRemote        string
	upstreamMergeRef      string
	upstreamMergeRefValid bool
	configReadErr         error
	configReadErrAt       int
	configReadValues      []string
	configReads           int
	lstatErr              error
	confirm               func(string) (bool, error)
	calls                 []string
}

func newGitHooksFixture(t *testing.T) *gitHooksFixture {
	t.Helper()
	root := t.TempDir()
	dataHome := filepath.Join(root, "data")
	fixture := &gitHooksFixture{
		t: t, home: filepath.Join(root, "home"), dataHome: dataHome,
		repoDir: filepath.Join(dataHome, "qubership", "pre-commit-global"),
		origin:  "https://github.com/exadmin/pre-commit-global.git", insideWorktree: true,
		javaSettings: "    java.specification.version = 21\n", currentBranch: "main",
		upstreamRemote: "origin", upstreamMergeRef: "refs/heads/main", upstreamMergeRefValid: true,
		confirm: func(string) (bool, error) { return false, nil },
	}
	fixture.worktreeRoot = fixture.repoDir
	return fixture
}

func (f *gitHooksFixture) deps() gitHooksDeps {
	return gitHooksDeps{
		Home: f.home, DataHome: f.dataHome,
		LookPath: func(name string) (string, error) {
			if (name == "git" && f.gitMissing) || (name == "java" && f.javaMissing) {
				return "", errors.New("not found")
			}
			return "/tools/" + name, nil
		},
		Run: f.run,
		Lstat: func(path string) (os.FileInfo, error) {
			if path == f.repoDir && f.lstatErr != nil {
				return nil, f.lstatErr
			}
			return os.Lstat(path)
		},
		Confirm: func(prompt string) (bool, error) { return f.confirm(prompt) },
		Warn:    os.Stderr,
	}
}

func (f *gitHooksFixture) run(_ context.Context, name string, args ...string) (string, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	if strings.HasSuffix(name, "java") {
		f.javaRuns++
		if !reflect.DeepEqual(args, []string{"-XshowSettings:properties", "-version"}) {
			f.t.Fatalf("Java args = %q", args)
		}
		return f.javaSettings, f.javaErr
	}
	if reflect.DeepEqual(args, []string{"config", "--global", "--get", "core.hooksPath"}) {
		f.configReads++
		if f.configReadErr != nil && (f.configReadErrAt == 0 || f.configReadErrAt == f.configReads) {
			return "", f.configReadErr
		}
		if f.configReads <= len(f.configReadValues) {
			value := f.configReadValues[f.configReads-1]
			f.configuredPath = value
			if value == "" {
				return "", errGitConfigKeyNotFound
			}
			return value + "\n", nil
		}
		if f.configuredPath == "" {
			return "", errGitConfigKeyNotFound
		}
		return f.configuredPath + "\n", nil
	}
	if len(args) == 4 && reflect.DeepEqual(args[:3], []string{"config", "--global", "core.hooksPath"}) {
		f.configuredPath = args[3]
		return "", nil
	}
	if reflect.DeepEqual(args, []string{"config", "--global", "--unset", "core.hooksPath"}) {
		f.configuredPath = ""
		return "", nil
	}
	if len(args) == 3 && args[0] == "clone" {
		if err := os.MkdirAll(filepath.Join(args[2], ".git"), 0o755); err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Join(args[2], "hooks-global"), 0o755); err != nil {
			return "", err
		}
		return "", nil
	}
	if len(args) >= 3 && args[0] == "-C" {
		switch strings.Join(args[2:], " ") {
		case "rev-parse --is-inside-work-tree":
			if !f.insideWorktree {
				return "false", errors.New("not a worktree")
			}
			return "true\n", nil
		case "rev-parse --show-toplevel":
			return f.worktreeRoot + "\n", nil
		case "remote get-url origin":
			return f.origin + "\n", nil
		case "status --porcelain --untracked-files=all":
			return f.dirty, nil
		case "symbolic-ref --quiet --short HEAD":
			return f.currentBranch + "\n", nil
		case "config --get branch." + f.currentBranch + ".remote":
			return f.upstreamRemote + "\n", nil
		case "config --get branch." + f.currentBranch + ".merge":
			return f.upstreamMergeRef + "\n", nil
		case "check-ref-format " + f.upstreamMergeRef:
			if !f.upstreamMergeRefValid {
				return "", errors.New("invalid ref")
			}
			return "", nil
		case "fetch --no-tags " + f.upstreamRemote + " " + f.upstreamMergeRef:
			return f.fetchOutput, f.fetchErr
		case "merge --ff-only FETCH_HEAD":
			return "", nil
		case "pull --ff-only":
			return f.fetchOutput, f.fetchErr
		}
	}
	return "", fmt.Errorf("unexpected command: %s", call)
}

func (f *gitHooksFixture) createRepository(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.repoDir, "hooks-global"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertCommandContains(t *testing.T, calls []string, want string) {
	t.Helper()
	for _, call := range calls {
		if call == want {
			return
		}
	}
	t.Fatalf("commands = %q, want %q", calls, want)
}

func commandIndex(calls []string, want string) int {
	for index, call := range calls {
		if call == want {
			return index
		}
	}
	return -1
}

func assertNoGitHooksMutation(t *testing.T, calls []string) {
	t.Helper()
	for _, call := range calls {
		if strings.Contains(call, " clone ") || strings.Contains(call, " pull ") ||
			strings.Contains(call, " fetch ") || strings.Contains(call, " merge ") ||
			strings.Contains(call, " config --global core.hooksPath ") || strings.Contains(call, " config --global --unset ") {
			t.Fatalf("unexpected mutation command: %q", call)
		}
	}
}
