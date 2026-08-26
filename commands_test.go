package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	if perm := fi.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
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

func TestApplyConfigureRejectsInvalidEndpointBeforeWriting(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	if err := applyConfigure(cfg, "https://old.example/v1/logs", "", "old-token", "", deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, "env")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	for _, endpoint := range []string{
		"http://collector.example/v1/logs",
		"https:///v1/logs",
		"https://:4318/v1/logs",
		"https://collector.example/v1/traces",
	} {
		t.Run(endpoint, func(t *testing.T) {
			err := applyConfigure(cfg, endpoint, "", "new-token", "", deliverySettingOverrides{})
			if err == nil || !strings.Contains(err.Error(), "https://collector.example/v1/logs") {
				t.Fatalf("applyConfigure() error = %v, want endpoint example", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != string(want) {
				t.Fatalf("configuration changed after invalid endpoint:\n%s", got)
			}
		})
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
	want := "github.com/Netcracker/*,*netcracker*/**"
	if strings.Join(got, ",") != want {
		t.Fatalf("repo allow = %v, want %q", got, want)
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

func TestApplyConfigurePathAllowReplacePreserveAndClear(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	first := pathAllowUpdate{Set: true, Patterns: []string{"/work/**", `C:\Users\Alice\**`}}
	if err := applyConfigureWithPath(cfg, "", "", "", "", first, deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg, pathAllowFileName)
	got, err := loadPathAllowFile(path)
	if err != nil || !reflect.DeepEqual(got, first.Patterns) {
		t.Fatalf("initial path allow = %#v, %v", got, err)
	}

	if err := applyConfigureWithPath(cfg, "https://otel.example/v1/logs", "", "", "", pathAllowUpdate{}, deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	got, err = loadPathAllowFile(path)
	if err != nil || !reflect.DeepEqual(got, first.Patterns) {
		t.Fatalf("preserved path allow = %#v, %v", got, err)
	}

	replacement := pathAllowUpdate{Set: true, Patterns: []string{"/projects/**"}}
	if err := applyConfigureWithPath(cfg, "", "", "", "", replacement, deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	got, err = loadPathAllowFile(path)
	if err != nil || !reflect.DeepEqual(got, replacement.Patterns) {
		t.Fatalf("replacement path allow = %#v, %v", got, err)
	}

	if err := applyConfigureWithPath(cfg, "", "", "", "", pathAllowUpdate{Clear: true}, deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path policy exists after clear: %v", err)
	}
}

func TestApplyConfigureRejectsPathUpdateBeforeWritingAnything(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	initial := pathAllowUpdate{Set: true, Patterns: []string{"/work/**"}}
	if err := applyConfigureWithPath(cfg, "", "", "", "", initial, deliverySettingOverrides{}); err != nil {
		t.Fatal(err)
	}

	invalid := pathAllowUpdate{Set: true, Patterns: []string{"/work/[ab]"}}
	err := applyConfigureWithPath(cfg, "https://must-not-be-written.invalid", "", "", "", invalid, deliverySettingOverrides{})
	if err == nil {
		t.Fatal("invalid replacement succeeded")
	}
	got, loadErr := loadPathAllowFile(filepath.Join(cfg, pathAllowFileName))
	if loadErr != nil || !reflect.DeepEqual(got, initial.Patterns) {
		t.Fatalf("previous path allow changed to %#v, %v", got, loadErr)
	}
	if endpoint := loadEnvFile(filepath.Join(cfg, "env"))["AI_AGENT_TELEMETRY_ENDPOINT"]; endpoint != "" {
		t.Fatalf("endpoint was written before validation: %q", endpoint)
	}
	if _, err := os.Stat(filepath.Join(cfg, pathAllowFileName+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("temporary path policy remains: %v", err)
	}

	conflict := pathAllowUpdate{Set: true, Clear: true, Patterns: []string{"/work/**"}}
	if err := applyConfigureWithPath(cfg, "", "", "", "", conflict, deliverySettingOverrides{}); err == nil {
		t.Fatal("conflicting path update succeeded")
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

func TestSelftestDeliversVersionOneProbeAndClearsIt(t *testing.T) {
	var version int
	s := &Outbox{Dir: t.TempDir()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names, err := s.List()
		if err != nil || len(names) != 1 {
			t.Errorf("probe buffer = %v, err = %v", names, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		raw, err := os.ReadFile(filepath.Join(s.Dir, names[0]))
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var envelope struct {
			SchemaVersion int `json:"schema_version"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		version = envelope.SchemaVersion
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res, err := runSelftest(s, srv.URL, "", nil, 2*time.Second)
	if err != nil {
		t.Fatalf("selftest: %v", err)
	}
	if !res.Delivered || version != eventSchemaVersion {
		t.Fatalf("result = %+v, version = %d", res, version)
	}
	if names, _ := s.List(); len(names) != 0 {
		t.Fatalf("probe should be cleared, got %v", names)
	}
}

func TestSelftestFindsLegacyAndVersionOneProbes(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	legacy := `{"agent":"selftest","session_id":"123e4567-e89b-42d3-a456-426614174000","skill":"__selftest__","ts":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(s.Dir, "0001.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := newSelftestProbe(time.Unix(2, 0).UTC())
	raw, err := json.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "0002.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := probesRemaining(s); got != 2 {
		t.Fatalf("probes remaining = %d, want 2", got)
	}
}

func TestSelftestRejectsModifiedReservedPairs(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	entries := map[string]string{
		"0001.json": `{"agent":"selftest","session_id":"123e4567-e89b-42d3-a456-426614174000","skill":"brainstorming","ts":"2026-01-01T00:00:00Z"}`,
		"0002.json": `{"agent":"codex","session_id":"s1","skill":"__selftest__","ts":"2026-01-01T00:00:00Z"}`,
	}
	for name, raw := range entries {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := probesRemaining(s); got != 0 {
		t.Fatalf("probes remaining = %d, want 0", got)
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

	r := gatherStatus(s, cfg, "https://otel.example/v1/logs", telemetryPolicy{}, deliverySettings{})
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

func TestGatherStatusReportsPathPolicyState(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	tests := []struct {
		name   string
		policy telemetryPolicy
		want   string
	}{
		{name: "not configured", policy: telemetryPolicy{}, want: "not configured"},
		{name: "configured", policy: telemetryPolicy{PathAllowList: []string{"/work/**"}}, want: "configured"},
		{name: "invalid", policy: telemetryPolicy{PathAllowError: errors.New("permission denied")}, want: "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := gatherStatus(s, t.TempDir(), "", tt.policy, deliverySettings{})
			if report.PathScope != tt.want {
				t.Fatalf("path scope = %q, want %q", report.PathScope, tt.want)
			}
		})
	}
}

func TestFormatStatusReportsPathPolicyWithoutLeakingCompactDetails(t *testing.T) {
	configured := statusReport{
		PathScope:     "configured",
		PathAllowList: []string{"/Users/alice/work/**", `C:\Users\Alice\**`},
	}
	compact := formatStatus(configured, false)
	if !strings.Contains(compact, "path_scope: configured") {
		t.Fatalf("compact status = %q", compact)
	}
	if strings.Contains(compact, "/Users/alice") || strings.Contains(compact, `C:\Users\Alice`) {
		t.Fatalf("compact status disclosed path patterns: %q", compact)
	}
	verbose := formatStatus(configured, true)
	for _, want := range []string{"path_allow:", "    - /Users/alice/work/**", `    - C:\Users\Alice\**`} {
		if !strings.Contains(verbose, want) {
			t.Fatalf("verbose status = %q, want %q", verbose, want)
		}
	}

	invalid := statusReport{PathScope: "invalid", PathAllowError: "/private/config/path-allow: permission denied"}
	compact = formatStatus(invalid, false)
	if !strings.Contains(compact, "path_scope: invalid") || strings.Contains(compact, "/private/config") {
		t.Fatalf("compact invalid status = %q", compact)
	}
	verbose = formatStatus(invalid, true)
	if !strings.Contains(verbose, "path_allow_error: /private/config/path-allow: permission denied") {
		t.Fatalf("verbose invalid status = %q", verbose)
	}
}

func TestGatherStatusReadsLastDeliveryError(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	s := &Outbox{Dir: t.TempDir()}
	if err := os.WriteFile(lastDeliveryErrorPath(s), []byte("proxyconnect tcp: operation not permitted"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed(t, s, 1)

	r := gatherStatus(s, cfg, "https://otel.example/v1/logs", telemetryPolicy{}, deliverySettings{})
	if r.LastDeliveryError != "proxyconnect tcp: operation not permitted" {
		t.Fatalf("last delivery error = %q", r.LastDeliveryError)
	}
}

func TestGatherStatusNotConfiguredWhenNoEndpoint(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), pkgName)
	s := &Outbox{Dir: t.TempDir()}
	r := gatherStatus(s, cfg, "", telemetryPolicy{}, deliverySettings{})
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

func TestFormatStatusShowsDeliverySettingsOnlyWhenVerbose(t *testing.T) {
	report := statusReport{BufferCap: 1000, FlushTimeout: 30 * time.Second}
	if strings.Contains(formatStatus(report, false), "buffer_cap") {
		t.Fatal("compact status contains delivery settings")
	}
	verbose := formatStatus(report, true)
	for _, want := range []string{"configuration:", "  buffer_cap: 1000", "  flush_timeout: 30s"} {
		if !strings.Contains(verbose, want) {
			t.Fatalf("verbose status = %q, want %q", verbose, want)
		}
	}
}

func TestFormatStatusReportsHooksWithoutInvalidDetail(t *testing.T) {
	report := statusReport{Hooks: []hookStatus{
		{Target: hookClaude, Path: "/home/u/.claude/settings.json", State: hookInstalled},
		{Target: hookCline, Path: "/home/u/Documents/Cline/Hooks/PostToolUse", State: hookOutdated,
			Detail: "legacy managed hook; run replacement commands"},
		{Target: hookCodex, Path: "/home/u/.codex/hooks.json", State: hookMissing},
		{Target: hookCursor, Path: "/home/u/.cursor/hooks.json", State: hookInvalid, Detail: "parse hooks: invalid character"},
	}}
	out := formatStatus(report, false)
	want := "hooks:\n  claude: installed\n  cline: outdated\n  codex: missing\n  cursor: invalid\n"
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
		{Target: hookCline, Path: "/home/u/Documents/Cline/Hooks/PostToolUse", State: hookOutdated,
			Detail: "legacy managed hook; run replacement commands"},
		{Target: hookCodex, Path: "/home/u/.codex/hooks.json", State: hookMissing},
		{Target: hookCursor, Path: "/home/u/.cursor/hooks.json", State: hookInvalid, Detail: "parse /home/u/.cursor/hooks.json: invalid character"},
	}}
	out := formatStatus(report, true)
	for _, want := range []string{
		"claude: installed (/home/u/.claude/settings.json)",
		"cline: outdated (/home/u/Documents/Cline/Hooks/PostToolUse): legacy managed hook; run replacement commands",
		"codex: missing (/home/u/.codex/hooks.json)",
		"cursor: invalid (/home/u/.cursor/hooks.json): parse /home/u/.cursor/hooks.json: invalid character",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want %q", out, want)
		}
	}
}
