package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestResolveEndpointFromFlagWins(t *testing.T) {
	if got := resolveEndpointFrom("https://flag/v1/logs", "https://env/v1/logs", "https://file/v1/logs"); got != "https://flag/v1/logs" {
		t.Fatalf("got %q, want the flag value", got)
	}
}

func TestResolveEndpointFromEnvBeatsFile(t *testing.T) {
	if got := resolveEndpointFrom("", "https://env/v1/logs", "https://file/v1/logs"); got != "https://env/v1/logs" {
		t.Fatalf("got %q, want the env value over the file", got)
	}
}

func TestResolveEndpointFromFileFallback(t *testing.T) {
	if got := resolveEndpointFrom("", "", "https://file/v1/logs"); got != "https://file/v1/logs" {
		t.Fatalf("got %q, want the env-file fallback", got)
	}
}

func TestResolveEndpointFromNeitherIsEmpty(t *testing.T) {
	if got := resolveEndpointFrom("", "", ""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestResolveDeliverySettingsFromDefaults(t *testing.T) {
	got := resolveDeliverySettingsFrom(nil, nil, func(string) {})
	if got.BufferCap != 100 || got.FlushTimeout != 2*time.Second {
		t.Fatalf("settings = %+v, want buffer 100 and timeout 2s", got)
	}
}

func TestResolveDeliverySettingsFromFile(t *testing.T) {
	got := resolveDeliverySettingsFrom(nil, map[string]string{
		envBufferCap:    "250",
		envFlushTimeout: "5s",
	}, func(string) {})
	if got.BufferCap != 250 || got.FlushTimeout != 5*time.Second {
		t.Fatalf("settings = %+v, want buffer 250 and timeout 5s", got)
	}
}

func TestResolveDeliverySettingsFromProcessEnvWins(t *testing.T) {
	got := resolveDeliverySettingsFrom(
		map[string]string{envBufferCap: "1000", envFlushTimeout: "30s"},
		map[string]string{envBufferCap: "200", envFlushTimeout: "5s"},
		func(string) {},
	)
	if got.BufferCap != 1000 || got.FlushTimeout != 30*time.Second {
		t.Fatalf("settings = %+v, want process environment values", got)
	}
}

func TestResolveDeliverySettingsFromInvalidOverrideWarnsAndUsesDefaults(t *testing.T) {
	var warnings []string
	got := resolveDeliverySettingsFrom(
		map[string]string{envBufferCap: "zero", envFlushTimeout: "-1s"},
		map[string]string{envBufferCap: "200", envFlushTimeout: "5s"},
		func(message string) { warnings = append(warnings, message) },
	)
	if got.BufferCap != 100 || got.FlushTimeout != 2*time.Second {
		t.Fatalf("settings = %+v, want defaults", got)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{
		`AI_AGENT_TELEMETRY_BUFFER_CAP value "zero"`,
		"using default 100",
		`AI_AGENT_TELEMETRY_FLUSH_TIMEOUT value "-1s"`,
		"using default 2s",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings = %q, want %q", joined, want)
		}
	}
}

func TestParseDeliverySettingsRejectsNonpositiveAndMalformedValues(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "many"} {
		if _, err := parseBufferCap(value); err == nil {
			t.Fatalf("parseBufferCap(%q) succeeded, want error", value)
		}
	}
	for _, value := range []string{"", "0s", "-1s", "long"} {
		if _, err := parseFlushTimeout(value); err == nil {
			t.Fatalf("parseFlushTimeout(%q) succeeded, want error", value)
		}
	}
}

func TestParseEnvReadsKeyValueLines(t *testing.T) {
	in := []byte("AI_AGENT_TELEMETRY_ENDPOINT=https://otel.example/v1/logs\nAI_AGENT_TELEMETRY_TOKEN=abc123\n")
	got := parseEnv(in)
	if got["AI_AGENT_TELEMETRY_ENDPOINT"] != "https://otel.example/v1/logs" {
		t.Fatalf("endpoint = %q", got["AI_AGENT_TELEMETRY_ENDPOINT"])
	}
	if got["AI_AGENT_TELEMETRY_TOKEN"] != "abc123" {
		t.Fatalf("token = %q", got["AI_AGENT_TELEMETRY_TOKEN"])
	}
}

func TestParseEnvSkipsBlanksCommentsAndTrims(t *testing.T) {
	in := []byte("\n# a comment\n  AI_AGENT_TELEMETRY_ENDPOINT = https://x/v1/logs  \nnonsense-without-equals\n")
	got := parseEnv(in)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %v", len(got), got)
	}
	if got["AI_AGENT_TELEMETRY_ENDPOINT"] != "https://x/v1/logs" {
		t.Fatalf("endpoint = %q (key/value not trimmed)", got["AI_AGENT_TELEMETRY_ENDPOINT"])
	}
}

func TestLoadEnvFileMissingReturnsEmpty(t *testing.T) {
	got := loadEnvFile(filepath.Join(t.TempDir(), "ai-agent-telemetry", "env"))
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0 for missing file", len(got))
	}
}

func TestLoadEnvFileReadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "env")
	if err := os.WriteFile(p, []byte("AI_AGENT_TELEMETRY_ENDPOINT=https://disk/v1/logs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadEnvFile(p)
	if got["AI_AGENT_TELEMETRY_ENDPOINT"] != "https://disk/v1/logs" {
		t.Fatalf("endpoint = %q", got["AI_AGENT_TELEMETRY_ENDPOINT"])
	}
}

func TestParseRepoAllowSkipsBlanksCommentsAndSplitsLists(t *testing.T) {
	in := []byte(`
# organization scope
github.com/Netcracker/*

github.com/Qubership/*, gitlab.example.com/qubership/**
`)
	got := parseRepoAllow(in)
	want := []string{"github.com/Netcracker/*", "github.com/Qubership/*", "gitlab.example.com/qubership/**"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("repo allow = %v, want %v", got, want)
	}
}

func TestResolveTokenFromEnvWins(t *testing.T) {
	if got := resolveTokenFrom("env-token", t.TempDir()); got != "env-token" {
		t.Fatalf("got %q, want env-token", got)
	}
}

func TestResolveTokenFromFileFallback(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "ai-agent-telemetry")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "token"), []byte("  file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveTokenFrom("", dir); got != "file-token" {
		t.Fatalf("got %q, want file-token (trimmed)", got)
	}
}

func TestResolveTokenFromEnvFile(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "ai-agent-telemetry")
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "env"), []byte("AI_AGENT_TELEMETRY_TOKEN=provisioned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveTokenFrom("", dir); got != "provisioned" {
		t.Fatalf("got %q, want the token from the env file", got)
	}
}

func TestResolveTokenEnvFileBeatsLegacyTokenFile(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "ai-agent-telemetry")
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "env"), []byte("AI_AGENT_TELEMETRY_TOKEN=from-env-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "token"), []byte("from-legacy-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveTokenFrom("", dir); got != "from-env-file" {
		t.Fatalf("got %q, want the env-file token to win over the legacy token file", got)
	}
}

func TestResolveTokenFromNeitherIsEmpty(t *testing.T) {
	if got := resolveTokenFrom("", t.TempDir()); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// selfSignedPEM returns a valid self-signed certificate in PEM form for tests.
func selfSignedPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestCopyCAFileWritesCanonicalCert(t *testing.T) {
	dir := filepath.Join(t.TempDir(), pkgName)
	src := filepath.Join(t.TempDir(), "source.crt")
	pemBytes := selfSignedPEM(t)
	if err := os.WriteFile(src, pemBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyCAFile(dir, src); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(pemBytes) {
		t.Fatal("copied cert bytes differ from source")
	}
}

func TestCopyCAFileRejectsMissingSource(t *testing.T) {
	dir := filepath.Join(t.TempDir(), pkgName)
	if err := copyCAFile(dir, filepath.Join(t.TempDir(), "nope.crt")); err == nil {
		t.Fatal("want error for missing source")
	}
}

func TestCopyCAFileRejectsNonPEM(t *testing.T) {
	dir := filepath.Join(t.TempDir(), pkgName)
	src := filepath.Join(t.TempDir(), "bad.crt")
	if err := os.WriteFile(src, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyCAFile(dir, src); err == nil {
		t.Fatal("want error for non-PEM input")
	}
}

func TestCATLSConfigNilWhenNoCertFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), pkgName)
	cfg, err := caTLSConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Fatal("want nil config when no ca.crt (fall back to system trust)")
	}
}

func TestCATLSConfigBuildsPoolWhenCertPresent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), pkgName)
	src := filepath.Join(t.TempDir(), "source.crt")
	if err := os.WriteFile(src, selfSignedPEM(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyCAFile(dir, src); err != nil {
		t.Fatal(err)
	}
	cfg, err := caTLSConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatal("want a TLS config with a populated root pool")
	}
}

var uuidV4Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestResolveMachineIDMintsAndPersists(t *testing.T) {
	dir := t.TempDir()
	id := resolveMachineIDFrom(dir)
	if !uuidV4Re.MatchString(id) {
		t.Fatalf("not a v4 UUID: %q", id)
	}
	path := filepath.Join(dir, "ai-agent-telemetry", "machine-id")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("id not persisted: %v", err)
	}
	if got := string(b); got != id+"\n" {
		t.Fatalf("file = %q, want %q", got, id+"\n")
	}
}

func TestResolveMachineIDStableAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	first := resolveMachineIDFrom(dir)
	second := resolveMachineIDFrom(dir)
	if first != second {
		t.Fatalf("id changed between calls: %q vs %q", first, second)
	}
}
