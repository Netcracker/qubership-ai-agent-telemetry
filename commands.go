package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// applyConfigure updates the existing settings without changing path policy.
func applyConfigure(
	configDir, endpoint, caPath, token, repoAllow string,
	delivery deliverySettingOverrides,
) error {
	return applyConfigureWithPath(
		configDir, endpoint, caPath, token, repoAllow, pathAllowUpdate{}, delivery,
	)
}

type pathAllowUpdate struct {
	Patterns []string
	Set      bool
	Clear    bool
}

// applyConfigureWithPath writes only the settings supplied by the caller. It
// validates a complete path-policy replacement before changing any files.
func applyConfigureWithPath(
	configDir, endpoint, caPath, token, repoAllow string,
	pathUpdate pathAllowUpdate,
	delivery deliverySettingOverrides,
) error {
	if err := validateCollectorEndpoint(endpoint); err != nil {
		return err
	}
	if pathUpdate.Set && pathUpdate.Clear {
		return fmt.Errorf("--path-allow and --clear-path-allow cannot be combined")
	}
	if pathUpdate.Set {
		if len(pathUpdate.Patterns) == 0 {
			return fmt.Errorf("path allow list must not be empty")
		}
		if err := validatePathAllow(pathUpdate.Patterns); err != nil {
			return err
		}
	}
	updates := map[string]string{}
	if endpoint != "" {
		updates["AI_AGENT_TELEMETRY_ENDPOINT"] = endpoint
	}
	if token != "" {
		updates["AI_AGENT_TELEMETRY_TOKEN"] = token
	}
	if delivery.BufferCap != "" {
		updates[envBufferCap] = delivery.BufferCap
	}
	if delivery.FlushTimeout != "" {
		updates[envFlushTimeout] = delivery.FlushTimeout
	}
	if repoAllow == "" && repoAllowUnset(configDir) {
		repoAllow = defaultRepoAllow
	}
	if len(updates) > 0 {
		if err := writeEnvFile(configDir, updates); err != nil {
			return err
		}
	}
	if repoAllow != "" {
		if err := writeRepoAllowFile(configDir, repoAllow); err != nil {
			return err
		}
	}
	if caPath != "" {
		if err := copyCAFile(configDir, caPath); err != nil {
			return err
		}
	}
	if pathUpdate.Set {
		if err := writePathAllowFile(configDir, pathUpdate.Patterns); err != nil {
			return err
		}
	} else if pathUpdate.Clear {
		if err := os.Remove(filepath.Join(configDir, pathAllowFileName)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func validateCollectorEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err == nil && strings.EqualFold(parsed.Scheme, "https") && parsed.Hostname() != "" &&
		strings.HasSuffix(parsed.Path, "/v1/logs") {
		return nil
	}
	return fmt.Errorf(
		"collector endpoint %q must use HTTPS, include a host, and end in /v1/logs; "+
			"use https://collector.example/v1/logs",
		endpoint,
	)
}

func writePathAllowFile(configDir string, patterns []string) error {
	if err := validatePathAllow(patterns); err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	var b strings.Builder
	for _, pattern := range patterns {
		fmt.Fprintln(&b, pattern)
	}
	path := filepath.Join(configDir, pathAllowFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func repoAllowUnset(configDir string) bool {
	if os.Getenv(envRepoAllow) != "" {
		return false
	}
	if len(loadRepoAllowFile(filepath.Join(configDir, repoAllowFileName))) > 0 {
		return false
	}
	return true
}

func writeRepoAllowFile(configDir, repoAllow string) error {
	allow := splitList(repoAllow)
	if len(allow) == 0 {
		return nil
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	var b strings.Builder
	for _, pat := range allow {
		fmt.Fprintln(&b, pat)
	}
	path := filepath.Join(configDir, repoAllowFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeEnvFile merges updates into the env file under configDir and writes it
// back atomically (temp file + rename) with 0600 permissions, since the file
// may hold the token. Existing keys not in updates are preserved, so callers
// can set the endpoint and the token in separate steps. Sorted output makes the
// write idempotent for an unchanged set of values.
func writeEnvFile(configDir string, updates map[string]string) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(configDir, "env")

	merged := loadEnvFile(path)
	for k, v := range updates {
		merged[k] = v
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf []byte
	for _, k := range keys {
		buf = append(buf, fmt.Sprintf("%s=%s\n", k, merged[k])...)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// selftestSkill is the marker skill name carried by a probe event. The
// collector and dashboards filter on it so a probe never counts as real
// skill usage.
const selftestSkill = "__selftest__"

// selftestResult reports what the live probe proved.
type selftestResult struct {
	Delivered bool // the collector accepted the probe and it left the outbox
	Sent      int  // events sent in the flush that carried the probe
}

// runSelftest sends one real, marked probe event and confirms the pipeline
// works end to end up to ingest: the collector accepted it (HTTP 200) and the
// probe left the outbox. This is the guarantee available without read access to
// the store. An empty endpoint is a configuration error, not a delivery
// failure — the machine is not configured.
func runSelftest(s *Outbox, endpoint, token string, tlsConfig *tls.Config, timeout time.Duration) (selftestResult, error) {
	if endpoint == "" {
		return selftestResult{}, errors.New("no endpoint: machine is not configured")
	}
	probe := newSelftestProbe(time.Now().UTC())
	if err := s.Enqueue(probe); err != nil {
		return selftestResult{}, err
	}
	sent, err := Flush(s, endpoint, token, tlsConfig, timeout)
	if err != nil {
		return selftestResult{Sent: sent}, err
	}
	return selftestResult{Delivered: probesRemaining(s) == 0, Sent: sent}, nil
}

// probesRemaining counts probe events still buffered — used to confirm the
// probe actually left the outbox after a flush.
func probesRemaining(s *Outbox) int {
	names, err := s.List()
	if err != nil {
		return 0
	}
	n := 0
	for _, name := range names {
		if ev, err := s.Read(name); err == nil && ev.Agent == selftestAgent {
			if payload, ok := ev.Payload.(SkillPayload); ok && payload.SkillName == selftestSkill {
				n++
			}
		}
	}
	return n
}

// statusReport is the read-only diagnosis the configure skill reads to decide
// what, if anything, is missing. It never sends anything (see selftest for the
// live check).
type statusReport struct {
	Version           string
	ConfigDir         string
	Endpoint          string
	Configured        bool
	CAFound           bool
	RepoScope         string
	PathScope         string
	PathAllowList     []string
	PathAllowError    string
	Buffered          int
	LastFlush         string
	LastDeliveryError string
	Hooks             []hookStatus
	BufferCap         int
	FlushTimeout      time.Duration
}

// gatherStatus inspects the outbox and config dir against an already-resolved
// endpoint. A machine is configured once it has an endpoint to send to.
func gatherStatus(
	s *Outbox,
	configDir, endpoint string,
	policy telemetryPolicy,
	settings deliverySettings,
) statusReport {
	r := statusReport{
		Version:       version,
		ConfigDir:     configDir,
		Endpoint:      endpoint,
		Configured:    endpoint != "",
		RepoScope:     policy.repoScope(),
		PathScope:     policy.pathScope(),
		PathAllowList: append([]string(nil), policy.PathAllowList...),
		LastFlush:     "never",
		Hooks:         gatherHookStatus(userHomeDir()),
		BufferCap:     settings.BufferCap,
		FlushTimeout:  settings.FlushTimeout,
	}
	if policy.PathAllowError != nil {
		r.PathAllowError = policy.PathAllowError.Error()
	}
	if configDir != "" {
		if _, err := os.Stat(filepath.Join(configDir, caFileName)); err == nil {
			r.CAFound = true
		}
	}
	if names, err := s.List(); err == nil {
		r.Buffered = len(names)
	}
	if fi, err := os.Stat(filepath.Join(s.Dir, flushStampName)); err == nil {
		r.LastFlush = fi.ModTime().UTC().Format(time.RFC3339)
	}
	if msg, ok := readLastDeliveryError(s); ok {
		r.LastDeliveryError = msg
	}
	return r
}

// formatStatus renders the report for a human and, when the machine is not yet
// configured, says so plainly so the next step is obvious.
func formatStatus(r statusReport, verbose bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "version: %s\n", r.Version)
	fmt.Fprintf(&b, "config_dir: %s\n", r.ConfigDir)
	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = "(unset)"
	}
	fmt.Fprintf(&b, "endpoint: %s\n", endpoint)
	repoScope := r.RepoScope
	if repoScope == "" {
		repoScope = "all"
	}
	fmt.Fprintf(&b, "repo_scope: %s\n", repoScope)
	pathScope := r.PathScope
	if pathScope == "" {
		pathScope = "not configured"
	}
	fmt.Fprintf(&b, "path_scope: %s\n", pathScope)
	fmt.Fprintf(&b, "ca: %s\n", caState(r.CAFound))
	fmt.Fprintf(&b, "buffered: %d\n", r.Buffered)
	fmt.Fprintf(&b, "last_flush_attempt: %s\n", r.LastFlush)
	fmt.Fprint(&b, "hooks:\n")
	for _, hook := range r.Hooks {
		fmt.Fprintf(&b, "  %s: %s", hook.Target, hook.State)
		if verbose {
			fmt.Fprintf(&b, " (%s)", hook.Path)
			if hook.Detail != "" {
				fmt.Fprintf(&b, ": %s", hook.Detail)
			}
		}
		fmt.Fprintln(&b)
	}
	if r.Configured {
		fmt.Fprint(&b, "state: configured\n")
	} else {
		fmt.Fprint(&b, "state: not configured — run `ai-agent-telemetry configure` to set the endpoint\n")
	}
	if verbose {
		fmt.Fprint(&b, "configuration:\n")
		fmt.Fprintf(&b, "  buffer_cap: %d\n", r.BufferCap)
		fmt.Fprintf(&b, "  flush_timeout: %s\n", r.FlushTimeout)
		if len(r.PathAllowList) > 0 {
			fmt.Fprint(&b, "  path_allow:\n")
			for _, pattern := range r.PathAllowList {
				fmt.Fprintf(&b, "    - %s\n", pattern)
			}
		}
		fmt.Fprint(&b, "diagnostics:\n")
		if r.PathAllowError != "" {
			fmt.Fprintf(&b, "  path_allow_error: %s\n", r.PathAllowError)
		}
		if r.LastDeliveryError != "" {
			fmt.Fprintf(&b, "  last_delivery_error: %s\n", r.LastDeliveryError)
		} else {
			fmt.Fprint(&b, "  last_delivery_error: none recorded\n")
		}
	} else if r.Buffered > 0 && r.LastDeliveryError != "" {
		fmt.Fprint(&b, "diagnostics: delivery errors found; run `ai-agent-telemetry status --verbose`\n")
	}
	return b.String()
}

func caState(found bool) string {
	if found {
		return "ca.crt found"
	}
	return "none (system trust store)"
}
