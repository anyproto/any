//go:build vector && !gomobile && windows

package indexer

import (
	"os/exec"
	"syscall"
)

// Windows has no nice(2): priority is a per-process class set at spawn.
const (
	belowNormalPriorityClass = 0x00004000
	idlePriorityClass        = 0x00000040
)

// applyLowPriority spawns the child in a background priority class —
// the whole process, threads included.
func applyLowPriority(cmd *exec.Cmd, niceness int) {
	if niceness <= 0 {
		return
	}
	class := uint32(belowNormalPriorityClass)
	if niceness >= 15 {
		class = idlePriorityClass
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= class
}

// lowerChildPriority is a no-op on Windows: the parent set the priority
// class at spawn.
func lowerChildPriority(int) {}
