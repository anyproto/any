//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// bootstrapControlSignal returns the Unix signal used by the CLI control
// command. SIGHUP refreshes incrementally; SIGUSR1 forces wipe-and-rebuild.
func bootstrapControlSignal(clean bool) (syscall.Signal, error) {
	if clean {
		return syscall.SIGUSR1, nil
	}
	return syscall.SIGHUP, nil
}

func notifyProcessSignals(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGUSR1, syscall.SIGINT, syscall.SIGTERM)
}

func isBootstrapCleanSignal(sig os.Signal) bool {
	return sig == syscall.SIGUSR1
}
