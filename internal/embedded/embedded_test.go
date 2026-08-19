package embedded

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/config"
)

// nodeconfFixture is the in-repo sanitized placeholder
// (internal/config/nodeconf-placeholder.yml). It boots + binds but joins
// no network — exactly what these lifecycle tests need. Passed
// explicitly: an empty Network falls through to the embedded PRODUCTION
// default, which tests must never join.
func nodeconfFixture(t *testing.T) string {
	t.Helper()
	raw := config.NodeconfPlaceholder()
	if len(raw) == 0 {
		t.Fatal("nodeconf fixture is empty")
	}
	return string(raw)
}

const loopbackEphemeral = "127.0.0.1:0" // OS-assigned free port

// start is the lifecycle tests' shorthand for the common Options shape:
// ephemeral loopback listen, index on, no push node.
func start(dataDir, nodeconfYAML string) (string, error) {
	return Start(Options{
		DataDir:      dataDir,
		ListenAddr:   loopbackEphemeral,
		NodeconfYAML: nodeconfYAML,
		IndexEnabled: true,
	})
}

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

	addr, err := start(t.TempDir(), nodeconfFixture(t))
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

	if _, err := start(t.TempDir(), nodeconfFixture(t)); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	_, err := start(t.TempDir(), nodeconfFixture(t))
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Start err = %v, want ErrAlreadyRunning", err)
	}
}

func TestBadDataDirIsBadDirError(t *testing.T) {
	resetState(t)
	defer resetState(t)

	// Empty data dir is the simplest bad-dir case.
	if _, err := start("", nodeconfFixture(t)); !errors.Is(err, ErrBadDataDir) {
		t.Fatalf("empty data dir err = %v, want ErrBadDataDir", err)
	}

	// An un-creatable path (a child of a regular file) is also bad-dir.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup file: %v", err)
	}
	badPath := filepath.Join(file, "child")
	if _, err := start(badPath, nodeconfFixture(t)); !errors.Is(err, ErrBadDataDir) {
		t.Fatalf("uncreatable data dir err = %v, want ErrBadDataDir", err)
	}
}

// An empty nodeconfYAML means "use the embedded default", so the host
// (an Android/iOS app) doesn't have to vendor its own copy of the
// production conf. Asserted on the assembled config rather than by
// starting: booting the default would join the production network.
func TestEmptyNodeconfFallsThroughToEmbeddedDefault(t *testing.T) {
	for _, in := range []string{"", "  \n\t "} {
		cfg := assembleConfig(Options{DataDir: t.TempDir(), ListenAddr: loopbackEphemeral, NodeconfYAML: in})
		if cfg.Network.Nodeconf != "" {
			t.Fatalf("nodeconf %q assembled to %q, want empty so LoadNodeconf picks the default", in, cfg.Network.Nodeconf)
		}
		raw, err := config.LoadNodeconf(cfg.Network)
		if err != nil {
			t.Fatalf("LoadNodeconf: %v", err)
		}
		if !strings.Contains(string(raw), "fileV2") {
			t.Error("fallback is not the production nodeconf")
		}
	}
}

// A supplied conf still wins over the default.
func TestSuppliedNodeconfWins(t *testing.T) {
	cfg := assembleConfig(Options{DataDir: t.TempDir(), ListenAddr: loopbackEphemeral, NodeconfYAML: nodeconfFixture(t)})
	raw, err := config.LoadNodeconf(cfg.Network)
	if err != nil {
		t.Fatalf("LoadNodeconf: %v", err)
	}
	if strings.Contains(string(raw), "fileV2") {
		t.Error("supplied placeholder was overridden by the production default")
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
		addr, err := start(dataDir, nodeconf)
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
	var leaked int
	for attempt := 0; attempt < 20; attempt++ {
		time.Sleep(100 * time.Millisecond)
		leaked = runtime.NumGoroutine() - baseline
		if leaked <= 2 {
			leaked = 0
			break
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

	addr, err := start(t.TempDir(), nodeconfFixture(t))
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

// TestAssembleConfigPush pins the SYN-83 push-node bridge: Options
// push inputs land on cfg.Push and the config tristate alone decides
// activation — both fields non-empty ⇒ Active, anything less ⇒ off.
// Asserted on assembleConfig directly (no engine boot needed).
func TestAssembleConfigPush(t *testing.T) {
	base := Options{DataDir: "/d", ListenAddr: loopbackEphemeral, NodeconfYAML: "nc"}

	t.Run("no push inputs stays off", func(t *testing.T) {
		cfg := assembleConfig(base)
		if cfg.Push.Active() {
			t.Fatal("Push.Active() = true with no push inputs, want false")
		}
		if cfg.Push.PeerId != "" || len(cfg.Push.Addrs) != 0 {
			t.Fatalf("Push = %+v, want zero", cfg.Push)
		}
	})

	t.Run("peer id + addrs activates", func(t *testing.T) {
		opts := base
		opts.PushPeerId = "12D3KooWTestPushNode"
		opts.PushAddrs = "quic://host:1101, host2:1102 ,,"
		cfg := assembleConfig(opts)
		if !cfg.Push.Active() {
			t.Fatal("Push.Active() = false, want true (peer id + addrs set)")
		}
		if cfg.Push.PeerId != opts.PushPeerId {
			t.Fatalf("Push.PeerId = %q, want %q", cfg.Push.PeerId, opts.PushPeerId)
		}
		// Comma-split with trim, empties dropped — ANY_PUSH_ADDRS semantics.
		want := []string{"quic://host:1101", "host2:1102"}
		if len(cfg.Push.Addrs) != len(want) || cfg.Push.Addrs[0] != want[0] || cfg.Push.Addrs[1] != want[1] {
			t.Fatalf("Push.Addrs = %v, want %v", cfg.Push.Addrs, want)
		}
	})

	t.Run("peer id without addrs stays off", func(t *testing.T) {
		opts := base
		opts.PushPeerId = "12D3KooWTestPushNode"
		if cfg := assembleConfig(opts); cfg.Push.Active() {
			t.Fatal("Push.Active() = true without addrs, want false")
		}
	})

	t.Run("addrs without peer id stays off", func(t *testing.T) {
		opts := base
		opts.PushAddrs = "quic://host:1101"
		if cfg := assembleConfig(opts); cfg.Push.Active() {
			t.Fatal("Push.Active() = true without peer id, want false")
		}
	})
}
