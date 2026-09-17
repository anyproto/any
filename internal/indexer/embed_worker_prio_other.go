//go:build llamacpp && !android && !ios && !linux && !darwin && !freebsd && !netbsd && !openbsd && !windows

package indexer

// lowerChildPriority is a no-op on platforms with no nice(2) binding
// here; applyLowPriority covers every non-Windows GOOS, so this keeps
// the pair defined on the same build space.
func lowerChildPriority(int) {}
