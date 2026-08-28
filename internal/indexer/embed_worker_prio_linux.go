//go:build vector && !gomobile && linux

package indexer

import (
	"os"
	"strconv"
	"syscall"
)

// lowerChildPriority nices this process so a re-index yields to
// interactive work. Linux nice is per-THREAD and inherited across
// clone(), so nicing every task that exists now also covers the Go
// runtime threads created later and the decode threads llama.cpp
// spawns from them — hence the /proc walk rather than one call.
// Called before the model loads, i.e. before any of those exist.
func lowerChildPriority(niceness int) {
	if niceness <= 0 {
		return
	}
	ents, err := os.ReadDir("/proc/self/task")
	if err != nil {
		_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, niceness)
		return
	}
	for _, e := range ents {
		if tid, err := strconv.Atoi(e.Name()); err == nil {
			_ = syscall.Setpriority(syscall.PRIO_PROCESS, tid, niceness)
		}
	}
}
