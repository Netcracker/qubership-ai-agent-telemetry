package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseHookTargets(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []hookTarget
		wantErr bool
	}{
		{name: "default", want: allHookTargets},
		{name: "all", raw: "all", want: allHookTargets},
		{name: "none", raw: "none", want: []hookTarget{}},
		{name: "subset", raw: "codex,claude", want: []hookTarget{hookClaude, hookCodex}},
		{name: "deduplicate", raw: "cursor,cursor", want: []hookTarget{hookCursor}},
		{name: "whitespace", raw: " cursor, claude ", want: []hookTarget{hookClaude, hookCursor}},
		{name: "unknown", raw: "windsurf", wantErr: true},
		{name: "empty member", raw: "claude,,codex", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHookTargets(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("targets = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseHookTargetsNamesInvalidValue(t *testing.T) {
	_, err := parseHookTargets("claude, windsurf ")
	if err == nil || !strings.Contains(err.Error(), " windsurf ") {
		t.Fatalf("error = %v, want invalid value", err)
	}
}

func TestParseHooksCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []hookTarget
		wantErr bool
	}{
		{name: "install all", args: []string{"install"}, want: allHookTargets},
		{name: "install subset", args: []string{"install", "--target=cursor,claude"}, want: []hookTarget{hookClaude, hookCursor}},
		{name: "missing action", wantErr: true},
		{name: "unknown action", args: []string{"remove"}, wantErr: true},
		{name: "unknown flag", args: []string{"install", "--bogus"}, wantErr: true},
		{name: "unknown target", args: []string{"install", "--target=windsurf"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHooksCommand(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("targets = %v, want %v", got, tt.want)
			}
		})
	}
}
