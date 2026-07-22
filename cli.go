package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
}

type usageError struct{ err error }

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
	root := newRootCommand(deps)
	root.SetArgs(args)
	_, err := root.ExecuteC()
	if err == nil {
		return 0
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
	if deps.UpdateRunner == nil {
		deps.UpdateRunner = func(_ context.Context, _ updateRunnerOptions) int {
			_, _ = fmt.Fprintln(deps.ErrOut, "internal update runner is not wired")
			return 1
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

func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return usageError{err: err}
		}
		return nil
	}
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
