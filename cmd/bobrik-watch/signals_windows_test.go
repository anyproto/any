//go:build windows

package main

import (
	"strings"
	"syscall"
	"testing"
)

func TestBootstrapControlSignalRequiresRestartOnWindows(t *testing.T) {
	for _, clean := range []bool{false, true} {
		sig, err := bootstrapControlSignal(clean)
		if err == nil {
			t.Fatalf("bootstrapControlSignal(%v) error = nil, want restart-required error", clean)
		}
		if sig != 0 {
			t.Fatalf("bootstrapControlSignal(%v) signal = %s, want zero signal", clean, sig)
		}
		if !strings.Contains(err.Error(), "restart bobrik-watch") {
			t.Fatalf("bootstrapControlSignal(%v) error = %q, want restart guidance", clean, err)
		}
	}
}

func TestIsBootstrapCleanSignalNeverMatchesOnWindows(t *testing.T) {
	if isBootstrapCleanSignal(syscall.SIGHUP) {
		t.Fatal("isBootstrapCleanSignal(SIGHUP) = true, want false")
	}
	if isBootstrapCleanSignal(syscall.SIGINT) {
		t.Fatal("isBootstrapCleanSignal(SIGINT) = true, want false")
	}
}
