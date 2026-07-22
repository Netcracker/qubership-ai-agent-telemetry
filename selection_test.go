package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestNormalizeLifecycleSelection(t *testing.T) {
	tests := []struct {
		name     string
		selected []string
		skipped  []string
		want     []componentName
		wantErr  string
	}{
		{name: "default", want: []componentName{componentAPM, componentTelemetry, componentGitHooks}},
		{name: "normalizes case and duplicates", selected: []string{"TELEMETRY", "apm", "Telemetry"}, want: []componentName{componentAPM, componentTelemetry}},
		{name: "all", selected: []string{"ALL"}, want: []componentName{componentAPM, componentTelemetry, componentGitHooks}},
		{name: "skip", skipped: []string{"TELEMETRY"}, want: []componentName{componentAPM, componentGitHooks}},
		{name: "select then skip", selected: []string{"apm", "telemetry"}, skipped: []string{"APM"}, want: []componentName{componentTelemetry}},
		{name: "invalid selection", selected: []string{"database"}, wantErr: `unknown component "database"`},
		{name: "invalid skip", skipped: []string{"database"}, wantErr: `unknown component "database"`},
		{name: "all mixed with name", selected: []string{"all", "apm"}, wantErr: `component "all" must be used alone`},
		{name: "empty final selection", skipped: []string{"all"}, wantErr: "component selection must not be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeSelection(tt.selected, tt.skipped)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("normalizeSelection() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalizeSelection() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeLifecycleHarnesses(t *testing.T) {
	tests := []struct {
		name    string
		values  []string
		want    []hookTarget
		wantErr string
	}{
		{name: "default", want: []hookTarget{hookClaude, hookCodex, hookCursor}},
		{name: "normalizes and sorts", values: []string{"CURSOR", "claude", "Cursor"}, want: []hookTarget{hookClaude, hookCursor}},
		{name: "all", values: []string{"All"}, want: []hookTarget{hookClaude, hookCodex, hookCursor}},
		{name: "invalid", values: []string{"windsurf"}, wantErr: `unknown harness "windsurf"`},
		{name: "all mixed", values: []string{"all", "codex"}, wantErr: `harness "all" must be used alone`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeHarnesses(tt.values)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("normalizeHarnesses() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalizeHarnesses() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeLifecycleOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    lifecycleOptions
		want    lifecycleOptions
		wantErr string
	}{
		{
			name: "install defaults",
			opts: lifecycleOptions{Action: actionInstall},
			want: lifecycleOptions{Action: actionInstall, Components: allComponents(), Harnesses: append([]hookTarget(nil), allHookTargets...)},
		},
		{
			name: "normalizes typed values",
			opts: lifecycleOptions{Action: actionUpdate, Components: []componentName{"TELEMETRY", "apm", "telemetry"}, Harnesses: []hookTarget{"CURSOR", "claude"}},
			want: lifecycleOptions{Action: actionUpdate, Components: []componentName{componentAPM, componentTelemetry}, Harnesses: []hookTarget{hookClaude, hookCursor}},
		},
		{name: "CLI only", opts: lifecycleOptions{Action: actionUpdate, CLIOnly: true}, want: lifecycleOptions{Action: actionUpdate, CLIOnly: true}},
		{name: "CLI only action", opts: lifecycleOptions{Action: actionInstall, CLIOnly: true}, wantErr: "--cli-only is valid only for update"},
		{name: "CLI only components", opts: lifecycleOptions{Action: actionUpdate, CLIOnly: true, Components: []componentName{componentAPM}}, wantErr: "--cli-only cannot be combined with component options"},
		{name: "CLI only harnesses", opts: lifecycleOptions{Action: actionUpdate, CLIOnly: true, Harnesses: []hookTarget{hookCodex}}, wantErr: "--cli-only cannot be combined with harness options"},
		{name: "CLI only force", opts: lifecycleOptions{Action: actionUpdate, CLIOnly: true, ForceGitHooks: true}, wantErr: "--cli-only cannot be combined with --force-git-hooks"},
		{name: "CLI only noninteractive", opts: lifecycleOptions{Action: actionUpdate, CLIOnly: true, NonInteractive: true}, wantErr: "--cli-only cannot be combined with --non-interactive"},
		{name: "purge without telemetry", opts: lifecycleOptions{Action: actionUninstall, Components: []componentName{componentAPM}, Purge: true}, wantErr: "--purge requires telemetry"},
		{name: "remove CLI without telemetry", opts: lifecycleOptions{Action: actionUninstall, Components: []componentName{componentGitHooks}, RemoveCLI: true}, wantErr: "--remove-cli requires telemetry"},
		{name: "purge install", opts: lifecycleOptions{Action: actionInstall, Purge: true}, wantErr: "--purge is valid only for uninstall"},
		{name: "remove CLI update", opts: lifecycleOptions{Action: actionUpdate, RemoveCLI: true}, wantErr: "--remove-cli is valid only for uninstall"},
		{name: "uninstall harnesses", opts: lifecycleOptions{Action: actionUninstall, Harnesses: []hookTarget{hookCodex}}, wantErr: "harness selection is not valid for uninstall"},
		{name: "unknown action", opts: lifecycleOptions{Action: "repair"}, wantErr: `unknown lifecycle action "repair"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeLifecycleOptions(tt.opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("normalizeLifecycleOptions() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalizeLifecycleOptions() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCompleteSelectionDetectsOnlyTheFullSet(t *testing.T) {
	if !isCompleteSelection([]componentName{componentGitHooks, componentAPM, componentTelemetry, componentAPM}) {
		t.Fatal("explicit complete set was not detected")
	}
	for _, components := range [][]componentName{
		nil,
		{componentAPM},
		{componentAPM, componentTelemetry},
		{componentAPM, componentTelemetry, "database"},
	} {
		if isCompleteSelection(components) {
			t.Fatalf("proper or invalid subset %v detected as complete", components)
		}
	}
}

func TestCompleteSelectionCSV(t *testing.T) {
	allowed := []string{"all", "apm", "telemetry", "git-hooks"}
	directiveWithSpace := cobra.ShellCompDirectiveNoFileComp
	directiveWithoutSpace := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	tests := []struct {
		name          string
		input         string
		want          []string
		wantDirective cobra.ShellCompDirective
	}{
		{name: "initial", want: allowed, wantDirective: directiveWithoutSpace},
		{name: "partial", input: "te", want: []string{"telemetry"}, wantDirective: directiveWithoutSpace},
		{name: "prefix and duplicate suppression", input: "apm,", want: []string{"apm,telemetry", "apm,git-hooks"}, wantDirective: directiveWithoutSpace},
		{name: "typed prefix retained", input: "apm,te", want: []string{"apm,telemetry"}, wantDirective: directiveWithoutSpace},
		{name: "all only first", input: "apm,a", want: []string{}, wantDirective: directiveWithSpace},
		{name: "all terminates", input: "all,", want: []string{}, wantDirective: directiveWithSpace},
		{name: "duplicate omitted", input: "apm,telemetry,ap", want: []string{}, wantDirective: directiveWithSpace},
		{name: "last value permits space", input: "apm,telemetry,", want: []string{"apm,telemetry,git-hooks"}, wantDirective: directiveWithSpace},
		{name: "invalid prefix", input: "database,", want: []string{}, wantDirective: directiveWithSpace},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, directive := completeCSV(allowed, tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("completeCSV(%q) = %v, want %v", tt.input, got, tt.want)
			}
			if directive != tt.wantDirective {
				t.Fatalf("completeCSV(%q) directive = %d, want %d", tt.input, directive, tt.wantDirective)
			}
		})
	}
}
