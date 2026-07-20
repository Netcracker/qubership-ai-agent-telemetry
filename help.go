package main

import (
	"fmt"
	"strings"
)

type commandHelpEntry struct {
	Name    string
	Summary string
	Usage   []string
	Options []helpOption
}

type helpOption struct {
	Syntax      string
	Description string
}

var commandHelpEntries = []commandHelpEntry{
	{
		Name:    "configure",
		Summary: "Write machine settings and install global harness hooks.",
		Usage:   []string{"ai-agent-telemetry configure [options]"},
		Options: []helpOption{
			{Syntax: "--endpoint=<url>", Description: "Set the OTLP/HTTP collector endpoint."},
			{Syntax: "--ca=<path>", Description: "Install a private CA certificate."},
			{Syntax: "--buffer-cap=<events>", Description: "Set the positive local event buffer capacity (default: 100)."},
			{Syntax: "--buffer-cap <events>", Description: "Use the equivalent space-separated form."},
			{Syntax: "--flush-timeout=<duration>", Description: "Set the positive ordinary flush timeout (default: 2s)."},
			{Syntax: "--flush-timeout <duration>", Description: "Use the equivalent space-separated form."},
			{Syntax: "--repo-allow=<pattern>", Description: "Allow a repository pattern; may be repeated."},
			{Syntax: "--repo-allow <pattern>", Description: "Use the equivalent space-separated form."},
			{Syntax: "--hooks=<targets>", Description: "Install all, none, or a comma-separated subset of claude,codex,cursor (default: all)."},
			{Syntax: "--hooks <targets>", Description: "Use the equivalent space-separated form."},
		},
	},
	{
		Name:    "hooks",
		Summary: "Install, repair, or remove global harness hooks without changing collector settings.",
		Usage: []string{
			"ai-agent-telemetry hooks install [--target=<list>]",
			"ai-agent-telemetry hooks uninstall [--target=<list>]",
		},
		Options: []helpOption{
			{Syntax: "--target=<list>", Description: "Operate on a comma-separated subset of claude,codex,cursor (default: all)."},
		},
	},
	{
		Name:    "status",
		Summary: "Show configuration, delivery backlog, and global hook status without sending data.",
		Usage:   []string{"ai-agent-telemetry status [--verbose]"},
		Options: []helpOption{
			{Syntax: "-v, --verbose", Description: "Show native paths and parse or delivery errors."},
		},
	},
	{
		Name:    "selftest",
		Summary: "Send a probe and confirm that the collector accepts it.",
		Usage:   []string{"ai-agent-telemetry selftest"},
	},
	{
		Name:    "ingest",
		Summary: "Read a harness hook payload from stdin, detect skill use, and queue events.",
		Usage:   []string{"ai-agent-telemetry ingest --agent=<harness> [--endpoint=<url>]"},
		Options: []helpOption{
			{Syntax: "--agent=<harness>", Description: "Parse a claude, codex, or cursor hook payload."},
			{Syntax: "--endpoint=<url>", Description: "Override the collector endpoint; not accepted by the Codex hook command."},
		},
	},
	{
		Name:    "flush",
		Summary: "Send queued events to the collector.",
		Usage:   []string{"ai-agent-telemetry flush [--endpoint=<url>]"},
		Options: []helpOption{
			{Syntax: "--endpoint=<url>", Description: "Override the collector endpoint."},
		},
	},
	{
		Name:    "update-check",
		Summary: "Check whether a newer GitHub release is available.",
		Usage:   []string{"ai-agent-telemetry update-check"},
	},
	{
		Name:    "self-update",
		Summary: "Download, verify, and install the latest GitHub release.",
		Usage:   []string{"ai-agent-telemetry self-update"},
	},
	{
		Name:    "version",
		Summary: "Print the build version.",
		Usage:   []string{"ai-agent-telemetry version"},
	},
}

func rootHelp() string {
	var b strings.Builder
	b.WriteString("Usage:\n")
	b.WriteString("  ai-agent-telemetry <command> [options]\n")
	b.WriteString("  ai-agent-telemetry help [command]\n\n")
	b.WriteString("Commands:\n")
	for _, entry := range commandHelpEntries {
		fmt.Fprintf(&b, "  %-14s %s\n", entry.Name, entry.Summary)
	}
	b.WriteString("\nRun `ai-agent-telemetry help <command>` for command details.\n")
	return b.String()
}

func commandHelp(name string) (string, bool) {
	entry, ok := findCommandHelp(name)
	if !ok {
		return "", false
	}

	var b strings.Builder
	b.WriteString(entry.Summary + "\n\nUsage:\n")
	for _, usage := range entry.Usage {
		b.WriteString("  " + usage + "\n")
	}
	if len(entry.Options) > 0 {
		b.WriteString("\nOptions:\n")
		for _, option := range entry.Options {
			fmt.Fprintf(&b, "  %-24s %s\n", option.Syntax, option.Description)
		}
	}
	b.WriteString("\n  -h, --help               Show this help.\n")
	return b.String(), true
}

func findCommandHelp(name string) (commandHelpEntry, bool) {
	for _, entry := range commandHelpEntries {
		if entry.Name == name {
			return entry, true
		}
	}
	return commandHelpEntry{}, false
}

func routeHelp(args []string) (output string, code int, handled bool) {
	if len(args) == 0 {
		return "", 0, false
	}

	if args[0] == "help" {
		switch len(args) {
		case 1:
			return rootHelp(), 0, true
		case 2:
			if help, ok := commandHelp(args[1]); ok {
				return help, 0, true
			}
			return fmt.Sprintf("unknown help topic %q\n\n%s", args[1], rootHelp()), 2, true
		default:
			return "help accepts at most one command\n\n" + rootHelp(), 2, true
		}
	}

	if isHelpFlag(args[0]) {
		if len(args) == 1 {
			return rootHelp(), 0, true
		}
		return "root help does not accept arguments\n\n" + rootHelp(), 2, true
	}

	if _, known := findCommandHelp(args[0]); !known {
		return "", 0, false
	}
	if len(args) >= 2 && isHelpToken(args[1]) {
		if len(args) == 2 {
			help, _ := commandHelp(args[0])
			return help, 0, true
		}
		help, _ := commandHelp(args[0])
		return fmt.Sprintf("%s help does not accept arguments\n\n%s", args[0], help), 2, true
	}
	if args[0] == "hooks" && len(args) >= 3 &&
		(args[1] == "install" || args[1] == "uninstall") && isHelpToken(args[2]) {
		help, _ := commandHelp("hooks")
		if len(args) == 3 {
			return help, 0, true
		}
		return "hooks help does not accept arguments\n\n" + help, 2, true
	}
	return "", 0, false
}

func isHelpFlag(value string) bool {
	return value == "-h" || value == "--help"
}

func isHelpToken(value string) bool {
	return value == "help" || isHelpFlag(value)
}
