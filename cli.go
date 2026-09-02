package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

type appDeps struct {
	In           io.Reader
	Out          io.Writer
	ErrOut       io.Writer
	Home         func() string
	UpdateRunner func(context.Context, updateRunnerOptions) int
	CleanupImage func(context.Context, cleanupImageOptions) int
	Lifecycle    lifecycleDeps
	Update       updateHandoff
}

type usageError struct{ err error }

type silentExitStatus struct{ code int }

func (e silentExitStatus) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func defaultAppDeps() appDeps {
	return appDeps{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
		Home:   userHomeDir,
	}
}

func execute(args []string, deps appDeps) int {
	deps = normalizeAppDeps(deps)
	if handled, code := routeInternalUpdateMode(context.Background(), args, deps); handled {
		return code
	}
	if err := legacyArgumentError(args); err != nil {
		_, _ = fmt.Fprintln(deps.ErrOut, err)
		return 2
	}
	root := newRootCommand(deps)
	root.SetArgs(args)
	_, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	var status silentExitStatus
	if errors.As(err, &status) {
		return status.code
	}
	_, _ = fmt.Fprintln(root.ErrOrStderr(), err)
	if isUsageError(err) {
		return 2
	}
	return 1
}

func newRootCommand(deps appDeps) *cobra.Command {
	deps = normalizeAppDeps(deps)
	root := &cobra.Command{
		Use:           "ai-agent-telemetry",
		Short:         "Collect skill-use telemetry from AI coding agents",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          usageArgs(cobra.NoArgs),
		RunE:          helpRunE,
	}
	root.SetIn(deps.In)
	root.SetOut(deps.Out)
	root.SetErr(deps.ErrOut)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageError{err: err}
	})
	root.AddCommand(
		newLifecycleCommand(actionInstall, deps),
		newLifecycleCommand(actionUpdate, deps),
		newLifecycleCommand(actionUninstall, deps),
		newLegacyMigrationCommand("update-check", "update"),
		newLegacyMigrationCommand("self-update", "update"),
		newConfigureCommand(deps),
		newHooksCommand(deps),
		newStatusCommand(deps),
		newSelftestCommand(deps),
		newIngestCommand(deps),
		newFlushCommand(deps),
		newVersionCommand(),
		newCompletionCommand(),
	)
	root.SetHelpCommand(newHelpCommand())
	return root
}

func legacyArgumentError(args []string) error {
	for _, arg := range args {
		name := arg
		if index := strings.IndexByte(name, '='); index >= 0 {
			name = name[:index]
		}
		switch name {
		case "--components":
			return fmt.Errorf("%s is no longer supported; use the v1.2.0 bootstrap with uninstall --components apm,git-hooks for legacy cleanup", name)
		case "--skip":
			return fmt.Errorf("%s is no longer supported; telemetry is required; use --harnesses to select native hooks", name)
		case "--force-git-hooks":
			return fmt.Errorf("%s is no longer supported; native harness hooks are refreshed automatically", name)
		case "--cli-only":
			return fmt.Errorf("%s is no longer supported; update always refreshes telemetry", name)
		case "--remove-cli":
			return fmt.Errorf("%s is no longer supported; uninstall always removes the managed CLI", name)
		case "--force-update", "-ForceUpdate":
			return fmt.Errorf("%s is no longer supported; use update", name)
		case "--force", "-Force":
			return fmt.Errorf("%s is no longer supported; use update", name)
		case "--skip-config", "-SkipConfig":
			return fmt.Errorf("%s is no longer supported", name)
		}
	}
	return nil
}

func newHelpCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "help [command...]",
		Short: "Help about any command",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Root().Help()
			}
			topic, remaining, err := cmd.Root().Find(args)
			if err != nil || len(remaining) != 0 || topic == cmd.Root() {
				return usageError{err: fmt.Errorf("unknown help topic %q", strings.Join(args, " "))}
			}
			return topic.Help()
		},
	}
}

func normalizeAppDeps(deps appDeps) appDeps {
	if deps.In == nil {
		deps.In = strings.NewReader("")
	}
	if deps.Out == nil {
		deps.Out = io.Discard
	}
	if deps.ErrOut == nil {
		deps.ErrOut = io.Discard
	}
	if deps.Home == nil {
		deps.Home = userHomeDir
	}
	if deps.Lifecycle.ManagedCLI.Install == nil && deps.Lifecycle.ManagedCLI.Remove == nil &&
		deps.Lifecycle.Telemetry.Install == nil && deps.Lifecycle.Telemetry.Update == nil &&
		deps.Lifecycle.Telemetry.Uninstall == nil {
		deps.Lifecycle = defaultLifecycleDeps(deps.Home(), deps.ErrOut)
	}
	if deps.Lifecycle.Progress == nil {
		deps.Lifecycle.Progress = deps.ErrOut
	}
	if deps.Update.Prepare == nil {
		executable, _ := os.Executable()
		managedPath := managedCLIPath(deps.Home(), runtime.GOOS)
		deps.Update = newUpdateHandoff(updateHandoffDeps{
			ManagedPath: managedPath,
			Bootstrap:   !sameExecutablePath(executable, managedPath, runtime.GOOS),
			Stdin:       deps.In, Stdout: deps.Out, Stderr: deps.ErrOut,
		})
	}
	if deps.UpdateRunner == nil {
		deps.UpdateRunner = func(ctx context.Context, options updateRunnerOptions) int {
			var prepared lifecycleOptions
			runnerLifecycle := deps.Lifecycle
			runnerLifecycle.ManagedInstallSource = func() (string, error) { return options.ManagedPath, nil }
			return runPreparedUpdateRunner(ctx, options, updateRunnerCallbacks{
				Preflight: func(ctx context.Context, args []string) error {
					parsed, err := parseLifecycleArgs(actionUpdate, args)
					if err != nil {
						return err
					}
					prepared = parsed
					return preflightLifecycle(ctx, prepared, runnerLifecycle)
				},
				Lifecycle: func(ctx context.Context, _ []string) int {
					summary := executePreparedLifecycle(ctx, prepared, runnerLifecycle)
					_, _ = io.WriteString(deps.Out, formatLifecycleSummary(summary))
					if summary.Err != nil {
						_, _ = fmt.Fprintln(deps.ErrOut, summary.Err)
						return 1
					}
					return 0
				},
				Stdout: deps.Out, Stderr: deps.ErrOut,
			})
		}
	}
	if deps.CleanupImage == nil {
		deps.CleanupImage = func(ctx context.Context, options cleanupImageOptions) int {
			return cleanupUpdateImage(ctx, options, deps.ErrOut)
		}
	}
	return deps
}

func helpRunE(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

func newLegacyMigrationCommand(name, replacement string) *cobra.Command {
	return &cobra.Command{
		Use:    name,
		Hidden: true,
		Args:   usageArgs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			return usageError{err: fmt.Errorf("%s is no longer supported; use %s", name, replacement)}
		},
	}
}

func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			err = withCommandSuggestions(cmd, args, err)
			return usageError{err: err}
		}
		return nil
	}
}

func withCommandSuggestions(cmd *cobra.Command, args []string, err error) error {
	if len(args) == 0 || !cmd.HasAvailableSubCommands() {
		return err
	}
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	suggestions := cmd.SuggestionsFor(args[0])
	if len(suggestions) == 0 {
		return err
	}
	return fmt.Errorf("%w\n\nDid you mean this?\n\t%s", err, strings.Join(suggestions, "\n\t"))
}

func isUsageError(err error) bool {
	var target usageError
	if errors.As(err, &target) {
		return true
	}
	message := err.Error()
	return strings.HasPrefix(message, "unknown command ") ||
		strings.HasPrefix(message, "unknown shorthand flag:") ||
		strings.HasPrefix(message, "unknown flag:")
}
