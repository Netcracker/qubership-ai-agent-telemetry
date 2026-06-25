package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

const (
	bufferCap       = 100
	flushCountN     = 10
	flushIntervalT  = 60 * time.Second
	flushTimeout    = 2 * time.Second
	selftestTimeout = 10 * time.Second
)

func main() {
	os.Exit(run(os.Args[1:], func(s string) { fmt.Print(s) }))
}

func run(args []string, stdout func(string)) int {
	if len(args) == 0 {
		stdout("usage: ai-agent-telemetry <ingest|flush|status|selftest|configure|update-check|version>\n")
		return 2
	}
	switch args[0] {
	case "version":
		stdout(version + "\n")
		return 0
	case "update-check":
		// Report whether a newer release exists. Always exits 0 — a check is
		// advisory and must never become a reason a caller fails.
		stdout(formatUpdateCheck(gatherUpdateCheck(version, func() (string, error) {
			return latestReleaseTag(updateCheckTimeout)
		})))
		return 0
	case "configure":
		endpoint, caPath := parseConfigureFlags(args[1:])
		cfg := pkgConfigDir()
		if cfg == "" {
			fmt.Fprintln(os.Stderr, "configure: no user config directory available")
			return 1
		}
		token := readSecret("Collector token (leave blank to skip): ")
		if err := applyConfigure(cfg, endpoint, caPath, token); err != nil {
			fmt.Fprintln(os.Stderr, "configure:", err)
			return 1
		}
		s, err := DefaultOutbox()
		if err != nil {
			fmt.Fprintln(os.Stderr, "outbox:", err)
			return 1
		}
		stdout(formatStatus(gatherStatus(s, cfg, resolveEndpoint(""))))
		return 0
	case "selftest":
		s, err := DefaultOutbox()
		if err != nil {
			fmt.Fprintln(os.Stderr, "outbox:", err)
			return 1
		}
		tlsCfg, cerr := caTLSConfig(pkgConfigDir())
		if cerr != nil {
			fmt.Fprintln(os.Stderr, "ca:", cerr)
		}
		res, err := runSelftest(s, resolveEndpoint(""), resolveToken(), tlsCfg, selftestTimeout)
		if err != nil {
			stdout("selftest: failed — " + err.Error() + "\n")
			return 1
		}
		if !res.Delivered {
			stdout("selftest: probe not confirmed (try again)\n")
			return 1
		}
		stdout("selftest: ok — probe accepted by the collector and cleared from the outbox\n")
		return 0
	case "ingest":
		agent, endpoint := parseFlags(args[1:])
		endpoint = resolveEndpoint(endpoint)
		s, err := DefaultOutbox()
		if err != nil {
			fmt.Fprintln(os.Stderr, "outbox:", err)
			return 0 // never fail the hook
		}
		raw, _ := io.ReadAll(os.Stdin)
		return ingest(s, agent, endpoint, raw, gitRemote)
	case "flush":
		_, endpoint := parseFlags(args[1:])
		endpoint = resolveEndpoint(endpoint)
		s, err := DefaultOutbox()
		if err != nil {
			fmt.Fprintln(os.Stderr, "outbox:", err)
			return 0
		}
		tlsCfg, err := caTLSConfig(pkgConfigDir())
		if err != nil {
			fmt.Fprintln(os.Stderr, "ca:", err)
		}
		if _, err := Flush(s, endpoint, resolveToken(), tlsCfg, flushTimeout); err != nil {
			fmt.Fprintln(os.Stderr, "flush:", err)
		}
		return 0
	case "status":
		s, err := DefaultOutbox()
		if err != nil {
			fmt.Fprintln(os.Stderr, "outbox:", err)
			return 0
		}
		stdout(formatStatus(gatherStatus(s, pkgConfigDir(), resolveEndpoint(""))))
		return 0
	default:
		stdout("unknown command: " + args[0] + "\n")
		return 2
	}
}

// parseFlags reads --agent= and --endpoint= without a flag framework (minimal).
func parseFlags(args []string) (agent, endpoint string) {
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--agent="):
			agent = strings.TrimPrefix(a, "--agent=")
		case strings.HasPrefix(a, "--endpoint="):
			endpoint = strings.TrimPrefix(a, "--endpoint=")
		}
	}
	return agent, endpoint
}

// parseConfigureFlags reads the configure flags: --endpoint= and --ca=.
func parseConfigureFlags(args []string) (endpoint, ca string) {
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--endpoint="):
			endpoint = strings.TrimPrefix(a, "--endpoint=")
		case strings.HasPrefix(a, "--ca="):
			ca = strings.TrimPrefix(a, "--ca=")
		}
	}
	return endpoint, ca
}

// readSecret prompts on stderr and reads a line without echoing it, so the
// token never lands in a terminal scrollback. It prefers the controlling
// terminal (/dev/tty) so it still works under `curl | sh`, where stdin is the
// pipe; it falls back to stdin when stdin is itself a terminal (e.g. the
// Windows console). Returns "" if no terminal is available.
func readSecret(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer tty.Close()
		b, rerr := term.ReadPassword(int(tty.Fd()))
		fmt.Fprintln(os.Stderr)
		if rerr == nil {
			return strings.TrimSpace(string(b))
		}
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, rerr := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if rerr == nil {
			return strings.TrimSpace(string(b))
		}
	}
	fmt.Fprintln(os.Stderr)
	return ""
}

// ingest is the per-event path: parse, enqueue, rotate, opportunistic flush.
// It returns 0 even on error — a hook must never fail the agent turn.
func ingest(s *Outbox, agent, endpoint string, stdin []byte, remote remoteResolver) int {
	events, err := detect(agent, stdin, remote, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, "detect:", err)
		return 0
	}
	for _, ev := range events {
		if err := s.Enqueue(ev); err != nil {
			fmt.Fprintln(os.Stderr, "enqueue:", err)
		}
	}
	if _, err := s.Rotate(bufferCap); err != nil {
		fmt.Fprintln(os.Stderr, "rotate:", err)
	}
	if shouldFlush(s, flushCountN, flushIntervalT) {
		touchFlushStamp(s)
		tlsCfg, cerr := caTLSConfig(pkgConfigDir())
		if cerr != nil {
			fmt.Fprintln(os.Stderr, "ca:", cerr)
		}
		if _, err := Flush(s, endpoint, resolveToken(), tlsCfg, flushTimeout); err != nil {
			fmt.Fprintln(os.Stderr, "flush:", err)
		}
	}
	return 0
}

// shouldFlush is true when there is something to send AND either enough has
// piled up or enough time has passed since the last attempt.
func shouldFlush(s *Outbox, countN int, intervalT time.Duration) bool {
	names, err := s.List()
	if err != nil || len(names) == 0 {
		return false
	}
	if len(names) >= countN {
		return true
	}
	fi, err := os.Stat(filepath.Join(s.Dir, flushStampName))
	if err != nil {
		return true // no prior attempt recorded
	}
	return time.Since(fi.ModTime()) >= intervalT
}

// touchFlushStamp records the time of a flush attempt (success or failure) so
// the throttle bounds retry frequency against a dead collector.
func touchFlushStamp(s *Outbox) {
	p := filepath.Join(s.Dir, flushStampName)
	now := time.Now()
	if err := os.WriteFile(p, []byte(now.UTC().Format(time.RFC3339)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "flush stamp:", err)
		return
	}
	_ = os.Chtimes(p, now, now)
}

// gitRemote best-effort resolves origin URL for a working dir; "" on failure.
func gitRemote(cwd string) string {
	cmd := exec.Command("git", "-C", cwd, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return sanitizeRemote(strings.TrimSpace(string(out)))
}
