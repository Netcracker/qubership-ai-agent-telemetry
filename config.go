package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const pkgName = "ai-agent-telemetry"

// configBase resolves the root under which the package config dir lives. It is
// the SAME XDG-style path on every OS — $XDG_CONFIG_HOME, else ~/.config — the
// way the binary already lives at ~/.local/bin on every OS. This is deliberate:
// os.UserConfigDir() returns %AppData% on Windows, which MSIX virtualizes for a
// packaged harness (Claude Desktop), so a packaged and a plain shell would
// resolve different config dirs. A home-relative path outside AppData is never
// virtualized, so all harnesses share one config. Returns "" when neither a
// config dir nor a home dir is available.
func configBase() string {
	return configBaseFrom(os.Getenv("XDG_CONFIG_HOME"), userHomeDir())
}

// configBaseFrom is the testable core: an explicit $XDG_CONFIG_HOME wins, else
// fall back to <home>/.config. Empty when both inputs are empty.
func configBaseFrom(xdg, home string) string {
	return xdgBaseFrom(xdg, home, ".config")
}

// xdgBaseFrom is the resolution both configBaseFrom and cacheBaseFrom share: an
// explicit $XDG_* dir wins, else fall back to <home>/<sub>. Empty when both
// inputs are empty.
func xdgBaseFrom(xdg, home, sub string) string {
	if x := strings.TrimSpace(xdg); x != "" {
		return x
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, sub)
}

// userHomeDir is a best-effort os.UserHomeDir() that returns "" on error rather
// than propagating one, since every caller treats an absent home as "no config".
func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// pkgConfigDir is the per-machine config directory holding durable configuration
// state: the env file, the CA certificate, the token, and the machine id. It is
// <configBase>/ai-agent-telemetry — a uniform ~/.config path on every
// OS. Returns "" when no config dir is available.
func pkgConfigDir() string {
	base := configBase()
	if base == "" {
		return ""
	}
	return filepath.Join(base, pkgName)
}

// pkgConfigPath joins name onto the package config dir, or "" if there is none.
func pkgConfigPath(name string) string {
	dir := pkgConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// pkgEnv loads the env file from the package config dir.
func pkgEnv() map[string]string {
	return loadEnvFile(pkgConfigPath("env"))
}

// resolveEndpoint returns the OTLP/HTTP collector URL. Precedence: an explicit
// --endpoint= flag, then the AI_AGENT_TELEMETRY_ENDPOINT environment variable
// (for CI and automation overrides), then the env file so the
// binary is self-sufficient when invoked directly (not through the bootstrap).
// Empty when none is set — the flush then becomes a no-op.
func resolveEndpoint(flag string) string {
	return resolveEndpointFrom(flag, os.Getenv("AI_AGENT_TELEMETRY_ENDPOINT"), pkgEnv()["AI_AGENT_TELEMETRY_ENDPOINT"])
}

// resolveEndpointFrom is the testable core: flag wins, then env, then the
// env-file value.
func resolveEndpointFrom(flag, env, fileVal string) string {
	if flag != "" {
		return flag
	}
	if env != "" {
		return env
	}
	return fileVal
}

// parseEnv reads KEY=VALUE lines into a map. Blank lines and lines starting
// with '#' are ignored, as are lines without '='. Keys and values are trimmed
// of surrounding whitespace. This is the format the env file uses,
// shared with the shell bootstrap so there is one config format per machine.
func parseEnv(b []byte) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// loadEnvFile parses the env file at path. A missing or unreadable file yields
// an empty map, not an error: an unconfigured machine is a valid state.
func loadEnvFile(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	return parseEnv(b)
}

// resolveToken returns the collector bearer token. It prefers the
// AI_AGENT_TELEMETRY_TOKEN environment variable and falls back to a per-user
// secret file at <configBase>/ai-agent-telemetry/token. Empty when
// neither is set — the flush then sends no Authorization header.
func resolveToken() string {
	return resolveTokenFrom(os.Getenv("AI_AGENT_TELEMETRY_TOKEN"), configBase())
}

// resolveTokenFrom is the testable core. Precedence: the env var, then the
// env file (the single config file configure writes), then the
// legacy standalone token file. Surrounding whitespace/newlines are trimmed.
func resolveTokenFrom(env, configDir string) string {
	if env != "" {
		return env
	}
	if configDir == "" {
		return ""
	}
	pkgDir := filepath.Join(configDir, pkgName)
	if tok := loadEnvFile(filepath.Join(pkgDir, "env"))["AI_AGENT_TELEMETRY_TOKEN"]; tok != "" {
		return tok
	}
	b, err := os.ReadFile(filepath.Join(pkgDir, "token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// caFileName is the well-known name the CLI auto-discovers under the config
// dir. The configure command copies the deployment CA here; the flush adds it to the
// trust pool when present.
const caFileName = "ca.crt"

// copyCAFile validates that src is a PEM bundle containing at least one
// certificate, then copies it to <configDir>/ca.crt. Validating up front turns
// a wrong path into a clear configure error instead of a silent TLS failure
// at send time. The cert is not secret, so 0644 is fine.
func copyCAFile(configDir, src string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return errors.New("no PEM certificate found in " + src)
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	dst := filepath.Join(configDir, caFileName)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// caTLSConfig auto-discovers the CA at <configDir>/ca.crt. When the file is
// absent it returns (nil, nil): the caller leaves TLS at its default, which
// trusts the system store. When present it appends the CA to a copy of the
// system pool, so trust is additive — the machine keeps trusting public and
// corporate roots and adds the private CA on top. A missing config dir or an
// unreadable system pool degrades to a pool holding just the CA.
func caTLSConfig(configDir string) (*tls.Config, error) {
	if configDir == "" {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Join(configDir, caFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(b) {
		return nil, errors.New("ca.crt holds no PEM certificate")
	}
	return &tls.Config{RootCAs: pool}, nil
}

// resolveMachineID returns a stable, anonymous identifier for this install.
// It is a random UUID generated once and persisted under the per-user config
// dir, never derived from hardware or the username. The collector uses it only
// to tell installs apart (e.g. to spot one install skewing skill-usage counts),
// not to identify the user. Returns "" if no config dir is available.
func resolveMachineID() string {
	dir := configBase()
	if dir == "" {
		return ""
	}
	return resolveMachineIDFrom(dir)
}

// resolveMachineIDFrom is the testable core: read the id file under configDir,
// or create it atomically on first run. O_EXCL guards against two concurrent
// processes each minting a different id — the loser re-reads the winner's file.
func resolveMachineIDFrom(configDir string) string {
	pkgDir := filepath.Join(configDir, pkgName)
	path := filepath.Join(pkgDir, "machine-id")
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id
		}
	}
	id := newUUID()
	if id == "" {
		return ""
	}
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		return id // usable this run, just not persisted
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		// Lost the race or cannot create: prefer an already-written id.
		if b, rerr := os.ReadFile(path); rerr == nil {
			if existing := strings.TrimSpace(string(b)); existing != "" {
				return existing
			}
		}
		return id
	}
	_, _ = f.WriteString(id + "\n")
	_ = f.Close()
	return id
}

// newUUID returns a random RFC 4122 version 4 UUID, or "" if the system CSPRNG
// is unavailable.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
