//go:build gomobile

// These tests cover the gomobile bind shim's thin forwarders over
// internal/embedded. Because mobile.go carries `//go:build android ||
// gomobile`, this suite compiles and runs ONLY under `-tags gomobile`; a
// default-tag `go test ./mobile/...` builds nothing here (a false green).
// Run it with: go test -tags gomobile ./mobile/android/ -race
package mobile

import (
	"net"
	"testing"

	"github.com/anyproto/any/internal/config"
)

const loopbackEphemeral = "127.0.0.1:0" // OS-assigned free port

// nodeconfFixture is the in-repo sanitized placeholder
// (internal/config/nodeconf-placeholder.yml). It boots + binds but joins
// no network — exactly what these forwarder tests need. Passed
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

// reset clears the process-global embedded singleton between tests via the
// shim's own surface so a failed test can't poison the next. Stop() drains
// gracefully; safe when nothing is running.
func reset(t *testing.T) {
	t.Helper()
	if err := Stop(); err != nil {
		t.Fatalf("reset Stop: %v", err)
	}
}

// TestStartAddressStop proves Start binds (Address reports the bound
// host:port), and Stop clears it.
func TestStartAddressStop(t *testing.T) {
	reset(t)
	defer reset(t)

	if err := Start(t.TempDir(), loopbackEphemeral, nodeconfFixture(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}

	addr := Address()
	if addr == "" {
		t.Fatal("Address() empty after a successful Start")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("Address() %q not parseable: %v", addr, err)
	}
	if host == "" || port == "" || port == "0" {
		t.Fatalf("Address() %q did not resolve to a bound host:port", addr)
	}

	if err := Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := Address(); got != "" {
		t.Fatalf("Address() = %q after Stop, want empty", got)
	}
}

// No empty-nodeconf case here: "" is now a valid input that selects the
// embedded production conf, and starting on it would join production.
// The fall-through is asserted in internal/embedded (assembleConfig) and
// internal/config (LoadNodeconf).

// TestDoubleStartIsError proves a second Start while one is running is
// rejected (the core's already-running guard, surfaced as an error).
func TestDoubleStartIsError(t *testing.T) {
	reset(t)
	defer reset(t)

	if err := Start(t.TempDir(), loopbackEphemeral, nodeconfFixture(t)); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := Start(t.TempDir(), loopbackEphemeral, nodeconfFixture(t)); err == nil {
		t.Fatal("second Start returned nil, want already-running error")
	}
}

// TestStopNow proves the hard-stop forwarder tears the server down: after
// StopNow the address is cleared and a fresh Start succeeds (the core's
// drain channel serializes the restart on the same data dir).
func TestStopNow(t *testing.T) {
	reset(t)
	defer reset(t)

	dataDir := t.TempDir()
	nodeconf := nodeconfFixture(t)

	if err := Start(dataDir, loopbackEphemeral, nodeconf); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if Address() == "" {
		t.Fatal("Address() empty after Start")
	}

	StopNow()
	if got := Address(); got != "" {
		t.Fatalf("Address() = %q after StopNow, want empty", got)
	}

	// A fresh Start on the same data dir must succeed once the prior
	// hard-stop's drain completes (the core waits on it internally).
	if err := Start(dataDir, loopbackEphemeral, nodeconf); err != nil {
		t.Fatalf("Start after StopNow: %v", err)
	}
}

// TestVersionNonEmpty proves the Version forwarder returns the
// build-stamped string (never empty).
func TestVersionNonEmpty(t *testing.T) {
	if Version() == "" {
		t.Fatal("Version() returned empty string")
	}
}

// TestStopWhenNotRunning proves Stop is a safe no-op when nothing is
// running (the shim must not error on a stop-before-start).
func TestStopWhenNotRunning(t *testing.T) {
	reset(t)
	if err := Stop(); err != nil {
		t.Fatalf("Stop with no server running: %v", err)
	}
}
