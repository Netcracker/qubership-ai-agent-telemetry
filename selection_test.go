package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestNormalizeLifecycleHarnesses(t *testing.T) {
	tests := []struct {
		name    string
		values  []string
		want    []hookTarget
		wantErr string
	}{
		{name: "default", want: []hookTarget{hookClaude, hookCline, hookCodex, hookCursor}},
		{name: "normalizes and sorts", values: []string{"CURSOR", "cline", "Cline"}, want: []hookTarget{hookCline, hookCursor}},
		{name: "all", values: []string{"All"}, want: []hookTarget{hookClaude, hookCline, hookCodex, hookCursor}},
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

func TestKnownEnumValuesUseOrderedMembership(t *testing.T) {
	for _, target := range allHookTargets {
		if !knownHookTarget(target) {
			t.Fatalf("knownHookTarget(%q) = false", target)
		}
	}
	if knownHookTarget("unknown") {
		t.Fatal("knownHookTarget accepted an unknown value")
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
			want: lifecycleOptions{Action: actionInstall, Harnesses: append([]hookTarget(nil), allHookTargets...)},
		},
		{
			name: "normalizes typed values",
			opts: lifecycleOptions{Action: actionUpdate, Harnesses: []hookTarget{"CURSOR", "claude"}},
			want: lifecycleOptions{Action: actionUpdate, Harnesses: []hookTarget{hookClaude, hookCursor}},
		},
		{name: "uninstall purge", opts: lifecycleOptions{Action: actionUninstall, Purge: true}, want: lifecycleOptions{Action: actionUninstall, Purge: true}},
		{name: "purge install", opts: lifecycleOptions{Action: actionInstall, Purge: true}, wantErr: "--purge is valid only for uninstall"},
		{name: "uninstall harnesses", opts: lifecycleOptions{Action: actionUninstall, Harnesses: []hookTarget{hookCodex}}, wantErr: "harness selection is not valid for uninstall"},
		{name: "uninstall noninteractive", opts: lifecycleOptions{Action: actionUninstall, NonInteractive: true}, wantErr: "--non-interactive is not valid for uninstall"},
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

func TestCompleteSelectionCSV(t *testing.T) {
	allowed := []string{"all", "claude", "codex", "cursor"}
	directiveWithSpace := cobra.ShellCompDirectiveNoFileComp
	directiveWithoutSpace := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	tests := []struct {
		name          string
		input         string
		want          []string
		wantDirective cobra.ShellCompDirective
	}{
		{name: "initial", want: allowed, wantDirective: directiveWithoutSpace},
		{name: "partial", input: "co", want: []string{"codex"}, wantDirective: directiveWithoutSpace},
		{name: "prefix and duplicate suppression", input: "claude,", want: []string{"claude,codex", "claude,cursor"}, wantDirective: directiveWithoutSpace},
		{name: "typed prefix retained", input: "claude,co", want: []string{"claude,codex"}, wantDirective: directiveWithoutSpace},
		{name: "all only first", input: "claude,a", want: []string{}, wantDirective: directiveWithSpace},
		{name: "all terminates", input: "all,", want: []string{}, wantDirective: directiveWithSpace},
		{name: "duplicate omitted", input: "claude,codex,cl", want: []string{}, wantDirective: directiveWithSpace},
		{name: "last value permits space", input: "claude,codex,", want: []string{"claude,codex,cursor"}, wantDirective: directiveWithSpace},
		{name: "invalid prefix", input: "windsurf,", want: []string{}, wantDirective: directiveWithSpace},
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

func TestCompleteHookSelectionTreatsNoneAsExclusive(t *testing.T) {
	wantDirective := cobra.ShellCompDirectiveNoFileComp
	for _, tt := range []struct {
		input string
		want  []string
	}{
		{input: "none", want: []string{"none"}},
		{input: "none,", want: []string{}},
	} {
		got, directive := completeCSV(hookFlagValues(true), tt.input)
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("completeCSV(%q) = %v, want %v", tt.input, got, tt.want)
		}
		if directive != wantDirective {
			t.Fatalf("completeCSV(%q) directive = %d, want %d", tt.input, directive, wantDirective)
		}
	}
}
