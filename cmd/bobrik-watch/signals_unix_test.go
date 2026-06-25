//go:build !windows

package main

import (
	"syscall"
	"testing"
)

func TestBootstrapControlSignalUsesUnixSignals(t *testing.T) {
	sig, err := bootstrapControlSignal(false)
	if err != nil {
		t.Fatalf("bootstrapControlSignal(false) error = %v", err)
	}
	if sig != syscall.SIGHUP {
		t.Fatalf("bootstrapControlSignal(false) = %s, want %s", sig, syscall.SIGHUP)
	}

	sig, err = bootstrapControlSignal(true)
	if err != nil {
		t.Fatalf("bootstrapControlSignal(true) error = %v", err)
	}
	if sig != syscall.SIGUSR1 {
		t.Fatalf("bootstrapControlSignal(true) = %s, want %s", sig, syscall.SIGUSR1)
	}
}

func TestIsBootstrapCleanSignalRecognizesSIGUSR1OnUnix(t *testing.T) {
	if !isBootstrapCleanSignal(syscall.SIGUSR1) {
		t.Fatal("isBootstrapCleanSignal(SIGUSR1) = false, want true")
	}
	if isBootstrapCleanSignal(syscall.SIGHUP) {
		t.Fatal("isBootstrapCleanSignal(SIGHUP) = true, want false")
	}
}
