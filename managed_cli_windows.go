//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type windowsUserPATHRegistry struct{}

func (windowsUserPATHRegistry) Read() (string, uint32, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return "", 0, err
	}
	defer key.Close()
	value, valueType, err := key.GetStringValue("PATH")
	if errors.Is(err, registry.ErrNotExist) {
		return "", registryString, nil
	}
	return value, valueType, err
}

func (windowsUserPATHRegistry) Write(value string, valueType uint32) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if valueType == registryExpandString {
		return key.SetExpandStringValue("PATH", value)
	}
	return key.SetStringValue("PATH", value)
}

func platformManagedCLIConfig(home string) managedCLIConfig {
	entry := filepath.Join(home, ".local", "bin")
	return managedCLIConfig{
		Home: home, GOOS: "windows",
		Paths:      newWindowsPathManager(entry, windowsUserPATHRegistry{}, notifyWindowsEnvironmentChange),
		Executable: os.Executable,
	}
}

const (
	windowsHWNDBroadcast      = 0xffff
	windowsWMSettingChange    = 0x001a
	windowsSMTOAbortIfHung    = 0x0002
	windowsNotifyTimeoutMilli = 5000
)

var (
	user32DLL           = windows.NewLazySystemDLL("user32.dll")
	sendMessageTimeoutW = user32DLL.NewProc("SendMessageTimeoutW")
)

func notifyWindowsEnvironmentChange() error {
	environment, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return err
	}
	result, _, callErr := sendMessageTimeoutW.Call(
		windowsHWNDBroadcast,
		windowsWMSettingChange,
		0,
		uintptr(unsafe.Pointer(environment)),
		windowsSMTOAbortIfHung,
		windowsNotifyTimeoutMilli,
		0,
	)
	if result == 0 {
		if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
			return fmt.Errorf("send WM_SETTINGCHANGE: %w", callErr)
		}
		return errors.New("send WM_SETTINGCHANGE returned zero")
	}
	return nil
}

func runPlatformUpdateRunner(
	source string,
	options updateRunnerOptions,
	lifecycle func() int,
	stdout, stderr io.Writer,
) (int, error) {
	return runWindowsUpdateSwap(source, options.ManagedPath, options.ParentPID, randHex(), func(path string, args []string) error {
		return newWindowsUpdateCleanupCommand(path, args, stdout, stderr).Start()
	}, lifecycle, stderr)
}

func newWindowsUpdateCleanupCommand(executable string, args []string, stdout, stderr io.Writer) *exec.Cmd {
	command := exec.Command(executable, args...)
	command.Stdout = stdout
	command.Stderr = stderr
	return command
}

func waitForUpdateParent(ctx context.Context, pid int) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)
	for {
		status, err := windows.WaitForSingleObject(handle, 50)
		if err != nil {
			return fmt.Errorf("wait for process %d: %w", pid, err)
		}
		switch status {
		case windows.WAIT_OBJECT_0:
			return nil
		case uint32(windows.WAIT_TIMEOUT):
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		default:
			return fmt.Errorf("wait for process %d returned status %d", pid, status)
		}
	}
}
