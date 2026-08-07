//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClineHookOperationsRejectFIFOWithoutOpeningIt(t *testing.T) {
	tests := []struct {
		name string
		run  func(string) string
	}{
		{
			name: "install",
			run: func(home string) string {
				_, _, err := installClineHook(home, "darwin")
				if err == nil {
					return "install returned no error"
				}
				return err.Error()
			},
		},
		{
			name: "status",
			run: func(home string) string {
				state, detail := inspectClineHook(clineHookPath(home, "darwin"), "darwin")
				if state != hookInvalid {
					return "status did not report invalid"
				}
				return detail
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			path := clineHookPath(home, "darwin")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}

			result := make(chan string, 1)
			go func() { result <- tt.run(home) }()

			select {
			case detail := <-result:
				if !strings.Contains(detail, "not a regular file") {
					t.Fatalf("detail = %q, want non-regular conflict without opening FIFO", detail)
				}
			case <-time.After(250 * time.Millisecond):
				fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
				if err == nil {
					_ = syscall.Close(fd)
				}
				<-result
				t.Fatal("operation blocked while opening a FIFO")
			}
		})
	}
}
