package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newConfigureCommand(deps appDeps) *cobra.Command {
	var endpoint, caPath, hooks, bufferCap, flushTimeout string
	var repoAllow []string
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Write machine settings and install global harness hooks",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			targets := append([]hookTarget(nil), allHookTargets...)
			if cmd.Flags().Changed("hooks") {
				if strings.TrimSpace(hooks) == "" {
					return usageError{err: fmt.Errorf("hook target value must not be empty")}
				}
				var err error
				targets, err = parseHookTargets(hooks)
				if err != nil {
					return usageError{err: err}
				}
			}
			delivery := deliverySettingOverrides{BufferCap: bufferCap, FlushTimeout: flushTimeout}
			if bufferCap != "" {
				if _, err := parseBufferCap(bufferCap); err != nil {
					return usageError{err: fmt.Errorf("invalid --buffer-cap value %q: %w", bufferCap, err)}
				}
			}
			if flushTimeout != "" {
				if _, err := parseFlushTimeout(flushTimeout); err != nil {
					return usageError{err: fmt.Errorf("invalid --flush-timeout value %q: %w", flushTimeout, err)}
				}
			}

			configDir := pkgConfigDir()
			if configDir == "" {
				return fmt.Errorf("configure: no user config directory available")
			}
			collectorEndpoint := configureEndpoint(endpoint)
			token := readSecret("Collector token (leave blank to skip): ")
			if err := applyConfigure(configDir, collectorEndpoint, caPath, token, strings.Join(repoAllow, ","), delivery); err != nil {
				return fmt.Errorf("configure: %w", err)
			}
			results := installManagedHooks(deps.Home(), targets, cmd.ErrOrStderr())
			outbox, err := DefaultOutbox()
			if err != nil {
				return fmt.Errorf("outbox: %w", err)
			}
			settings := resolveDeliverySettings()
			_, _ = io.WriteString(cmd.OutOrStdout(), formatStatus(
				gatherStatus(outbox, configDir, resolveEndpoint(""), resolveTelemetryPolicy(), settings), false,
			))
			if err := hookInstallError(results); err != nil {
				return fmt.Errorf("configure hooks: %w", err)
			}
			if codexHookChanged(results) {
				_, _ = io.WriteString(cmd.OutOrStdout(), "restart Codex and approve `ai-agent-telemetry ingest --agent=codex` if prompted\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Set the OTLP/HTTP collector endpoint")
	cmd.Flags().StringVar(&caPath, "ca", "", "Install a private CA certificate")
	cmd.Flags().StringArrayVar(&repoAllow, "repo-allow", nil, "Allow a repository pattern (repeatable)")
	cmd.Flags().StringVar(&hooks, "hooks", "", "Install all, none, or a comma-separated hook subset")
	cmd.Flags().StringVar(&bufferCap, "buffer-cap", "", "Set the positive local event buffer capacity")
	cmd.Flags().StringVar(&flushTimeout, "flush-timeout", "", "Set the positive ordinary flush timeout")
	return cmd
}

func newHooksCommand(deps appDeps) *cobra.Command {
	parent := &cobra.Command{
		Use:   "hooks",
		Short: "Manage global harness hooks",
		Args:  usageArgs(cobra.NoArgs),
		RunE:  helpRunE,
	}
	var installTarget string
	install := &cobra.Command{
		Use:   "install",
		Short: "Install or repair global harness hooks",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("target") && installTarget == "" {
				return usageError{err: fmt.Errorf("hook target value must not be empty")}
			}
			if installTarget == "all" || installTarget == "none" {
				return usageError{err: fmt.Errorf("hook target %q is not valid here; omit --target to install all hooks", installTarget)}
			}
			targets, err := parseHookTargets(installTarget)
			if err != nil {
				return usageError{err: err}
			}
			home := deps.Home()
			if home == "" {
				return fmt.Errorf("hooks: no user home directory available")
			}
			results := installManagedHooks(home, targets, cmd.ErrOrStderr())
			for _, result := range results {
				state := "unchanged"
				if result.Err != nil {
					state = "failed"
				} else if result.Changed {
					state = "installed"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s: %s\n", result.Target, state, result.Path)
			}
			if err := hookInstallError(results); err != nil {
				return fmt.Errorf("hooks: %w", err)
			}
			if codexHookChanged(results) {
				_, _ = io.WriteString(cmd.OutOrStdout(), "restart Codex and approve `ai-agent-telemetry ingest --agent=codex` if prompted\n")
			}
			return nil
		},
	}
	install.Flags().StringVar(&installTarget, "target", "", "Install a comma-separated hook subset")
	registerHookTargetCompletion(install)

	var uninstallTarget string
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove owned global harness hooks",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("target") && uninstallTarget == "" {
				return usageError{err: fmt.Errorf("hook target value must not be empty")}
			}
			if uninstallTarget == "all" || uninstallTarget == "none" {
				return usageError{err: fmt.Errorf("hook target %q is not valid here; omit --target to uninstall all hooks", uninstallTarget)}
			}
			targets, err := parseHookTargets(uninstallTarget)
			if err != nil {
				return usageError{err: err}
			}
			home := deps.Home()
			if home == "" {
				return fmt.Errorf("hooks: no user home directory available")
			}
			results := uninstallHooks(home, targets, cmd.ErrOrStderr())
			for _, result := range results {
				state := "unchanged"
				if result.Err != nil {
					state = "failed"
				} else if result.Changed {
					state = "removed"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s: %s\n", result.Target, state, result.Path)
			}
			if err := hookInstallError(results); err != nil {
				return fmt.Errorf("hooks: %w", err)
			}
			return nil
		},
	}
	uninstall.Flags().StringVar(&uninstallTarget, "target", "", "Remove a comma-separated hook subset")
	registerHookTargetCompletion(uninstall)
	parent.AddCommand(install, uninstall)
	return parent
}

func registerHookTargetCompletion(command *cobra.Command) {
	_ = command.RegisterFlagCompletionFunc("target", func(_ *cobra.Command, _ []string, value string) ([]string, cobra.ShellCompDirective) {
		return completeCSV([]string{string(hookClaude), string(hookCodex), string(hookCursor)}, value)
	})
}

func newStatusCommand(_ appDeps) *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show configuration, delivery backlog, and global hook status",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			outbox, err := DefaultOutbox()
			if err != nil {
				return fmt.Errorf("outbox: %w", err)
			}
			settings := resolveDeliverySettings()
			_, _ = io.WriteString(cmd.OutOrStdout(), formatStatus(
				gatherStatus(outbox, pkgConfigDir(), resolveEndpoint(""), resolveTelemetryPolicy(), settings), verbose,
			))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show native paths and detailed errors")
	return cmd
}

func newSelftestCommand(_ appDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "selftest",
		Short: "Send a probe and confirm collector delivery",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			outbox, err := DefaultOutbox()
			if err != nil {
				return fmt.Errorf("outbox: %w", err)
			}
			tlsConfig, caErr := caTLSConfig(pkgConfigDir())
			if caErr != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ca:", caErr)
			}
			result, err := runSelftest(outbox, resolveEndpoint(""), resolveToken(), tlsConfig, selftestTimeout)
			if err != nil {
				return fmt.Errorf("selftest: failed: %w", err)
			}
			if !result.Delivered {
				return fmt.Errorf("selftest: probe not confirmed (try again)")
			}
			_, _ = io.WriteString(cmd.OutOrStdout(), "selftest: ok: probe accepted by the collector and cleared from the outbox\n")
			return nil
		},
	}
}

func newIngestCommand(_ appDeps) *cobra.Command {
	return &cobra.Command{
		Use:                "ingest",
		Short:              "Read a harness hook payload and queue events",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
				return cmd.Help()
			}
			agent, endpoint, err := parseIngestFlags(args)
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "ingest:", err)
				return nil
			}
			outbox, err := DefaultOutbox()
			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "outbox:", err)
				return nil
			}
			raw, _ := io.ReadAll(cmd.InOrStdin())
			returnErrorCode := ingest(outbox, agent, resolveEndpoint(endpoint), raw, gitRemote, resolveDeliverySettings())
			if returnErrorCode != 0 {
				return fmt.Errorf("ingest exited with code %d", returnErrorCode)
			}
			return nil
		},
	}
}

func newFlushCommand(_ appDeps) *cobra.Command {
	var endpoint string
	cmd := &cobra.Command{
		Use:   "flush",
		Short: "Send queued events to the collector",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			outbox, err := DefaultOutbox()
			if err != nil {
				return fmt.Errorf("outbox: %w", err)
			}
			sent, err := flushExplicit(outbox, deliveryResolver{
				Endpoint: func() (string, error) { return resolveEndpoint(endpoint), nil },
				TLS:      func() (*tls.Config, error) { return caTLSConfig(pkgConfigDir()) },
				Token:    resolveToken,
				Timeout:  func() time.Duration { return resolveDeliverySettings().FlushTimeout },
			})
			if err != nil {
				return fmt.Errorf("flush: %w", err)
			}
			if sent == 0 {
				_, _ = io.WriteString(cmd.OutOrStdout(), "nothing to flush\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Override the collector endpoint")
	return cmd
}

func newUpdateCheckCommand(_ appDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "update-check",
		Short: "Check whether a newer GitHub release is available",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = io.WriteString(cmd.OutOrStdout(), formatUpdateCheck(gatherUpdateCheck(version, func() (string, error) {
				return latestReleaseTag(updateCheckTimeout)
			})))
			return nil
		},
	}
}

func newSelfUpdateCommand(_ appDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "self-update",
		Short: "Download, verify, and install the latest GitHub release",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := runSelfUpdate(version, func(value string) {
				_, _ = io.WriteString(cmd.OutOrStdout(), value)
			}); err != nil {
				return fmt.Errorf("self-update: %w", err)
			}
			return nil
		},
	}
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), version)
			return nil
		},
	}
}

func newCompletionCommand() *cobra.Command {
	parent := &cobra.Command{
		Use:   "completion",
		Short: "Generate shell completion scripts",
		Args:  usageArgs(cobra.NoArgs),
		RunE:  helpRunE,
	}
	generators := []struct {
		name string
		run  func(*cobra.Command, io.Writer) error
	}{
		{name: "bash", run: func(root *cobra.Command, out io.Writer) error { return root.GenBashCompletion(out) }},
		{name: "zsh", run: func(root *cobra.Command, out io.Writer) error { return root.GenZshCompletion(out) }},
		{name: "fish", run: func(root *cobra.Command, out io.Writer) error { return root.GenFishCompletion(out, true) }},
		{name: "powershell", run: func(root *cobra.Command, out io.Writer) error { return root.GenPowerShellCompletion(out) }},
	}
	for _, generator := range generators {
		generator := generator
		parent.AddCommand(&cobra.Command{
			Use:   generator.name,
			Short: "Generate completion for " + generator.name,
			Args:  usageArgs(cobra.NoArgs),
			RunE: func(cmd *cobra.Command, _ []string) error {
				return generator.run(cmd.Root(), cmd.OutOrStdout())
			},
		})
	}
	return parent
}
