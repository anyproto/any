package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// resetHandle clears the package-level server handle between tests so a
// prior test's start/stop can't leak state into the next. startServer
// guards on `handle != nil`, so every test that starts must stop.
func resetHandle(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		stopServer(false)
	})
}

// TestStartServer_BadDataDir asserts the data-dir failure paths funnel to
// errBadDataDir: an empty path, and a path whose parent is a file (so
// MkdirAll cannot create it).
func TestStartServer_BadDataDir(t *testing.T) {
	resetHandle(t)

	if got := startServer("", 0); got != errBadDataDir {
		t.Fatalf("empty data dir: got %d, want errBadDataDir (%d)", got, errBadDataDir)
	}

	// A regular file, then a path UNDER it — MkdirAll must fail because a
	// file is in the way.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := writeFile(file); err != nil {
		t.Fatal(err)
	}
	under := filepath.Join(file, "child")
	if got := startServer(under, 0); got != errBadDataDir {
		t.Fatalf("data dir under a file: got %d, want errBadDataDir (%d)", got, errBadDataDir)
	}
}

// TestStartServer_DoubleStart asserts the second concurrent start returns
// errAlreadyRunning while the first is up.
func TestStartServer_DoubleStart(t *testing.T) {
	resetHandle(t)

	port := startServer(t.TempDir(), 0)
	if port <= 0 {
		t.Fatalf("first start: got %d, want a positive port", port)
	}

	if got := startServer(t.TempDir(), 0); got != errAlreadyRunning {
		t.Fatalf("second start: got %d, want errAlreadyRunning (%d)", got, errAlreadyRunning)
	}
}

// TestStartServer_PortDelivered asserts startServer returns the bound
// ephemeral port and that the listener is actually reachable on it.
func TestStartServer_PortDelivered(t *testing.T) {
	resetHandle(t)

	port := startServer(t.TempDir(), 0)
	if port <= 0 {
		t.Fatalf("start: got %d, want a positive port", port)
	}

	// The handle records the same port the function returned.
	handleMu.Lock()
	hp := handle.port
	handleMu.Unlock()
	if hp != port {
		t.Fatalf("handle.port = %d, returned port = %d; want equal", hp, port)
	}

	// The port is bound: a dial succeeds.
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial bound port %d: %v", port, err)
	}
	conn.Close()
}

// TestStartServer_ServesHealth boots with embedder=none (FTS-only) and
// exercises a REAL /v1 flow end-to-end: GET /v1/health over the bound
// port. Route picked via the probe-any-api skill — /v1/health is a
// system route that serves in UNAUTHORIZED mode (an empty data dir has
// no account to boot), so it needs no staging peers and runs fully
// offline. It returns api.HealthResponse with an empty account field
// when unauthorized.
func TestStartServer_ServesHealth(t *testing.T) {
	resetHandle(t)

	port := startServer(t.TempDir(), 0)
	if port <= 0 {
		t.Fatalf("start: got %d, want a positive port", port)
	}

	url := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/v1/health"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /v1/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/health: status %d, want 200", resp.StatusCode)
	}

	var h struct {
		Account   string `json:"account"`
		Status    string `json:"status"`
		StartedAt string `json:"startedAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	// Unauthorized boot reports an empty account.
	if h.Account != "" {
		t.Errorf("unauthorized health account = %q, want empty", h.Account)
	}
	if h.StartedAt == "" {
		t.Error("health startedAt is empty, want a timestamp")
	}
}

// TestStartStopRestart is the lifecycle test: start -> port -> stop ->
// restart, with no leaked goroutine and no leaked listener (the port is
// free / rebindable after each stop). iOS restarts the server on
// foreground, so start->stop->start must be clean.
func TestStartStopRestart(t *testing.T) {
	resetHandle(t)

	baseline := runtime.NumGoroutine()

	const cycles = 3
	for i := 0; i < cycles; i++ {
		port := startServer(t.TempDir(), 0)
		if port <= 0 {
			t.Fatalf("cycle %d start: got %d, want a positive port", i, port)
		}

		// Server is reachable.
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatalf("cycle %d dial %d: %v", i, port, err)
		}
		conn.Close()

		stopServer(true)

		// The handle is cleared after stop.
		handleMu.Lock()
		cleared := handle == nil
		handleMu.Unlock()
		if !cleared {
			t.Fatalf("cycle %d: handle not cleared after stop", i)
		}

		// The listener is gone: the freed port can be re-bound. This is
		// a convergence check (bounded retries), not a sleep-to-fix —
		// the OS may hold TIME_WAIT briefly, but a fresh Listen on the
		// same port must succeed quickly.
		assertPortRebindable(t, port)
	}

	// No goroutine leak across the cycles: the server goroutine from
	// each cycle's start must have exited by its stop. Poll with bounded
	// retries to let any in-flight teardown settle (convergence, not a
	// sleep-to-fix). A small slack absorbs runtime/background goroutines
	// (GC, netpoll) that are not server-owned.
	const slack = 2
	final := waitGoroutines(t, baseline+slack)
	if final > baseline+slack {
		t.Errorf("goroutine leak: started at %d, ended at %d (> baseline+%d)", baseline, final, slack)
	}
}

// assertPortRebindable asserts that, within a bounded retry window, the
// given TCP port on 127.0.0.1 can be bound again — proving the prior
// server's listener was fully released.
func assertPortRebindable(t *testing.T, port int) {
	t.Helper()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(5 * time.Second)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			ln.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("port %d not rebindable within deadline: %v", port, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitGoroutines polls NumGoroutine until it is at or below want, or a
// bounded deadline elapses, returning the last observed count. It is a
// convergence check for "the server goroutine has exited", not a
// sleep-to-fix: teardown is asynchronous (the run goroutine returns
// shortly after ctx cancel), so we wait for it to converge rather than
// asserting on a single racy sample.
func waitGoroutines(t *testing.T, want int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	last := runtime.NumGoroutine()
	for {
		last = runtime.NumGoroutine()
		if last <= want {
			return last
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// writeFile creates an empty regular file at path.
func writeFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}
