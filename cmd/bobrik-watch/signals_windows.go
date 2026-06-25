//go:build windows

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func bootstrapControlSignal(clean bool) (syscall.Signal, error) {
	flag := "--bootstrap"
	if clean {
		flag = "--bootstrap-clean"
	}
	// TODO: Replace Unix signal control with current-user-only local IPC if
	// Windows needs live refresh. Until then, Windows refresh is restart-only.
	return 0, fmt.Errorf("%s is unsupported on windows; restart bobrik-watch to refresh programs and skills", flag)
}

func notifyProcessSignals(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
}

func isBootstrapCleanSignal(os.Signal) bool {
	return false
}
