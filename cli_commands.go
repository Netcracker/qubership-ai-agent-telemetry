package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type lifecycleFlagValues struct {
	components, skip, harnesses                              []string
	forceGitHooks, nonInteractive, purge, removeCLI, cliOnly bool
	repoScopeChange                                          string
	legacyForceUpdate, legacyForce, legacySkipConfig         bool
}

func newLifecycleCommand(action lifecycleAction, deps appDeps) *cobra.Command {
	var values lifecycleFlagValues
	cmd := &cobra.Command{
		Use:   string(action),
		Short: lifecycleCommandDescription(action),
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := legacyLifecycleOptionError(cmd, values); err != nil {
				return usageError{err: err}
			}
			opts, err := lifecycleOptionsFromFlags(action, cmd, values)
			if err != nil {
				return usageError{err: err}
			}
			var lifecycleErr error
			run := func(ctx context.Context, _ []string) int {
				summary := runLifecycle(ctx, opts, deps.Lifecycle)
				_, _ = io.WriteString(cmd.OutOrStdout(), formatLifecycleSummary(summary))
				lifecycleErr = summary.Err
				if summary.Err != nil {
					return 1
				}
				return 0
			}
			if action == actionUpdate {
				if err := preflightManagedCLI(opts, deps.Lifecycle.ManagedCLI); err != nil {
					return err
				}
				code, err := runUpdateWithHandoff(cmd.Context(), lifecycleArgs(opts), deps.Update, run)
				if err != nil {
					return err
				}
				if lifecycleErr != nil {
					return lifecycleErr
				}
				if code != 0 {
					return silentExitStatus{code: code}
				}
				return nil
			}
			run(cmd.Context(), nil)
			return lifecycleErr
		},
	}
	bindLifecycleFlags(cmd, action, &values)
	registerLifecycleCompletion(cmd, action)
	return cmd
}

func lifecycleCommandDescription(action lifecycleAction) string {
	switch action {
	case actionInstall:
		return "Install the managed CLI and selected components"
	case actionUpdate:
		return "Update the managed CLI and selected components"
	default:
		return "Remove selected components and the managed CLI when appropriate"
	}
}

func bindLifecycleFlags(cmd *cobra.Command, action lifecycleAction, values *lifecycleFlagValues) {
	cmd.Flags().StringSliceVar(&values.components, "components", nil,
		"Select a comma-separated component subset: "+enumValuesDescription(componentFlagValues(true)))
	cmd.Flags().StringSliceVar(&values.skip, "skip", nil,
		"Skip a comma-separated component subset: "+enumValuesDescription(componentFlagValues(false)))
	if action != actionUninstall {
		cmd.Flags().StringSliceVar(&values.harnesses, "harnesses", nil,
			"Select a comma-separated harness subset: "+enumValuesDescription(harnessFlagValues(true)))
		cmd.Flags().BoolVar(&values.forceGitHooks, "force-git-hooks", false, "Replace an unrelated global Git hooks path")
		cmd.Flags().BoolVar(&values.nonInteractive, "non-interactive", false, "Disable interactive prompts")
	}
	if action == actionUpdate {
		cmd.Flags().BoolVar(&values.cliOnly, "cli-only", false, "Update only the managed CLI")
		cmd.Flags().StringVar(&values.repoScopeChange, "repo-scope-change", "",
			"Handle the legacy repository scope: accept or keep; omit to be prompted")
	}
	if action == actionUninstall {
		cmd.Flags().BoolVar(&values.purge, "purge", false, "Remove telemetry configuration and cache")
		cmd.Flags().BoolVar(&values.removeCLI, "remove-cli", false, "Remove the managed CLI after a partial uninstall")
	}
	cmd.Flags().BoolVar(&values.legacyForceUpdate, "force-update", false, "")
	cmd.Flags().BoolVar(&values.legacyForce, "force", false, "")
	cmd.Flags().BoolVar(&values.legacySkipConfig, "skip-config", false, "")
	_ = cmd.Flags().MarkHidden("force-update")
	_ = cmd.Flags().MarkHidden("force")
	_ = cmd.Flags().MarkHidden("skip-config")
}

func lifecycleOptionsFromFlags(action lifecycleAction, cmd *cobra.Command, values lifecycleFlagValues) (lifecycleOptions, error) {
	opts := lifecycleOptions{Action: action, ForceGitHooks: values.forceGitHooks, NonInteractive: values.nonInteractive,
		Purge: values.purge, RemoveCLI: values.removeCLI, CLIOnly: values.cliOnly,
		RepoScopeChange: repoScopeChange(values.repoScopeChange)}
	if cmd.Flags().Changed("components") || cmd.Flags().Changed("skip") {
		components, err := normalizeSelection(values.components, values.skip)
		if err != nil {
			return lifecycleOptions{}, err
		}
		opts.Components = components
	}
	if action != actionUninstall && cmd.Flags().Changed("harnesses") {
		harnesses, err := normalizeHarnesses(values.harnesses)
		if err != nil {
			return lifecycleOptions{}, err
		}
		opts.Harnesses = harnesses
	}
	return normalizeLifecycleOptions(opts)
}

func parseLifecycleArgs(action lifecycleAction, args []string) (lifecycleOptions, error) {
	var values lifecycleFlagValues
	cmd := &cobra.Command{Use: string(action), SilenceErrors: true, SilenceUsage: true}
	bindLifecycleFlags(cmd, action, &values)
	cmd.SetArgs(args)
	if err := cmd.ParseFlags(args); err != nil {
		return lifecycleOptions{}, err
	}
	if len(cmd.Flags().Args()) != 0 {
		return lifecycleOptions{}, fmt.Errorf("%s accepts no arguments", action)
	}
	if err := legacyLifecycleOptionError(cmd, values); err != nil {
		return lifecycleOptions{}, err
	}
	return lifecycleOptionsFromFlags(action, cmd, values)
}

func legacyLifecycleOptionError(cmd *cobra.Command, values lifecycleFlagValues) error {
	switch {
	case cmd.Flags().Changed("force-update") || values.legacyForceUpdate:
		return errors.New("--force-update is no longer supported; use update")
	case cmd.Flags().Changed("force") || values.legacyForce:
		return errors.New("--force is no longer supported; use update --components telemetry")
	case cmd.Flags().Changed("skip-config") || values.legacySkipConfig:
		return errors.New("--skip-config is no longer supported; use --skip telemetry")
	default:
		return nil
	}
}

func lifecycleArgs(opts lifecycleOptions) []string {
	args := make([]string, 0, 10)
	if opts.CLIOnly {
		args = append(args, "--cli-only")
		if opts.RepoScopeChange != repoScopeChangeAsk {
			args = append(args, "--repo-scope-change", string(opts.RepoScopeChange))
		}
		return args
	}
	components := make([]string, len(opts.Components))
	for i, component := range opts.Components {
		components[i] = string(component)
	}
	args = append(args, "--components", strings.Join(components, ","))
	if opts.Action != actionUninstall {
		harnesses := make([]string, len(opts.Harnesses))
		for i, harness := range opts.Harnesses {
			harnesses[i] = string(harness)
		}
		args = append(args, "--harnesses", strings.Join(harnesses, ","))
	}
	if opts.ForceGitHooks {
		args = append(args, "--force-git-hooks")
	}
	if opts.NonInteractive {
		args = append(args, "--non-interactive")
	}
	if opts.RepoScopeChange != repoScopeChangeAsk {
		args = append(args, "--repo-scope-change", string(opts.RepoScopeChange))
	}
	if opts.Purge {
		args = append(args, "--purge")
	}
	if opts.RemoveCLI {
		args = append(args, "--remove-cli")
	}
	return args
}

func registerLifecycleCompletion(cmd *cobra.Command, action lifecycleAction) {
	components := componentFlagValues(true)
	_ = cmd.RegisterFlagCompletionFunc("components", func(_ *cobra.Command, _ []string, value string) ([]string, cobra.ShellCompDirective) {
		return completeCSV(components, value)
	})
	_ = cmd.RegisterFlagCompletionFunc("skip", func(_ *cobra.Command, _ []string, value string) ([]string, cobra.ShellCompDirective) {
		return completeCSV(components[1:], value)
	})
	if action != actionUninstall {
		_ = cmd.RegisterFlagCompletionFunc("harnesses", func(_ *cobra.Command, _ []string, value string) ([]string, cobra.ShellCompDirective) {
			return completeCSV(harnessFlagValues(true), value)
		})
	}
}

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
					return usageError{err: enumValueError(err, "hook targets", hookFlagValues(true))}
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
	cmd.Flags().StringVar(&hooks, "hooks", "", "Install hook targets: "+enumValuesDescription(hookFlagValues(true)))
	_ = cmd.RegisterFlagCompletionFunc("hooks", func(_ *cobra.Command, _ []string, value string) ([]string, cobra.ShellCompDirective) {
		return completeCSV(hookFlagValues(true), value)
	})
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
				return usageError{err: fmt.Errorf("hook target %q is not valid here; valid hook targets: %s; omit --target to install all hooks", installTarget, enumValuesDescription(hookFlagValues(false)))}
			}
			targets, err := parseHookTargets(installTarget)
			if err != nil {
				return usageError{err: enumValueError(err, "hook targets", hookFlagValues(false))}
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
	install.Flags().StringVar(&installTarget, "target", "", "Install hook targets: "+enumValuesDescription(hookFlagValues(false)))
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
				return usageError{err: fmt.Errorf("hook target %q is not valid here; valid hook targets: %s; omit --target to uninstall all hooks", uninstallTarget, enumValuesDescription(hookFlagValues(false)))}
			}
			targets, err := parseHookTargets(uninstallTarget)
			if err != nil {
				return usageError{err: enumValueError(err, "hook targets", hookFlagValues(false))}
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
	uninstall.Flags().StringVar(&uninstallTarget, "target", "", "Remove hook targets: "+enumValuesDescription(hookFlagValues(false)))
	registerHookTargetCompletion(uninstall)
	parent.AddCommand(install, uninstall)
	return parent
}

func registerHookTargetCompletion(command *cobra.Command) {
	_ = command.RegisterFlagCompletionFunc("target", func(_ *cobra.Command, _ []string, value string) ([]string, cobra.ShellCompDirective) {
		return completeCSV(hookFlagValues(false), value)
	})
}

func enumValueError(err error, name string, values []string) error {
	return fmt.Errorf("%w; valid %s: %s", err, name, enumValuesDescription(values))
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
