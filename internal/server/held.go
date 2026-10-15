package server

import "sync"

// heldDirs counts, per account dir, the engines a straggling request
// still holds after a timed-out teardown (teardownEngineLocked).
// Process-wide, not per deps: the host may stop and start the server
// in the same process (mobile), and the SDK DB stays open until the
// straggler leaves. A boot of a held dir is refused as account_in_use
// until closeResources has run; the OS lock says the same where it
// exists, and the mobile build has none.
var (
	heldMu   sync.Mutex
	heldDirs = map[string]int{}
)

func holdDir(dir string) {
	heldMu.Lock()
	defer heldMu.Unlock()
	heldDirs[dir]++
}

// releaseDir is holdDir's inverse, called once the engine's resources
// are closed.
func releaseDir(dir string) {
	heldMu.Lock()
	defer heldMu.Unlock()
	if heldDirs[dir]--; heldDirs[dir] <= 0 {
		delete(heldDirs, dir)
	}
}

func dirHeld(dir string) bool {
	heldMu.Lock()
	defer heldMu.Unlock()
	return heldDirs[dir] > 0
}
