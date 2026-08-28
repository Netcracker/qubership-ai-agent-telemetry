package main

import (
	"fmt"
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
	flushCountN     = 10
	flushIntervalT  = 60 * time.Second
	selftestTimeout = 10 * time.Second
)

func main() {
	os.Exit(execute(os.Args[1:], defaultAppDeps()))
}

// parseIngestFlags keeps the execpolicy-approved Codex hook shape exact. Codex
// execution policy matches command prefixes, so accepting a trailing endpoint
// override would let an approved hook redirect buffered events and credentials.
func parseIngestFlags(args []string) (agent, endpoint string, err error) {
	if len(args) > 0 && args[0] == "--agent=codex" && len(args) != 1 {
		return "", "", fmt.Errorf("the Codex hook does not accept additional arguments")
	}
	agent, endpoint = parseFlags(args)
	return agent, endpoint, nil
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

func configureEndpoint(flag string) string {
	if flag != "" {
		return flag
	}
	if resolveEndpoint("") != "" {
		return ""
	}
	return readLine("Collector endpoint (for example, https://collector.example/v1/logs; leave blank to skip): ")
}

// readLine prompts on stderr and reads one line from the controlling terminal.
// It mirrors readSecret's /dev/tty preference so `curl | sh` installers can
// still hand interactive configuration to the downloaded binary.
func readLine(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer func() { _ = tty.Close() }()
		var value string
		if _, err := fmt.Fscanln(tty, &value); err == nil {
			return strings.TrimSpace(value)
		}
		return ""
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		var value string
		if _, err := fmt.Fscanln(os.Stdin, &value); err == nil {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// readSecret prompts on stderr and reads a line without echoing it, so the
// token never lands in a terminal scrollback. It prefers the controlling
// terminal (/dev/tty) so it also works under `curl | sh`, where stdin is the
// pipe; it falls back to stdin when stdin is itself a terminal (e.g. the
// Windows console). Returns "" if no terminal is available.
func readSecret(prompt string) string {
	fmt.Fprint(os.Stderr, prompt)
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer func() { _ = tty.Close() }()
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
func ingest(
	s *Outbox,
	agent, endpoint string,
	stdin []byte,
	remote remoteResolver,
	settings deliverySettings,
) int {
	events, err := detect(agent, stdin, remote, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, "detect:", err)
		return 0
	}
	cache := newRepoRemoteCache(gitRemotes)
	events = filterEventsByPolicy(events, resolveTelemetryPolicy(), cache.remotesFor)
	for _, ev := range events {
		if err := s.Enqueue(ev); err != nil {
			fmt.Fprintln(os.Stderr, "enqueue:", err)
		}
	}
	if _, err := s.Rotate(settings.BufferCap); err != nil {
		fmt.Fprintln(os.Stderr, "rotate:", err)
	}
	if shouldFlush(s, flushCountN, flushIntervalT) {
		touchFlushStamp(s)
		tlsCfg, cerr := caTLSConfig(pkgConfigDir())
		if cerr != nil {
			fmt.Fprintln(os.Stderr, "ca:", cerr)
		}
		if _, err := Flush(s, endpoint, resolveToken(), tlsCfg, settings.FlushTimeout); err != nil {
			fmt.Fprintln(os.Stderr, "flush:", err)
		}
	}
	return 0
}

type repoRemoteCache struct {
	remotesFn func(string) []string
	remotes   map[string][]string
}

func newRepoRemoteCache(remotesFn func(string) []string) *repoRemoteCache {
	return &repoRemoteCache{
		remotesFn: remotesFn,
		remotes:   map[string][]string{},
	}
}

func (c *repoRemoteCache) remotesFor(cwd string) []string {
	if cwd == "" || c.remotesFn == nil {
		return nil
	}
	if v, ok := c.remotes[cwd]; ok {
		return v
	}
	v := c.remotesFn(cwd)
	c.remotes[cwd] = v
	return v
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
