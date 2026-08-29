//go:build vector && !gomobile && !windows

package indexer

import "os/exec"

// applyLowPriority is a no-op on Unix: the child nices itself at
// startup (lowerChildPriority), which is the only way to cover the
// threads llama.cpp spawns later.
func applyLowPriority(*exec.Cmd, int) {}
