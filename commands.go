package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// applyConfigure is the deterministic core the skill and the one-liner both
// call: it writes only the fields it is given. The endpoint and token go into
// the env file (merged, so they can be set in separate runs); repository scope
// goes into repo-allow; a CA path is validated and copied to ca.crt. Empty
// fields are left untouched, which keeps re-running configure safe.
func applyConfigure(
	configDir, endpoint, caPath, token, repoAllow string,
	delivery deliverySettingOverrides,
) error {
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
	probe := SkillEvent{
		Agent:     "selftest",
		SessionID: newUUID(),
		Skill:     selftestSkill,
		TS:        time.Now().UTC(),
	}
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
		if ev, err := s.Read(name); err == nil && ev.Skill == selftestSkill {
			n++
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
	Buffered          int
	LastFlush         string
	LastDeliveryError string
	Hooks             []hookStatus
}

// gatherStatus inspects the outbox and config dir against an already-resolved
// endpoint. A machine is configured once it has an endpoint to send to.
func gatherStatus(s *Outbox, configDir, endpoint string, policy telemetryPolicy) statusReport {
	r := statusReport{
		Version:    version,
		ConfigDir:  configDir,
		Endpoint:   endpoint,
		Configured: endpoint != "",
		RepoScope:  policy.repoScope(),
		LastFlush:  "never",
		Hooks:      gatherHookStatus(userHomeDir()),
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
		fmt.Fprint(&b, "diagnostics:\n")
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
