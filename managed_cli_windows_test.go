//go:build windows

package main

import (
	"bytes"
	"reflect"
	"testing"
)

func TestManagedCLIWindowsRegistryTypesMatchAdapterContract(t *testing.T) {
	if registryString != 1 || registryExpandString != 2 {
		t.Fatalf("registry types = %d, %d; want REG_SZ=1 and REG_EXPAND_SZ=2", registryString, registryExpandString)
	}
}

func TestWindowsUpdateSwapCleanupCommandUsesExactArgumentsAndStreams(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	args := []string{"__cleanup-update-image", "--path", `C:\bin\exact-old.exe`, "--wait-pid", "42"}
	command := newWindowsUpdateCleanupCommand(`C:\bin\ai-agent-telemetry.exe`, args, stdout, stderr)
	if command.Path != `C:\bin\ai-agent-telemetry.exe` || !reflect.DeepEqual(command.Args[1:], args) {
		t.Fatalf("cleanup command = %q, want exact executable and arguments", command.Args)
	}
	if command.Stdout != stdout || command.Stderr != stderr {
		t.Fatalf("cleanup streams = %p, %p; want %p, %p", command.Stdout, command.Stderr, stdout, stderr)
	}
}
