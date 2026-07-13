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
		stdout(rootHelp())
		return 2
	}
	if output, code, handled := routeHelp(args); handled {
		stdout(output)
		return code
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
	case "self-update":
		if err := runSelfUpdate(version, stdout); err != nil {
			fmt.Fprintln(os.Stderr, "self-update:", err)
			return 1
		}
		return 0
	case "configure":
		opts, err := parseConfigureFlags(args[1:])
		if err != nil {
			help, _ := commandHelp("configure")
			stdout("configure: " + err.Error() + "\n\n" + help)
			return 2
		}
		cfg := pkgConfigDir()
		if cfg == "" {
			fmt.Fprintln(os.Stderr, "configure: no user config directory available")
			return 1
		}
		endpoint := configureEndpoint(opts.Endpoint)
		token := readSecret("Collector token (leave blank to skip): ")
		if err := applyConfigure(cfg, endpoint, opts.CAPath, token, opts.RepoAllow); err != nil {
			fmt.Fprintln(os.Stderr, "configure:", err)
			return 1
		}
		results := installHooks(userHomeDir(), opts.Hooks)
		s, err := DefaultOutbox()
		if err != nil {
			fmt.Fprintln(os.Stderr, "outbox:", err)
			return 1
		}
		stdout(formatStatus(gatherStatus(s, cfg, resolveEndpoint(""), resolveTelemetryPolicy()), false))
		if err := hookInstallError(results); err != nil {
			fmt.Fprintln(os.Stderr, "configure hooks:", err)
			return 1
		}
		if codexHookChanged(results) {
			stdout("restart Codex and approve `ai-agent-telemetry ingest --agent=codex` if prompted\n")
		}
		return 0
	case "hooks":
		targets, err := parseHooksCommand(args[1:])
		if err != nil {
			help, _ := commandHelp("hooks")
			stdout("hooks: " + err.Error() + "\n\n" + help)
			return 2
		}
		home := userHomeDir()
		if home == "" {
			stdout("hooks: no user home directory available\n")
			return 1
		}
		results := installHooks(home, targets)
		for _, result := range results {
			if result.Err != nil {
				stdout(fmt.Sprintf("%s: failed: %s\n", result.Target, result.Path))
				continue
			}
			state := "unchanged"
			if result.Changed {
				state = "installed"
			}
			stdout(fmt.Sprintf("%s: %s: %s\n", result.Target, state, result.Path))
		}
		if err := hookInstallError(results); err != nil {
			stdout("hooks: " + err.Error() + "\n")
			return 1
		}
		if codexHookChanged(results) {
			stdout("restart Codex and approve `ai-agent-telemetry ingest --agent=codex` if prompted\n")
		}
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
		agent, endpoint, err := parseIngestFlags(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "ingest:", err)
			return 0
		}
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
		verbose := parseStatusFlags(args[1:])
		s, err := DefaultOutbox()
		if err != nil {
			fmt.Fprintln(os.Stderr, "outbox:", err)
			return 0
		}
		stdout(formatStatus(gatherStatus(s, pkgConfigDir(), resolveEndpoint(""), resolveTelemetryPolicy()), verbose))
		return 0
	default:
		stdout("unknown command: " + args[0] + "\n\n" + rootHelp())
		return 2
	}
}

func parseStatusFlags(args []string) bool {
	for _, a := range args {
		if a == "--verbose" || a == "-v" {
			return true
		}
	}
	return false
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

// parseConfigureFlags reads configure options and rejects unsupported input.
func parseConfigureFlags(args []string) (configureOptions, error) {
	opts := configureOptions{Hooks: append([]hookTarget(nil), allHookTargets...)}
	var repoAllowValues []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--endpoint="):
			opts.Endpoint = strings.TrimPrefix(a, "--endpoint=")
		case strings.HasPrefix(a, "--ca="):
			opts.CAPath = strings.TrimPrefix(a, "--ca=")
		case strings.HasPrefix(a, "--repo-allow="):
			repoAllowValues = append(repoAllowValues, strings.TrimPrefix(a, "--repo-allow="))
		case a == "--repo-allow":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return configureOptions{}, fmt.Errorf("missing value for %q", a)
			}
			i++
			repoAllowValues = append(repoAllowValues, args[i])
		case strings.HasPrefix(a, "--hooks="):
			value := strings.TrimPrefix(a, "--hooks=")
			if value == "" {
				return configureOptions{}, fmt.Errorf("hook target value must not be empty")
			}
			hooks, err := parseHookTargets(value)
			if err != nil {
				return configureOptions{}, err
			}
			opts.Hooks = hooks
		case a == "--hooks":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return configureOptions{}, fmt.Errorf("missing value for %q", a)
			}
			i++
			if strings.TrimSpace(args[i]) == "" {
				return configureOptions{}, fmt.Errorf("hook target value must not be empty")
			}
			hooks, err := parseHookTargets(args[i])
			if err != nil {
				return configureOptions{}, err
			}
			opts.Hooks = hooks
		default:
			return configureOptions{}, fmt.Errorf("unknown configure flag %q", a)
		}
	}
	opts.RepoAllow = strings.Join(repoAllowValues, ",")
	return opts, nil
}

func configureEndpoint(flag string) string {
	if flag != "" {
		return flag
	}
	if endpoint := resolveEndpoint(""); endpoint != "" {
		return endpoint
	}
	return readLine("Collector endpoint (leave blank to skip): ")
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
func ingest(s *Outbox, agent, endpoint string, stdin []byte, remote remoteResolver) int {
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
