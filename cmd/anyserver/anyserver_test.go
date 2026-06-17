package main

// Host-build tests for the c-archive shim. They exercise the plain Go
// funcs (startServer / stopServer) and the err->code mapping (errCode) —
// the //export AnyServerStart/Stop/StopNow wrappers cannot be called from
// `go test` (that's the accepted seam, exercised by the Swift app in Phase
// C). The package imports "C", but a plain _test.go in the same package
// still compiles under cgo for host `go test` as long as it doesn't call
// the //export wrappers.
//
// The deep lifecycle behaviour (double-start, restart, goroutine/listener
// leak under -race, GOMEMLIMIT) is owned and tested once by
// internal/embedded; here we only assert the shim's own two jobs: turn the
// bound addr into a positive port on success, and turn each embedded
// sentinel into the right negative code.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/anyproto/any/internal/embedded"
)

const loopbackEphemeral = "127.0.0.1:0" // OS-assigned free port

// nodeconfFixture is the in-repo sanitized staging placeholder
// (internal/config/nodeconf-staging.yml). It boots + binds but joins no
// network — exactly what this lifecycle test needs. Read relative to this
// package's dir (go test runs with cwd == package dir).
func nodeconfFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "config", "nodeconf-staging.yml"))
	if err != nil {
		t.Fatalf("read nodeconf fixture: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("nodeconf fixture is empty")
	}
	return string(raw)
}

// cleanupServer guarantees the process-global embedded singleton is torn
// down after a test that started it, so a leftover server can't poison the
// next test (the embedded state is process-global).
func cleanupServer(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { stopServer(false) })
}

// TestStartServer_SuccessReturnsPort asserts the shim's success path:
// startServer boots via embedded.Start and returns the bound ephemeral
// port (positive) parsed from the returned addr, and the port is actually
// reachable. Then stopServer(true) tears it down.
func TestStartServer_SuccessReturnsPort(t *testing.T) {
	cleanupServer(t)

	port := startServer(t.TempDir(), loopbackEphemeral, nodeconfFixture(t))
	if port <= 0 {
		t.Fatalf("startServer: got %d, want a positive port", port)
	}

	// The reported port matches the server's actually-bound address.
	if got := portOf(embedded.Address()); got != port {
		t.Fatalf("portOf(Address())=%d, startServer returned %d — mismatch", got, port)
	}

	// The port is bound: a dial succeeds.
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("dial bound port %d: %v", port, err)
	}
	conn.Close()

	stopServer(true)

	// After a graceful stop the singleton is cleared: Address() is empty.
	if addr := embedded.Address(); addr != "" {
		t.Fatalf("after stop: Address()=%q, want empty", addr)
	}
}

// TestStartServer_AlreadyRunning asserts a second start while one is up
// maps embedded.ErrAlreadyRunning -> errAlreadyRunning (-1).
func TestStartServer_AlreadyRunning(t *testing.T) {
	cleanupServer(t)

	if port := startServer(t.TempDir(), loopbackEphemeral, nodeconfFixture(t)); port <= 0 {
		t.Fatalf("first start: got %d, want a positive port", port)
	}

	if got := startServer(t.TempDir(), loopbackEphemeral, nodeconfFixture(t)); got != errAlreadyRunning {
		t.Fatalf("second start: got %d, want errAlreadyRunning (%d)", got, errAlreadyRunning)
	}
}

// TestStartServer_BadDataDir asserts the bad-data-dir paths map
// embedded.ErrBadDataDir -> errBadDataDir (-2): an empty path, and a path
// whose parent is a file (so MkdirAll cannot create it). A non-empty
// nodeconf is supplied so the failure is genuinely the data dir, not a
// missing nodeconf.
func TestStartServer_BadDataDir(t *testing.T) {
	cleanupServer(t)

	if got := startServer("", loopbackEphemeral, nodeconfFixture(t)); got != errBadDataDir {
		t.Fatalf("empty data dir: got %d, want errBadDataDir (%d)", got, errBadDataDir)
	}

	// A regular file, then a path UNDER it — EnsureDataDir's MkdirAll must
	// fail because a file is in the way.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	under := filepath.Join(file, "child")
	if got := startServer(under, loopbackEphemeral, nodeconfFixture(t)); got != errBadDataDir {
		t.Fatalf("data dir under a file: got %d, want errBadDataDir (%d)", got, errBadDataDir)
	}
}

// TestStartServer_EmptyNodeconfIsBoot asserts an empty nodeconf maps
// embedded.ErrNodeconfRequired -> errBoot (-3): online-only means the host
// must supply a real nodeconf, and a missing one is a boot-class config
// failure, not a bad data dir.
func TestStartServer_EmptyNodeconfIsBoot(t *testing.T) {
	cleanupServer(t)

	if got := startServer(t.TempDir(), loopbackEphemeral, ""); got != errBoot {
		t.Fatalf("empty nodeconf: got %d, want errBoot (%d)", got, errBoot)
	}
}

// TestErrCode_Mapping asserts the err->code translation directly, including
// the boot-class folding: ErrAlreadyRunning -> -1, ErrBadDataDir -> -2, and
// both ErrNodeconfRequired and any *BootError -> -3. Driving errCode with
// the sentinels (incl. wrapped ones) pins the contract the Swift side
// mirrors without needing a live server.
func TestErrCode_Mapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"already running", embedded.ErrAlreadyRunning, errAlreadyRunning},
		{"bad data dir", embedded.ErrBadDataDir, errBadDataDir},
		{"bad data dir wrapped", fmt.Errorf("ensure: %w", embedded.ErrBadDataDir), errBadDataDir},
		{"nodeconf required is boot-class", embedded.ErrNodeconfRequired, errBoot},
		{"boot error", &embedded.BootError{Err: errors.New("bind failed")}, errBoot},
		{"unknown error funnels to boot", errors.New("some other error"), errBoot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errCode(tc.err); got != tc.want {
				t.Fatalf("errCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
