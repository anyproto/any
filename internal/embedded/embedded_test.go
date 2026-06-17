package embedded

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

// nodeconfFixture is the in-repo sanitized staging placeholder
// (internal/config/nodeconf-staging.yml). It boots + binds but joins no
// network — exactly what these lifecycle tests need. Read relative to
// this package's dir (go test runs with cwd == package dir).
func nodeconfFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "config", "nodeconf-staging.yml"))
	if err != nil {
		t.Fatalf("read nodeconf fixture: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("nodeconf fixture is empty")
	}
	return string(raw)
}

const loopbackEphemeral = "127.0.0.1:0" // OS-assigned free port

// resetState clears any leftover singleton/drain state so a failed test
// can't poison the next one (the package state is process-global).
func resetState(t *testing.T) {
	t.Helper()
	// Best-effort graceful stop, then wait out any pending hard-stop drain.
	Stop(true)
	handleMu.Lock()
	prev := stopping
	handleMu.Unlock()
	if prev != nil {
		select {
		case <-prev:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for prior hard-stop drain")
		}
	}
}

func TestStartAddressStop(t *testing.T) {
	resetState(t)
	defer resetState(t)

	addr, err := Start(t.TempDir(), loopbackEphemeral, nodeconfFixture(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if addr == "" {
		t.Fatal("Start returned empty addr")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("returned addr %q not parseable: %v", addr, err)
	}
	if host == "" || port == "" || port == "0" {
		t.Fatalf("returned addr %q did not resolve to a bound host:port", addr)
	}
	if got := Address(); got != addr {
		t.Fatalf("Address() = %q, want %q (the bound addr from Start)", got, addr)
	}

	Stop(true)
	if got := Address(); got != "" {
		t.Fatalf("Address() = %q after Stop, want empty", got)
	}
}

func TestDoubleStartIsAlreadyRunning(t *testing.T) {
	resetState(t)
	defer resetState(t)

	if _, err := Start(t.TempDir(), loopbackEphemeral, nodeconfFixture(t)); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	_, err := Start(t.TempDir(), loopbackEphemeral, nodeconfFixture(t))
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Start err = %v, want ErrAlreadyRunning", err)
	}
}

func TestBadDataDirIsBadDirError(t *testing.T) {
	resetState(t)
	defer resetState(t)

	// Empty data dir is the simplest bad-dir case.
	if _, err := Start("", loopbackEphemeral, nodeconfFixture(t)); !errors.Is(err, ErrBadDataDir) {
		t.Fatalf("empty data dir err = %v, want ErrBadDataDir", err)
	}

	// An un-creatable path (a child of a regular file) is also bad-dir.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup file: %v", err)
	}
	badPath := filepath.Join(file, "child")
	if _, err := Start(badPath, loopbackEphemeral, nodeconfFixture(t)); !errors.Is(err, ErrBadDataDir) {
		t.Fatalf("uncreatable data dir err = %v, want ErrBadDataDir", err)
	}
}

func TestEmptyNodeconfIsError(t *testing.T) {
	resetState(t)
	defer resetState(t)

	_, err := Start(t.TempDir(), loopbackEphemeral, "")
	if !errors.Is(err, ErrNodeconfRequired) {
		t.Fatalf("empty nodeconf err = %v, want ErrNodeconfRequired", err)
	}
}

// TestStartStopRestartNoLeak exercises the start->stop->restart cycle N
// times on the SAME data dir (the background->foreground restart shape).
// With -race it proves the drain channel serializes teardown so two
// engines never run on one data dir, and the goroutine-count check guards
// against a leaked run goroutine / listener.
func TestStartStopRestartNoLeak(t *testing.T) {
	resetState(t)
	defer resetState(t)

	dataDir := t.TempDir()
	nodeconf := nodeconfFixture(t)

	// Let any pre-test goroutines settle, then snapshot the baseline.
	time.Sleep(200 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	const cycles = 3
	for i := 0; i < cycles; i++ {
		addr, err := Start(dataDir, loopbackEphemeral, nodeconf)
		if err != nil {
			t.Fatalf("cycle %d Start: %v", i, err)
		}
		if addr == "" {
			t.Fatalf("cycle %d Start returned empty addr", i)
		}
		// Alternate graceful and hard stops to cover both teardown paths
		// and the drain-channel wait on the next Start.
		Stop(i%2 == 0)
	}

	// Final graceful stop + drain wait so the last cycle is fully torn
	// down before we count goroutines.
	resetState(t)

	// Allow the runtime a moment to reap finished goroutines, then assert
	// we did not accumulate per-cycle leaks. A small constant slack
	// absorbs runtime/GC bookkeeping goroutines.
	leaked := -1
	for attempt := 0; attempt < 20; attempt++ {
		time.Sleep(100 * time.Millisecond)
		if n := runtime.NumGoroutine(); n <= baseline+2 {
			leaked = 0
			break
		} else {
			leaked = n - baseline
		}
	}
	if leaked != 0 {
		t.Fatalf("goroutine leak across %d cycles: baseline=%d, leaked≈%d", cycles, baseline, leaked)
	}
}

// TestMemoryLimitApplied confirms Start installs the GOMEMLIMIT soft cap.
// debug.SetMemoryLimit(-1) returns the current limit without changing it.
func TestMemoryLimitApplied(t *testing.T) {
	resetState(t)
	defer resetState(t)

	// Set a sentinel limit first so we can observe Start overwrite it.
	// debug.SetMemoryLimit returns the PREVIOUS limit, and a -1 argument
	// reads the current one without changing it (used below to assert).
	prev := debug.SetMemoryLimit(1 << 62)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })

	addr, err := Start(t.TempDir(), loopbackEphemeral, nodeconfFixture(t))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if addr == "" {
		t.Fatal("Start returned empty addr")
	}
	defer Stop(true)

	if got := debug.SetMemoryLimit(-1); got != gomemlimitBytes {
		t.Fatalf("GOMEMLIMIT = %d after Start, want %d (256 MiB)", got, gomemlimitBytes)
	}
}
