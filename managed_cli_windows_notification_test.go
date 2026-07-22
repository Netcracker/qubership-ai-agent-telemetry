package main

import (
	"errors"
	"strings"
	"testing"
)

func TestWindowsPathManagerNotifiesAfterAddAndRemove(t *testing.T) {
	registry := &notifyingPATHRegistry{value: `C:\\tools`, valueType: registryExpandString}
	notifications := 0
	manager := newWindowsPathManager(`C:\\managed`, registry, func() error {
		notifications++
		return nil
	})

	receipt, changed, err := manager.Ensure()
	if err != nil || !changed {
		t.Fatalf("Ensure() = %#v, %t, %v; want changed success", receipt, changed, err)
	}
	if err := manager.Remove(receipt, true); err != nil {
		t.Fatal(err)
	}
	if notifications != 2 {
		t.Fatalf("notifications = %d, want one after add and one after remove", notifications)
	}
}

func TestWindowsPathManagerRestoresAddedPATHWhenNotificationFails(t *testing.T) {
	registry := &notifyingPATHRegistry{value: `C:\\tools`, valueType: registryExpandString}
	notifyErr := errors.New("broadcast failed")
	notifications := 0
	manager := newWindowsPathManager(`C:\\managed`, registry, func() error {
		notifications++
		if notifications == 1 {
			return notifyErr
		}
		return nil
	})

	_, changed, err := manager.Ensure()
	if err == nil || !errors.Is(err, notifyErr) {
		t.Fatalf("Ensure() error = %v, want notification failure", err)
	}
	if changed {
		t.Fatal("Ensure() reported a changed PATH after notification failure")
	}
	if registry.value != `C:\\tools` || registry.valueType != registryExpandString {
		t.Fatalf("registry after rollback = %q, %d; want original value and type", registry.value, registry.valueType)
	}
	if got, want := strings.Join(registry.writes, ","), `C:\\tools;C:\\managed,C:\\tools`; got != want {
		t.Fatalf("registry writes = %q, want %q", got, want)
	}
	if notifications != 2 {
		t.Fatalf("notifications = %d, want failed change broadcast and successful rollback broadcast", notifications)
	}
}

func TestWindowsPathManagerJoinsRemoveNotificationAndRollbackFailures(t *testing.T) {
	registry := &notifyingPATHRegistry{
		value: `C:\\tools;C:\\managed`, valueType: registryString,
		writeErrors: []error{nil, errors.New("restore failed")},
	}
	notifyErr := errors.New("broadcast failed")
	manager := newWindowsPathManager(`C:\\managed`, registry, func() error { return notifyErr })
	receipt := pathReceipt{Version: 1, Kind: "windows-user-path", Entry: `C:\\managed`}

	err := manager.Remove(receipt, true)
	if err == nil || !errors.Is(err, notifyErr) || !strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("Remove() error = %v, want joined notification and rollback failures", err)
	}
}

type notifyingPATHRegistry struct {
	value       string
	valueType   uint32
	writeErrors []error
	writes      []string
}

func (f *notifyingPATHRegistry) Read() (string, uint32, error) {
	return f.value, f.valueType, nil
}

func (f *notifyingPATHRegistry) Write(value string, valueType uint32) error {
	f.writes = append(f.writes, value)
	if len(f.writeErrors) > 0 {
		err := f.writeErrors[0]
		f.writeErrors = f.writeErrors[1:]
		if err != nil {
			return err
		}
	}
	f.value, f.valueType = value, valueType
	return nil
}
