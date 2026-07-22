//go:build windows

package main

import "testing"

func TestManagedCLIWindowsRegistryTypesMatchAdapterContract(t *testing.T) {
	if registryString != 1 || registryExpandString != 2 {
		t.Fatalf("registry types = %d, %d; want REG_SZ=1 and REG_EXPAND_SZ=2", registryString, registryExpandString)
	}
}
