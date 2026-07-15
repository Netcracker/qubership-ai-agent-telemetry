package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWriteEnvFileCreatesWithSecurePerms(t *testing.T) {
	dir := filepath.Join(t.TempDir(), pkgName)
	if err := writeEnvFile(dir, map[string]string{"AI_AGENT_TELEMETRY_ENDPOINT": "https://x/v1/logs"}); err != nil {
		t.Fatal(err)
	}
	got := loadEnvFile(filepath.Join(dir, "env"))
	if got["AI_AGENT_TELEMETRY_ENDPOINT"] != "https://x/v1/logs" {
		t.Fatalf("endpoint = %q", got["AI_AGENT_TELEMETRY_ENDPOINT"])
	}
	fi, err := os.Stat(filepath.Join(dir, "env"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600 (the file may hold a token)", perm)
	}
}

func TestWriteEnvFileMergesKeepingExistingKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), pkgName)
	if err := writeEnvFile(dir, map[string]string{"AI_AGENT_TELEMETRY_ENDPOINT": "https://x/v1/logs"}); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvFile(dir, map[string]string{"AI_AGENT_TELEMETRY_TOKEN": "secret"}); err != nil {
		t.Fatal(err)
	}
	got := loadEnvFile(filepath.Join(dir, "env"))
	if got["AI_AGENT_TELEMETRY_ENDPOINT"] != "https://x/v1/logs" || got["AI_AGENT_TELEMETRY_TOKEN"] != "secret" {
		t.Fatalf("merge lost a key: %v", got)
	}
}

func TestWriteEnvFileOverwritesProvidedKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), pkgName)
	_ = writeEnvFile(dir, map[string]string{"AI_AGENT_TELEMETRY_ENDPOINT": "https://old/v1/logs"})
	_ = writeEnvFile(dir, map[string]string{"AI_AGENT_TELEMETRY_ENDPOINT": "https://new/v1/logs"})
	got := loadEnvFile(filepath.Join(dir, "env"))
	if got["AI_AGENT_TELEMETRY_ENDPOINT"] != "https://new/v1/logs" {
		t.Fatalf("endpoint = %q, want overwritten", got["AI_AGENT_TELEMETRY_ENDPOINT"])
	}
}

func TestApplyConfigureWritesEndpointTokenAndCA(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	src := filepath.Join(t.TempDir(), "src.crt")
	if err := os.WriteFile(src, selfSignedPEM(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyConfigure(cfg, "https://otel.example/v1/logs", src, "secret", "", deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	env := loadEnvFile(filepath.Join(cfg, "env"))
	if env["AI_AGENT_TELEMETRY_ENDPOINT"] != "https://otel.example/v1/logs" {
		t.Fatalf("endpoint = %q", env["AI_AGENT_TELEMETRY_ENDPOINT"])
	}
	if env["AI_AGENT_TELEMETRY_TOKEN"] != "secret" {
		t.Fatalf("token = %q", env["AI_AGENT_TELEMETRY_TOKEN"])
	}
	if _, err := os.Stat(filepath.Join(cfg, caFileName)); err != nil {
		t.Fatalf("ca.crt not written: %v", err)
	}
}

func TestApplyConfigureWritesDeliverySettings(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	delivery := deliverySettingOverrides{BufferCap: "1000", FlushTimeout: "30s"}
	if err := applyConfigure(cfg, "", "", "", "", delivery); err != nil {
		t.Fatal(err)
	}
	env := loadEnvFile(filepath.Join(cfg, "env"))
	if env[envBufferCap] != "1000" || env[envFlushTimeout] != "30s" {
		t.Fatalf("delivery settings = %v", env)
	}

	if err := applyConfigure(cfg, "https://otel.example/v1/logs", "", "", "", deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	env = loadEnvFile(filepath.Join(cfg, "env"))
	if env[envBufferCap] != "1000" || env[envFlushTimeout] != "30s" {
		t.Fatalf("omitted delivery settings were not preserved: %v", env)
	}
}

func TestApplyConfigureWritesRepoAllow(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	allow := "github.com/Netcracker/*,gitlab.example.com/qubership/**"
	if err := applyConfigure(cfg, "", "", "", allow, deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	got := loadRepoAllowFile(filepath.Join(cfg, repoAllowFileName))
	want := []string{"github.com/Netcracker/*", "gitlab.example.com/qubership/**"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("repo allow = %v, want %v", got, want)
	}
}

func TestApplyConfigureDefaultsRepoAllowWhenUnset(t *testing.T) {
	t.Setenv(envRepoAllow, "")
	cfg := filepath.Join(t.TempDir(), pkgName)
	if err := applyConfigure(cfg, "https://otel.example/v1/logs", "", "", "", deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	got := loadRepoAllowFile(filepath.Join(cfg, repoAllowFileName))
	if strings.Join(got, ",") != defaultRepoAllow {
		t.Fatalf("repo allow = %v, want %q", got, defaultRepoAllow)
	}
}

func TestApplyConfigurePreservesExistingRepoAllow(t *testing.T) {
	t.Setenv(envRepoAllow, "")
	cfg := filepath.Join(t.TempDir(), pkgName)
	allow := "github.com/Qubership/*"
	if err := applyConfigure(cfg, "", "", "", allow, deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfigure(cfg, "https://otel.example/v1/logs", "", "", "", deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	got := loadRepoAllowFile(filepath.Join(cfg, repoAllowFileName))
	if strings.Join(got, ",") != allow {
		t.Fatalf("repo allow = %v, want preserved %q", got, allow)
	}
}

func TestApplyConfigureOnlyWritesProvidedFields(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	if err := applyConfigure(cfg, "https://otel.example/v1/logs", "", "", "", deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	env := loadEnvFile(filepath.Join(cfg, "env"))
	if _, ok := env["AI_AGENT_TELEMETRY_TOKEN"]; ok {
		t.Fatal("token key should be absent when no token was given")
	}
	if _, err := os.Stat(filepath.Join(cfg, caFileName)); err == nil {
		t.Fatal("ca.crt should not exist when no CA path was given")
	}
}

func TestWriteEnvFileIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), pkgName)
	kv := map[string]string{"AI_AGENT_TELEMETRY_ENDPOINT": "https://x/v1/logs", "AI_AGENT_TELEMETRY_TOKEN": "t"}
	_ = writeEnvFile(dir, kv)
	first, _ := os.ReadFile(filepath.Join(dir, "env"))
	_ = writeEnvFile(dir, kv)
	second, _ := os.ReadFile(filepath.Join(dir, "env"))
	if string(first) != string(second) {
		t.Fatalf("not idempotent:\n%q\n%q", first, second)
	}
}

func TestParseConfigureFlags(t *testing.T) {
	opts, err := parseConfigureFlags([]string{
		"--endpoint=https://x/v1/logs",
		"--ca=/tmp/ca.crt",
		"--repo-allow=github.com/Netcracker/*",
		"--hooks=cursor,claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Endpoint != "https://x/v1/logs" {
		t.Fatalf("endpoint = %q", opts.Endpoint)
	}
	if opts.CAPath != "/tmp/ca.crt" {
		t.Fatalf("ca = %q", opts.CAPath)
	}
	if opts.RepoAllow != "github.com/Netcracker/*" {
		t.Fatalf("repoAllow = %q", opts.RepoAllow)
	}
	wantHooks := []hookTarget{hookClaude, hookCursor}
	if !reflect.DeepEqual(opts.Hooks, wantHooks) {
		t.Fatalf("hooks = %v, want %v", opts.Hooks, wantHooks)
	}
}

func TestParseConfigureFlagsAcceptsDeliverySettings(t *testing.T) {
	opts, err := parseConfigureFlags([]string{"--buffer-cap", "1000", "--flush-timeout=30s"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Delivery.BufferCap != "1000" || opts.Delivery.FlushTimeout != "30s" {
		t.Fatalf("delivery settings = %+v", opts.Delivery)
	}
}

func TestParseConfigureFlagsRejectsInvalidDeliverySettings(t *testing.T) {
	tests := [][]string{
		{"--buffer-cap=0"},
		{"--buffer-cap=-1"},
		{"--buffer-cap=many"},
		{"--flush-timeout=0s"},
		{"--flush-timeout=-1s"},
		{"--flush-timeout=long"},
		{"--buffer-cap"},
		{"--flush-timeout"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if _, err := parseConfigureFlags(args); err == nil {
				t.Fatalf("parseConfigureFlags(%q) succeeded, want error", args)
			}
		})
	}
}

func TestParseConfigureFlagsJoinsRepeatableRepoAllow(t *testing.T) {
	opts, err := parseConfigureFlags([]string{
		"--repo-allow", "github.com/Netcracker/*",
		"--repo-allow=github.com/Qubership/*,gitlab.example.com/qubership/**",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "github.com/Netcracker/*,github.com/Qubership/*,gitlab.example.com/qubership/**"
	if opts.RepoAllow != want {
		t.Fatalf("repoAllow = %q, want %q", opts.RepoAllow, want)
	}
}

func TestParseConfigureFlagsDefaultsHooks(t *testing.T) {
	opts, err := parseConfigureFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opts.Hooks, allHookTargets) {
		t.Fatalf("hooks = %v, want %v", opts.Hooks, allHookTargets)
	}
}

func TestParseConfigureFlagsAcceptsSeparateHooksValue(t *testing.T) {
	opts, err := parseConfigureFlags([]string{"--hooks", "none"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opts.Hooks, []hookTarget{}) {
		t.Fatalf("hooks = %v, want empty", opts.Hooks)
	}
}

func TestParseConfigureFlagsRejectsUnknownFlag(t *testing.T) {
	_, err := parseConfigureFlags([]string{"--bogus"})
	if err == nil || !strings.Contains(err.Error(), "--bogus") {
		t.Fatalf("error = %v, want unknown flag", err)
	}
}

func TestParseConfigureFlagsRejectsMissingValue(t *testing.T) {
	_, err := parseConfigureFlags([]string{"--hooks"})
	if err == nil || !strings.Contains(err.Error(), "--hooks") {
		t.Fatalf("error = %v, want missing --hooks value", err)
	}
}

func TestConfigureEndpointUsesFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "https://env/v1/logs")

	if got := configureEndpoint("https://flag/v1/logs"); got != "https://flag/v1/logs" {
		t.Fatalf("endpoint = %q, want flag value", got)
	}
}

func TestConfigureEndpointUsesEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AI_AGENT_TELEMETRY_ENDPOINT", "https://env/v1/logs")

	if got := configureEndpoint(""); got != "https://env/v1/logs" {
		t.Fatalf("endpoint = %q, want env value", got)
	}
}

func TestSelftestDeliversProbeAndClearsIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Outbox{Dir: t.TempDir()}
	res, err := runSelftest(s, srv.URL, "", nil, 2*time.Second)
	if err != nil {
		t.Fatalf("selftest: %v", err)
	}
	if !res.Delivered {
		t.Fatal("want Delivered true on HTTP 200")
	}
	files, _ := s.List()
	if len(files) != 0 {
		t.Fatalf("probe should have left the outbox: %d remain", len(files))
	}
}

func TestSelftestKeepsProbeOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := &Outbox{Dir: t.TempDir()}
	res, err := runSelftest(s, srv.URL, "", nil, 2*time.Second)
	if err == nil {
		t.Fatal("want error when the collector rejects the probe")
	}
	if res.Delivered {
		t.Fatal("want Delivered false on failure")
	}
	if n := probesRemaining(s); n != 1 {
		t.Fatalf("probe should remain in the outbox: %d probes", n)
	}
}

func TestSelftestErrorsWhenNotConfigured(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	if _, err := runSelftest(s, "", "", nil, time.Second); err == nil {
		t.Fatal("want error when no endpoint is configured")
	}
}

func TestGatherStatusReportsConfiguredState(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	if err := os.WriteFile(filepath.Join(t.TempDir(), "src.crt"), selfSignedPEM(t), 0o644); err != nil {
		t.Fatal(err)
	}
	// place a ca.crt via the real writer so the test mirrors configure
	src := filepath.Join(t.TempDir(), "src.crt")
	_ = os.WriteFile(src, selfSignedPEM(t), 0o644)
	if err := copyCAFile(cfg, src); err != nil {
		t.Fatal(err)
	}

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 2)

	r := gatherStatus(s, cfg, "https://otel.example/v1/logs", telemetryPolicy{})
	if !r.Configured {
		t.Fatal("want configured when an endpoint is set")
	}
	if !r.CAFound {
		t.Fatal("want CAFound when ca.crt is present")
	}
	if r.Buffered != 2 {
		t.Fatalf("buffered = %d, want 2", r.Buffered)
	}
	if r.Endpoint != "https://otel.example/v1/logs" {
		t.Fatalf("endpoint = %q", r.Endpoint)
	}
}

func TestGatherStatusReadsLastDeliveryError(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	s := &Outbox{Dir: t.TempDir()}
	if err := os.WriteFile(lastDeliveryErrorPath(s), []byte("proxyconnect tcp: operation not permitted"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed(t, s, 1)

	r := gatherStatus(s, cfg, "https://otel.example/v1/logs", telemetryPolicy{})
	if r.LastDeliveryError != "proxyconnect tcp: operation not permitted" {
		t.Fatalf("last delivery error = %q", r.LastDeliveryError)
	}
}

func TestGatherStatusNotConfiguredWhenNoEndpoint(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	s := &Outbox{Dir: t.TempDir()}
	r := gatherStatus(s, cfg, "", telemetryPolicy{})
	if r.Configured {
		t.Fatal("want not configured when endpoint is empty")
	}
	if r.CAFound {
		t.Fatal("want CAFound false when no ca.crt")
	}
}

func TestFormatStatusFlagsNextStepWhenNotConfigured(t *testing.T) {
	out := formatStatus(statusReport{Configured: false, ConfigDir: "/cfg"}, false)
	if !strings.Contains(strings.ToLower(out), "not configured") {
		t.Fatalf("output should flag the not-configured state, got:\n%s", out)
	}
}

func TestFormatStatusSuggestsVerboseWhenBufferedEventsHaveError(t *testing.T) {
	out := formatStatus(statusReport{
		Configured:        true,
		Buffered:          2,
		LastDeliveryError: "proxyconnect tcp: operation not permitted",
	}, false)

	if !strings.Contains(out, "diagnostics: delivery errors found; run `ai-agent-telemetry status --verbose`") {
		t.Fatalf("output should suggest verbose diagnostics, got:\n%s", out)
	}
	if strings.Contains(out, "proxyconnect tcp") {
		t.Fatalf("non-verbose output should not include the raw delivery error, got:\n%s", out)
	}
}

func TestFormatStatusVerboseIncludesLastDeliveryError(t *testing.T) {
	out := formatStatus(statusReport{
		Configured:        true,
		Buffered:          2,
		LastDeliveryError: "proxyconnect tcp: operation not permitted",
	}, true)

	if !strings.Contains(out, "diagnostics:\n") {
		t.Fatalf("verbose output should include diagnostics block, got:\n%s", out)
	}
	if !strings.Contains(out, "last_delivery_error: proxyconnect tcp: operation not permitted") {
		t.Fatalf("verbose output should include last delivery error, got:\n%s", out)
	}
}

func TestFormatStatusVerboseReportsNoRecordedError(t *testing.T) {
	out := formatStatus(statusReport{Configured: true, Buffered: 2}, true)

	if !strings.Contains(out, "last_delivery_error: none recorded") {
		t.Fatalf("verbose output should report no recorded delivery error, got:\n%s", out)
	}
}

func TestFormatStatusReportsHooksWithoutInvalidDetail(t *testing.T) {
	report := statusReport{Hooks: []hookStatus{
		{Target: hookClaude, Path: "/home/u/.claude/settings.json", State: hookInstalled},
		{Target: hookCodex, Path: "/home/u/.codex/hooks.json", State: hookMissing},
		{Target: hookCursor, Path: "/home/u/.cursor/hooks.json", State: hookInvalid, Detail: "parse hooks: invalid character"},
	}}
	out := formatStatus(report, false)
	want := "hooks:\n  claude: installed\n  codex: missing\n  cursor: invalid\n"
	if !strings.Contains(out, want) {
		t.Fatalf("output = %q, want block %q", out, want)
	}
	if strings.Contains(out, "invalid character") || strings.Contains(out, "/home/u/") {
		t.Fatalf("normal output exposes verbose hook details: %q", out)
	}
}

func TestFormatStatusVerboseReportsHooksWithPathsAndInvalidDetail(t *testing.T) {
	report := statusReport{Hooks: []hookStatus{
		{Target: hookClaude, Path: "/home/u/.claude/settings.json", State: hookInstalled},
		{Target: hookCodex, Path: "/home/u/.codex/hooks.json", State: hookMissing},
		{Target: hookCursor, Path: "/home/u/.cursor/hooks.json", State: hookInvalid, Detail: "parse /home/u/.cursor/hooks.json: invalid character"},
	}}
	out := formatStatus(report, true)
	for _, want := range []string{
		"claude: installed (/home/u/.claude/settings.json)",
		"codex: missing (/home/u/.codex/hooks.json)",
		"cursor: invalid (/home/u/.cursor/hooks.json): parse /home/u/.cursor/hooks.json: invalid character",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want %q", out, want)
		}
	}
}
