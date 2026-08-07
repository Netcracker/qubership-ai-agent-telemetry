//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClineHookOperationsRejectFIFOWithoutReadingIt(t *testing.T) {
	tests := []struct {
		name string
		run  func(string) string
		want string
	}{
		{
			name: "install",
			want: "not a regular file",
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
			want: "not a regular file",
			run: func(home string) string {
				state, detail := inspectClineHook(clineHookPath(home, "darwin"), "darwin")
				if state != hookInvalid {
					return "status did not report invalid"
				}
				return detail
			},
		},
		{
			name: "uninstall",
			want: "preserved user-owned Cline hook",
			run: func(home string) string {
				var warnings bytes.Buffer
				changed, err := removeClineHook(clineHookPath(home, "darwin"), "darwin", &warnings)
				if err != nil {
					return err.Error()
				}
				if changed {
					return "uninstall changed FIFO"
				}
				return warnings.String()
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
				if !strings.Contains(detail, tt.want) {
					t.Fatalf("detail = %q, want %q without reading FIFO", detail, tt.want)
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
