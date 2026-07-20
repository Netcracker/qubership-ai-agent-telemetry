package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunVersion(t *testing.T) {
	var out string
	code := run([]string{"version"}, func(s string) { out = s })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if out != version+"\n" {
		t.Fatalf("output = %q, want %q", out, version+"\n")
	}
}

func TestRunHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"help"}, want: "Commands:"},
		{name: "short flag", args: []string{"-h"}, want: "Commands:"},
		{name: "long flag", args: []string{"--help"}, want: "Commands:"},
		{name: "help topic", args: []string{"help", "hooks"}, want: "ai-agent-telemetry hooks install"},
		{name: "command help", args: []string{"hooks", "help"}, want: "--target=<list>"},
		{name: "command short flag", args: []string{"configure", "-h"}, want: "--hooks=<targets>"},
		{name: "command long flag", args: []string{"status", "--help"}, want: "--verbose"},
		{name: "nested action help", args: []string{"hooks", "install", "--help"}, want: "--target=<list>"},
		{name: "nested uninstall help", args: []string{"hooks", "uninstall", "--help"}, want: "ai-agent-telemetry hooks uninstall"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out string
			if code := run(tt.args, func(s string) { out += s }); code != 0 {
				t.Fatalf("exit code = %d, want 0; output = %q", code, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Fatalf("output = %q, want %q", out, tt.want)
			}
		})
	}
}

func TestRunHooksHelpShowsInstallAndUninstallUsage(t *testing.T) {
	var out string
	if code := run([]string{"help", "hooks"}, func(s string) { out += s }); code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	for _, want := range []string{
		"ai-agent-telemetry hooks install [--target=<list>]",
		"ai-agent-telemetry hooks uninstall [--target=<list>]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want %q", out, want)
		}
	}
}

func TestRunConfigureHelpShowsAcceptedValueForms(t *testing.T) {
	var out string
	if code := run([]string{"configure", "--help"}, func(s string) { out += s }); code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	for _, want := range []string{
		"--repo-allow <pattern>",
		"--hooks <targets>",
		"--buffer-cap <events>",
		"--flush-timeout <duration>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want %q", out, want)
		}
	}
}

func TestRunConfigureRejectsInvalidDeliverySettingsWithoutWritingFiles(t *testing.T) {
	tests := [][]string{
		{"configure", "--buffer-cap=0"},
		{"configure", "--flush-timeout=invalid"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			home, configHome := isolateRunConfigure(t)
			var out string
			if code := run(args, func(s string) { out += s }); code != 2 {
				t.Fatalf("exit code = %d, want 2; output = %q", code, out)
			}
			if !strings.Contains(out, "positive") {
				t.Fatalf("output = %q, want validation error", out)
			}
			for _, path := range []string{
				filepath.Join(configHome, pkgName, "env"),
				hookPath(home, hookClaude),
				hookPath(home, hookCodex),
				hookPath(home, hookCursor),
			} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("configuration exists after invalid command at %s: %v", path, err)
				}
			}
		})
	}
}

func TestRunConfigureRejectsExplicitEmptyHooksWithoutWritingFiles(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "equals form", args: []string{"configure", "--endpoint=https://otel.example/v1/logs", "--hooks="}},
		{name: "separate form", args: []string{"configure", "--endpoint=https://otel.example/v1/logs", "--hooks", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home, configHome := isolateRunConfigure(t)
			var out string
			if code := run(tt.args, func(s string) { out += s }); code != 2 {
				t.Fatalf("exit code = %d, want 2; output = %q", code, out)
			}
			if !strings.Contains(out, "must not be empty") {
				t.Fatalf("output = %q, want empty-value error", out)
			}
			for _, path := range []string{
				filepath.Join(configHome, pkgName, "env"),
				hookPath(home, hookClaude),
				hookPath(home, hookCodex),
				hookPath(home, hookCursor),
			} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("configuration exists after invalid command at %s: %v", path, err)
				}
			}
		})
	}
}

func TestRunHelpTopicCoversEveryPublicCommandWithoutSideEffects(t *testing.T) {
	home := t.TempDir()
	configHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	for _, entry := range commandHelpEntries {
		command := entry.Name
		forms := [][]string{
			{"help", command},
			{command, "help"},
			{command, "-h"},
			{command, "--help"},
		}
		for _, args := range forms {
			t.Run(strings.Join(args, "_"), func(t *testing.T) {
				var out string
				if code := run(args, func(s string) { out += s }); code != 0 {
					t.Fatalf("exit code = %d, want 0; output = %q", code, out)
				}
				if !strings.Contains(out, "Usage:") || !strings.Contains(out, "ai-agent-telemetry "+command) {
					t.Fatalf("output = %q, want command usage", out)
				}
			})
		}
	}

	for _, dir := range []string{home, configHome, cacheHome} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("help created files in %s: %v", dir, entries)
		}
	}
}

func TestRunHelpRejectsInvalidTopics(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown topic", args: []string{"help", "unknown"}, want: "unknown help topic"},
		{name: "extra topic argument", args: []string{"help", "hooks", "extra"}, want: "help accepts at most one command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out string
			if code := run(tt.args, func(s string) { out += s }); code != 2 {
				t.Fatalf("exit code = %d, want 2; output = %q", code, out)
			}
			if !strings.Contains(out, tt.want) || !strings.Contains(out, "Commands:") {
				t.Fatalf("output = %q, want error %q and root help", out, tt.want)
			}
		})
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var out string
	code := run([]string{"bogus"}, func(s string) { out += s })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "unknown command") || !strings.Contains(out, "Commands:") {
		t.Fatalf("output = %q, want error and root help", out)
	}
}

func TestRunWithoutCommandPrintsRootHelp(t *testing.T) {
	var out string
	code := run(nil, func(s string) { out += s })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "Usage:") || !strings.Contains(out, "Commands:") {
		t.Fatalf("output = %q, want root help", out)
	}
}

func TestParseIngestFlagsRestrictsCodexPolicyPrefix(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "canonical Codex hook", args: []string{"--agent=codex"}},
		{name: "Codex endpoint override", args: []string{"--agent=codex", "--endpoint=https://example.invalid"}, wantErr: true},
		{name: "Codex agent override", args: []string{"--agent=codex", "--agent=claude"}, wantErr: true},
		{name: "Codex unknown suffix", args: []string{"--agent=codex", "--verbose"}, wantErr: true},
		{name: "legacy Claude endpoint", args: []string{"--agent=claude", "--endpoint=https://otel.example/v1/logs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseIngestFlags(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunIngestRejectsCodexTrailingArgsBeforeOpeningOutbox(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	code := run(
		[]string{"ingest", "--agent=codex", "--endpoint=https://example.invalid"},
		func(string) {},
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want hook-safe 0", code)
	}
	if _, err := os.Stat(filepath.Join(cacheHome, pkgName)); !os.IsNotExist(err) {
		t.Fatalf("outbox was opened for rejected Codex arguments: %v", err)
	}
}

func TestRunHooksInstallAcceptsTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var out string
	code := run([]string{"hooks", "install", "--target=codex,claude"}, func(s string) { out += s })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	if !strings.Contains(out, "claude: installed") || !strings.Contains(out, "codex: installed") {
		t.Fatalf("output = %q, want install results", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("unrequested Cursor hook exists: %v", err)
	}
}

func TestRunHooksInstallContinuesAfterLegacyAPMCleanupWarning(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies: [\n")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var out, stderr strings.Builder
	code := runWithStderr(
		[]string{"hooks", "install", "--target=claude"},
		func(value string) { out.WriteString(value) },
		&stderr,
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out.String())
	}
	if !strings.Contains(stderr.String(), "could not verify or remove") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(hookPath(home, hookClaude)); err != nil {
		t.Fatalf("Claude hook not installed after cleanup warning: %v", err)
	}
}

func TestRunHooksUninstallWritesReceiptForFullTargetSet(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "default targets", args: []string{"hooks", "uninstall"}},
		{name: "explicit targets in different order", args: []string{"hooks", "uninstall", "--target=cursor,claude,codex"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_STATE_HOME", "")
			if err := hookInstallError(installHooks(home, allHookTargets)); err != nil {
				t.Fatal(err)
			}

			var out string
			if code := run(tt.args, func(s string) { out += s }); code != 0 {
				t.Fatalf("exit code = %d, want 0; output = %q", code, out)
			}
			for _, target := range allHookTargets {
				assertNoRemovalCommands(t, hookPath(home, target))
				if !strings.Contains(out, string(target)+": removed:") {
					t.Fatalf("output = %q, want removed result for %s", out, target)
				}
			}
			assertHookReceiptContents(t, home)

			out = ""
			if code := run(tt.args, func(s string) { out += s }); code != 0 {
				t.Fatalf("second exit code = %d, want 0; output = %q", code, out)
			}
			for _, target := range allHookTargets {
				if !strings.Contains(out, string(target)+": unchanged:") {
					t.Fatalf("second output = %q, want unchanged result for %s", out, target)
				}
			}
			assertHookReceiptContents(t, home)
		})
	}
}

func TestRunHooksUninstallSubsetDoesNotWriteReceipt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", "")
	if err := hookInstallError(installHooks(home, allHookTargets)); err != nil {
		t.Fatal(err)
	}

	var out string
	if code := run([]string{"hooks", "uninstall", "--target=cursor,claude"}, func(s string) { out += s }); code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	for _, target := range []hookTarget{hookClaude, hookCursor} {
		assertNoRemovalCommands(t, hookPath(home, target))
	}
	assertInstalledHook(t, hookPath(home, hookCodex), inspectCodexHook)
	if _, err := os.Stat(hookReceiptPath(home)); !os.IsNotExist(err) {
		t.Fatalf("receipt exists after subset removal: %v", err)
	}
}

func TestRunHooksUninstallContinuesAfterMalformedClaudeFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", "")
	if err := hookInstallError(installHooks(home, allHookTargets)); err != nil {
		t.Fatal(err)
	}
	claudePath := hookPath(home, hookClaude)
	malformed := []byte("{not json\n")
	if err := os.WriteFile(claudePath, malformed, 0o600); err != nil {
		t.Fatal(err)
	}

	var out string
	if code := run([]string{"hooks", "uninstall"}, func(s string) { out += s }); code != 1 {
		t.Fatalf("exit code = %d, want 1; output = %q", code, out)
	}
	if !strings.Contains(out, "claude: failed:") || !strings.Contains(out, "codex: removed:") || !strings.Contains(out, "cursor: removed:") {
		t.Fatalf("output = %q, want one failed and two removed results", out)
	}
	assertNoRemovalCommands(t, hookPath(home, hookCodex))
	assertNoRemovalCommands(t, hookPath(home, hookCursor))
	if _, err := os.Stat(hookReceiptPath(home)); !os.IsNotExist(err) {
		t.Fatalf("receipt exists after target failure: %v", err)
	}
}

func TestRunHooksUninstallReturnsFailureWhenReceiptCannotBeWritten(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", "")
	if err := hookInstallError(installHooks(home, allHookTargets)); err != nil {
		t.Fatal(err)
	}
	receiptPath := hookReceiptPath(home)
	if err := os.MkdirAll(receiptPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receiptPath, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, stderr strings.Builder
	code := runWithStderr(
		[]string{"hooks", "uninstall"},
		func(value string) { out.WriteString(value) },
		&stderr,
	)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output = %q; stderr = %q", code, out.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "hooks: write uninstall receipt:") {
		t.Fatalf("stderr = %q, want receipt write error", stderr.String())
	}
	for _, target := range allHookTargets {
		assertNoRemovalCommands(t, hookPath(home, target))
	}
}

func TestRunHooksInstallFailsClosedOnReceiptInvalidation(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies:\n  apm:\n    - "+legacyTelemetryAPMPackage+"\n")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", "")
	claudePath := hookPath(home, hookClaude)
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o700); err != nil {
		t.Fatal(err)
	}
	claudeContents := []byte("{\"theme\":\"dark\"}\n")
	if err := os.WriteFile(claudePath, claudeContents, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(home, ".apm", "apm.yml")
	manifestContents, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := hookReceiptPath(home)
	if err := os.MkdirAll(receiptPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receiptPath, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, stderr strings.Builder
	code := runWithStderr(
		[]string{"hooks", "install"},
		func(value string) { out.WriteString(value) },
		&stderr,
	)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output = %q; stderr = %q", code, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "hooks: invalidate hook removal receipt:") {
		t.Fatalf("output = %q, want receipt invalidation error", out.String())
	}
	gotClaude, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotClaude) != string(claudeContents) {
		t.Fatalf("Claude file = %q, want unchanged %q", gotClaude, claudeContents)
	}
	for _, target := range []hookTarget{hookCodex, hookCursor} {
		if _, err := os.Stat(hookPath(home, target)); !os.IsNotExist(err) {
			t.Fatalf("%s hook exists after invalidation failure: %v", target, err)
		}
	}
	gotManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotManifest) != string(manifestContents) {
		t.Fatalf("legacy APM manifest = %q, want unchanged %q", gotManifest, manifestContents)
	}
}

func TestRunHooksUninstallDoesNotCleanUpLegacyAPM(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies: [\n")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", "")
	if err := hookInstallError(installHooks(home, allHookTargets)); err != nil {
		t.Fatal(err)
	}

	var out, stderr strings.Builder
	code := runWithStderr(
		[]string{"hooks", "uninstall"},
		func(value string) { out.WriteString(value) },
		&stderr,
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q; stderr = %q", code, out.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no legacy APM cleanup warning", stderr.String())
	}
	manifest, err := os.ReadFile(filepath.Join(home, ".apm", "apm.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifest) != "dependencies: [\n" {
		t.Fatalf("legacy APM manifest = %q, want unchanged", manifest)
	}
}

func assertHookReceiptContents(t *testing.T, home string) {
	t.Helper()
	data, err := os.ReadFile(hookReceiptPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != hookReceiptContents {
		t.Fatalf("receipt = %q, want %q", data, hookReceiptContents)
	}
}

func TestRunConfigureContinuesAfterLegacyAPMCleanupWarning(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies: [\n")
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "")
	t.Setenv("AI_AGENT_TELEMETRY_TOKEN", "")
	t.Setenv(envRepoAllow, "")
	var out, stderr strings.Builder
	code := runWithStderr(
		[]string{"configure", "--endpoint=https://otel.example/v1/logs"},
		func(value string) { out.WriteString(value) },
		&stderr,
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out.String())
	}
	if !strings.Contains(stderr.String(), "could not verify or remove") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(configHome, pkgName, "env")); err != nil {
		t.Fatalf("telemetry env not written: %v", err)
	}
	for _, target := range allHookTargets {
		if _, err := os.Stat(hookPath(home, target)); err != nil {
			t.Fatalf("%s hook not installed after cleanup warning: %v", target, err)
		}
	}
}

func TestRunConfigureFailsClosedOnReceiptInvalidation(t *testing.T) {
	home, configHome := isolateRunConfigure(t)
	t.Setenv("XDG_STATE_HOME", "")

	manifestPath := filepath.Join(home, ".apm", "apm.yml")
	manifestContents := []byte("dependencies:\n  apm:\n    - " + legacyTelemetryAPMPackage + "\n")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestContents, 0o600); err != nil {
		t.Fatal(err)
	}
	receiptPath := hookReceiptPath(home)
	if err := os.MkdirAll(receiptPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receiptPath, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, stderr strings.Builder
	code := runWithStderr(
		[]string{"configure", "--endpoint=https://otel.example/v1/logs"},
		func(value string) { out.WriteString(value) },
		&stderr,
	)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output = %q; stderr = %q", code, out.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "configure hooks: invalidate hook removal receipt:") {
		t.Fatalf("stderr = %q, want receipt invalidation error", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(configHome, pkgName, "env")); err != nil {
		t.Fatalf("telemetry env not written: %v", err)
	}
	for _, target := range allHookTargets {
		if _, err := os.Stat(hookPath(home, target)); !os.IsNotExist(err) {
			t.Fatalf("%s hook exists after receipt invalidation failure: %v", target, err)
		}
	}
	gotManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotManifest) != string(manifestContents) {
		t.Fatalf("legacy APM manifest = %q, want unchanged %q", gotManifest, manifestContents)
	}
}

func TestRunConfigureHooksNoneSkipsLegacyAPMCleanup(t *testing.T) {
	home := writeGlobalAPMManifest(t, "dependencies: [\n")
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "")
	t.Setenv("AI_AGENT_TELEMETRY_TOKEN", "")
	t.Setenv(envRepoAllow, "")
	var out, stderr strings.Builder
	code := runWithStderr(
		[]string{"configure", "--endpoint=https://otel.example/v1/logs", "--hooks=none"},
		func(value string) { out.WriteString(value) },
		&stderr,
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no cleanup warning", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(configHome, pkgName, "env")); err != nil {
		t.Fatalf("telemetry env not written: %v", err)
	}
	for _, target := range allHookTargets {
		if _, err := os.Stat(hookPath(home, target)); !os.IsNotExist(err) {
			t.Fatalf("%s hook exists with --hooks=none: %v", target, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.yml")); err != nil {
		t.Fatalf("legacy APM manifest changed with --hooks=none: %v", err)
	}
}

func TestRunHooksInstallRejectsEmptyTargetWithoutWritingFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var out string
	code := run([]string{"hooks", "install", "--target="}, func(s string) { out += s })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output = %q", code, out)
	}
	for _, target := range allHookTargets {
		if _, err := os.Stat(hookPath(home, target)); !os.IsNotExist(err) {
			t.Fatalf("%s hook exists after invalid command: %v", target, err)
		}
	}
}

func TestRunConfigureInstallsHooks(t *testing.T) {
	home, configHome := isolateRunConfigure(t)
	var out string
	code := run([]string{"configure", "--endpoint=https://otel.example/v1/logs"}, func(s string) { out += s })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	for _, target := range allHookTargets {
		if _, err := os.Stat(hookPath(home, target)); err != nil {
			t.Fatalf("%s hook not installed: %v", target, err)
		}
	}
	if _, err := os.Stat(filepath.Join(configHome, pkgName, "env")); err != nil {
		t.Fatalf("telemetry env not written: %v", err)
	}
	if !strings.Contains(out, "restart Codex and approve `ai-agent-telemetry ingest --agent=codex` if prompted") {
		t.Fatalf("output = %q, want Codex restart reminder", out)
	}
}

func TestRunConfigureInstallsHooksNone(t *testing.T) {
	home, _ := isolateRunConfigure(t)
	var out string
	code := run([]string{"configure", "--endpoint=https://otel.example/v1/logs", "--hooks=none"}, func(s string) { out += s })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	for _, target := range allHookTargets {
		if _, err := os.Stat(hookPath(home, target)); !os.IsNotExist(err) {
			t.Fatalf("%s hook exists with --hooks=none: %v", target, err)
		}
	}
}

func TestRunConfigureInstallsHooksSubset(t *testing.T) {
	home, _ := isolateRunConfigure(t)
	var out string
	code := run([]string{"configure", "--endpoint=https://otel.example/v1/logs", "--hooks=claude,cursor"}, func(s string) { out += s })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output = %q", code, out)
	}
	for _, target := range []hookTarget{hookClaude, hookCursor} {
		if _, err := os.Stat(hookPath(home, target)); err != nil {
			t.Fatalf("%s hook not installed: %v", target, err)
		}
	}
	if _, err := os.Stat(hookPath(home, hookCodex)); !os.IsNotExist(err) {
		t.Fatalf("unrequested Codex hook exists: %v", err)
	}
}

func TestRunConfigureInstallsHooksContinuesAfterMalformedClaudeFile(t *testing.T) {
	home, configHome := isolateRunConfigure(t)
	claudePath := hookPath(home, hookClaude)
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out string
	code := run([]string{"configure", "--endpoint=https://otel.example/v1/logs"}, func(s string) { out += s })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output = %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(configHome, pkgName, "env")); err != nil {
		t.Fatalf("telemetry env not written: %v", err)
	}
	for _, target := range []hookTarget{hookCodex, hookCursor} {
		if _, err := os.Stat(hookPath(home, target)); err != nil {
			t.Fatalf("%s hook not installed after Claude failure: %v", target, err)
		}
	}
	if !strings.Contains(out, "claude: invalid") || !strings.Contains(out, "codex: installed") || !strings.Contains(out, "cursor: installed") {
		t.Fatalf("output = %q, want aggregate hook status", out)
	}
}

func TestRunConfigureRejectsUnavailableHomeWithoutRelativeHooks(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	configHome := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "")
	t.Setenv("AI_AGENT_TELEMETRY_TOKEN", "")
	t.Setenv(envRepoAllow, "")

	var out, stderr strings.Builder
	code := runWithStderr(
		[]string{"configure", "--endpoint=https://otel.example/v1/logs"},
		func(value string) { out.WriteString(value) },
		&stderr,
	)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output = %q; stderr = %q", code, out.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(configHome, pkgName, "env")); err != nil {
		t.Fatalf("telemetry env not written: %v", err)
	}
	for _, relativeDir := range []string{".claude", ".codex", ".cursor"} {
		if _, err := os.Stat(filepath.Join(workingDir, relativeDir)); !os.IsNotExist(err) {
			t.Fatalf("configure created relative hook directory %s: %v", relativeDir, err)
		}
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q, want no hook status", out.String())
	}
	if !strings.Contains(stderr.String(), "configure hooks: invalidate hook removal receipt: "+errUserHomeUnavailable.Error()) {
		t.Fatalf("stderr = %q, want unavailable-home receipt error", stderr.String())
	}
}

func isolateRunConfigure(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "")
	t.Setenv("AI_AGENT_TELEMETRY_TOKEN", "")
	t.Setenv(envRepoAllow, "")
	return home, configHome
}

func TestRunHooksReportsUnavailableHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	var out string
	code := run([]string{"hooks", "install"}, func(s string) { out += s })
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(out, "no user home directory") {
		t.Fatalf("output = %q, want home error", out)
	}
}

func TestRunHooksRejectsMissingAction(t *testing.T) {
	var out string
	code := run([]string{"hooks"}, func(s string) { out += s })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "action") || !strings.Contains(out, "ai-agent-telemetry hooks install") {
		t.Fatalf("output = %q, want missing action error and hooks help", out)
	}
}

func TestRunHooksRejectsUnknownAction(t *testing.T) {
	var out string
	code := run([]string{"hooks", "remove"}, func(s string) { out += s })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "remove") {
		t.Fatalf("output = %q, want unknown action", out)
	}
}

func TestRunHooksRejectsUnknownTarget(t *testing.T) {
	var out string
	code := run([]string{"hooks", "install", "--target=windsurf"}, func(s string) { out += s })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "windsurf") {
		t.Fatalf("output = %q, want invalid target", out)
	}
}

func TestIngestEnqueuesAndFlushes(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Isolate the config dir so a CA configured on the dev machine does not
	// force TLS onto the plain-HTTP test server (caTLSConfig reads pkgConfigDir).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	s := &Outbox{Dir: t.TempDir()}
	tp := filepath.Join(t.TempDir(), "r.jsonl")
	body := `{"type":"session_meta","payload":{"git":{"repository_url":"git@github.com:Netcracker/r.git"}}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/demo/SKILL.md\"}"}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{
		"hook_event_name": "Stop",
		"session_id":      "s1",
		"transcript_path": tp,
	})

	code := ingest(s, "codex", srv.URL, stdin, func(string) string { return "" }, deliverySettings{
		BufferCap:    defaultBufferCap,
		FlushTimeout: defaultFlushTimeout,
	})
	if code != 0 {
		t.Fatalf("ingest exit = %d, want 0", code)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("expected a flush on first ingest")
	}
	files, _ := s.List()
	if len(files) != 0 {
		t.Fatalf("buffer should be drained: %d files", len(files))
	}
	if _, err := os.Stat(filepath.Join(s.Dir, flushStampName)); err != nil {
		t.Fatalf("flush stamp missing: %v", err)
	}
}

func TestIngestUsesConfiguredBufferCap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	s := &Outbox{Dir: t.TempDir()}
	for i := 0; i < 2; i++ {
		if err := s.Enqueue(testSkillEvent(t, "codex", "s1", "", "", "old", time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
	}
	tp := filepath.Join(t.TempDir(), "r.jsonl")
	body := `{"type":"session_meta","payload":{"git":{"repository_url":"git@github.com:Netcracker/r.git"}}}` + "\n" +
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat skills/demo/SKILL.md\"}"}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{
		"hook_event_name": "Stop",
		"session_id":      "s1",
		"transcript_path": tp,
	})
	settings := deliverySettings{BufferCap: 1, FlushTimeout: defaultFlushTimeout}

	if code := ingest(s, "codex", "", stdin, func(string) string { return "" }, settings); code != 0 {
		t.Fatalf("ingest exit = %d, want 0", code)
	}
	files, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("buffered %d events, want configured cap 1", len(files))
	}
}

func TestRunFlushUsesConfiguredTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(envFlushTimeout, "1ms")
	s, err := DefaultOutbox()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(testSkillEvent(t, "codex", "s1", "", "", "queued", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"flush", "--endpoint=" + srv.URL}, func(string) {}); code != 0 {
		t.Fatalf("flush exit = %d, want 0", code)
	}
	files, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("buffered %d events, want timeout to preserve the queued event", len(files))
	}
}

func TestIngestBadJSONStillSucceeds(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	code := ingest(s, "codex", "", []byte("not json"), func(string) string { return "" }, deliverySettings{
		BufferCap:    defaultBufferCap,
		FlushTimeout: defaultFlushTimeout,
	})
	if code != 0 {
		t.Fatalf("ingest exit = %d, want 0", code)
	}
}

func TestReservedSelftestCannotBeSelectedThroughDetect(t *testing.T) {
	if _, err := detect(selftestAgent, []byte(`{}`), nil, time.Now().UTC()); err == nil {
		t.Fatal("detect accepted reserved selftest agent")
	}
	if err := validateSerializableEvent(newSelftestProbe(time.Now().UTC())); err != nil {
		t.Fatalf("newSelftestProbe produced invalid event: %v", err)
	}
}

func TestIngestCursorFromTranscript(t *testing.T) {
	// Isolate config/cache dirs so the real machine state and any configured CA
	// are untouched (DefaultOffsetStore uses the cache dir; caTLSConfig the config dir).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	dir := t.TempDir()
	tp := filepath.Join(dir, "t.jsonl")
	body := `{"message":{"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/.cursor/skills/from-transcript/SKILL.md"}}]}}` + "\n"
	if err := os.WriteFile(tp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, _ := json.Marshal(map[string]any{
		"hook_event_name": "afterAgentResponse",
		"session_id":      "c1",
		"workspace_roots": []string{"/repo"},
		"transcript_path": tp,
	})

	// Empty endpoint => Flush is a no-op, so events stay in the outbox to inspect.
	s := &Outbox{Dir: t.TempDir()}
	settings := deliverySettings{BufferCap: defaultBufferCap, FlushTimeout: defaultFlushTimeout}
	if code := ingest(
		s,
		"cursor",
		"",
		stdin,
		func(string) string { return "git@github.com:Netcracker/repo.git" },
		settings,
	); code != 0 {
		t.Fatalf("ingest exit = %d, want 0", code)
	}

	files, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("buffered %d events, want 1", len(files))
	}
}

func TestShouldFlushThrottle(t *testing.T) {
	dir := t.TempDir()
	s := &Outbox{Dir: dir}
	if shouldFlush(s, 10, time.Hour) {
		t.Fatal("should not flush with empty buffer")
	}
	_ = s.Enqueue(testSkillEvent(t, "codex", "s1", "", "", "x", time.Now().UTC()))
	if !shouldFlush(s, 10, time.Hour) {
		t.Fatal("should flush when no prior attempt")
	}
	touchFlushStamp(s)
	if shouldFlush(s, 10, time.Hour) {
		t.Fatal("should skip: stamp fresh and count below N")
	}
}

func TestRepoRemoteCacheMemoizesByRepoDir(t *testing.T) {
	var remotes int
	cache := newRepoRemoteCache(func(cwd string) []string {
		remotes++
		return []string{"git@github.com:Netcracker/repo.git"}
	})

	policy := telemetryPolicy{RepoAllowList: []string{"github.com/Netcracker/*"}}
	events := []TelemetryEvent{
		testSkillEvent(t, "codex", "s1", "", "/repo", "skill-a", time.Now()),
		testSkillEvent(t, "codex", "s2", "", "/repo", "skill-b", time.Now()),
	}
	got := filterEventsByPolicy(events, policy, cache.remotesFor)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if remotes != 1 {
		t.Fatalf("remote calls = %d, want 1", remotes)
	}
}
