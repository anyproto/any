//go:build llamacpp && !android && !ios && (darwin || freebsd || netbsd || openbsd)

package indexer

import "syscall"

// lowerChildPriority nices this process. BSD nice is per-process, so
// one call covers every thread, present and future.
func lowerChildPriority(niceness int) {
	if niceness > 0 {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, niceness)
	}
}
