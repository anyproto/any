package main

// Host-build tests for the c-archive shim. They exercise the plain Go
// funcs (startEngine / stopEngine / errCode / copyBounded) — the
// //export AnyLibStart/Stop/StopNow/Version wrappers cannot be called
// from `go test` (that's the accepted seam, exercised by the Swift app),
// and neither can anything typed in terms of C, since cgo is not
// permitted in a _test.go file. The package imports "C", but a plain
// _test.go in the same package still compiles under cgo as long as it
// doesn't reference C itself.
//
// The deep lifecycle behaviour (double-start, restart, goroutine/listener
// leak under -race, GOMEMLIMIT) is owned and tested once by
// internal/embedded; here we only assert the shim's own three jobs: report
// the bound address on success, turn each embedded sentinel into the right
// code, and never overrun a fixed-size C buffer.

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/embedded"
	"github.com/anyproto/any/internal/indexer"
)

const loopbackEphemeral = "127.0.0.1:0" // OS-assigned free port

// addressLen / messageLen mirror the C buffer sizes in the
// AnyLibStartResult typedef. The truncation tests run copyBounded at
// exactly these widths so a change to the typedef that isn't mirrored
// here shows up as a stale constant rather than a silent gap.
const (
	addressLen = 64
	messageLen = 512
)

// nodeconfFixture is the in-repo sanitized placeholder
// (internal/config/nodeconf-placeholder.yml). It boots + binds but joins
// no network — exactly what this lifecycle test needs. Passed
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

// cleanupEngine guarantees the process-global embedded singleton is torn
// down after a test that started it, so a leftover engine can't poison the
// next test (the embedded state is process-global).
func cleanupEngine(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { stopEngine(false) })
}

// start boots with push off — the arguments every lifecycle test shares.
func start(t *testing.T, dataDir string) startResult {
	t.Helper()
	return startEngine(dataDir, loopbackEphemeral, nodeconfFixture(t), "", "")
}

// dialAddr asserts the address is bound and reachable: a TCP dial
// succeeds. It closes the connection immediately.
func dialAddr(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial bound address %q: %v", addr, err)
	}
	conn.Close()
}

// TestStartEngine_SuccessFillsAddress asserts the shim's success path:
// code 0, `address` carrying the bound ephemeral host:port, and `message`
// EMPTY. The empty message is half the contract — the host reads a
// non-empty message as "something went wrong", so a success that leaked
// text would surface a phantom error.
func TestStartEngine_SuccessFillsAddress(t *testing.T) {
	cleanupEngine(t)

	res := start(t, t.TempDir())
	if res.code != codeOK {
		t.Fatalf("startEngine: code %d (%q), want codeOK (%d)", res.code, res.message, codeOK)
	}
	if res.address == "" {
		t.Fatal("startEngine: empty address on success")
	}
	if res.message != "" {
		t.Fatalf("startEngine: message %q on success, want empty", res.message)
	}

	// The reported address is the engine's actually-bound one.
	if got := embedded.Address(); got != res.address {
		t.Fatalf("Address()=%q, startEngine returned %q — mismatch", got, res.address)
	}

	// It is bound: a dial succeeds.
	dialAddr(t, res.address)

	stopEngine(true)

	// After a graceful stop the singleton is cleared: Address() is empty.
	if addr := embedded.Address(); addr != "" {
		t.Fatalf("after stop: Address()=%q, want empty", addr)
	}
}

// TestStartEngine_FailureFillsMessage asserts the mirror of the success
// case, and the reason this whole interface exists: a failure carries a
// non-empty explanation and no address. Driven through the bad-data-dir
// path because it fails deterministically without a live engine.
func TestStartEngine_FailureFillsMessage(t *testing.T) {
	cleanupEngine(t)

	res := start(t, "")
	if res.code != codeBadDataDir {
		t.Fatalf("empty data dir: code %d, want codeBadDataDir (%d)", res.code, codeBadDataDir)
	}
	if res.message == "" {
		t.Fatal("empty data dir: empty message — the host has nothing to show the user")
	}
	if res.address != "" {
		t.Fatalf("empty data dir: address %q, want empty", res.address)
	}
}

// TestStartEngine_AlreadyRunning asserts a second start while one is up
// maps embedded.ErrAlreadyRunning -> codeAlreadyRunning.
func TestStartEngine_AlreadyRunning(t *testing.T) {
	cleanupEngine(t)

	if res := start(t, t.TempDir()); res.code != codeOK {
		t.Fatalf("first start: code %d (%q), want codeOK", res.code, res.message)
	}

	if res := start(t, t.TempDir()); res.code != codeAlreadyRunning {
		t.Fatalf("second start: code %d, want codeAlreadyRunning (%d)", res.code, codeAlreadyRunning)
	}
}

// TestStartEngine_BadDataDir asserts the bad-data-dir paths map
// embedded.ErrBadDataDir -> codeBadDataDir: an empty path, and a path
// whose parent is a file (so MkdirAll cannot create it). A non-empty
// nodeconf is supplied so the failure is genuinely the data dir, not a
// missing nodeconf.
func TestStartEngine_BadDataDir(t *testing.T) {
	cleanupEngine(t)

	if res := start(t, ""); res.code != codeBadDataDir {
		t.Fatalf("empty data dir: code %d, want codeBadDataDir (%d)", res.code, codeBadDataDir)
	}

	// A regular file, then a path UNDER it — EnsureDataDir's MkdirAll must
	// fail because a file is in the way.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	under := filepath.Join(file, "child")
	if res := start(t, under); res.code != codeBadDataDir {
		t.Fatalf("data dir under a file: code %d, want codeBadDataDir (%d)", res.code, codeBadDataDir)
	}
}

// TestErrCode_Mapping asserts the err->code translation directly,
// including the two arms that only exist because of ordering: a
// rebuild-required failure reaches the shim wrapped in a *BootError, so
// the generic boot arm would swallow it if it ran first. The
// "index rebuild required" case is built as the EXACT shape
// internal/server/engine.go produces ("open indexer: %w" inside a
// BootError), so this pins the real chain, not a convenient stand-in.
func TestErrCode_Mapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int32
	}{
		{"already running", embedded.ErrAlreadyRunning, codeAlreadyRunning},
		{"bad data dir", embedded.ErrBadDataDir, codeBadDataDir},
		{"bad data dir wrapped", fmt.Errorf("ensure: %w", embedded.ErrBadDataDir), codeBadDataDir},
		{"boot error", &embedded.BootError{Err: errors.New("bind failed")}, codeBootFailed},
		{"unknown error funnels to boot", errors.New("some other error"), codeBootFailed},
		{
			"index rebuild required, as engine.go wraps it",
			&embedded.BootError{Err: fmt.Errorf("open indexer: %w", indexer.ErrIndexRebuildRequired)},
			codeIndexRebuildRequired,
		},
		{"index rebuild required, bare", indexer.ErrIndexRebuildRequired, codeIndexRebuildRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errCode(tc.err); got != tc.want {
				t.Fatalf("errCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// TestCopyBounded_FitsExactly asserts the ordinary case: a string that
// fits is copied whole and NUL-terminated right after it.
func TestCopyBounded_FitsExactly(t *testing.T) {
	dst := make([]byte, addressLen)
	copyBounded(dst, "127.0.0.1:53421")

	if got := cString(dst); got != "127.0.0.1:53421" {
		t.Fatalf("copyBounded = %q, want the address unchanged", got)
	}
}

// TestCopyBounded_EmptyString asserts an empty source still terminates
// the buffer — the success path writes "" into `message`, and a host
// reading an unterminated buffer would run into whatever follows.
func TestCopyBounded_EmptyString(t *testing.T) {
	dst := make([]byte, messageLen)
	for i := range dst {
		dst[i] = 'x' // poison, so a missing write is visible
	}
	copyBounded(dst, "")

	if dst[0] != 0 {
		t.Fatalf("copyBounded(\"\") left dst[0] = %q, want NUL", dst[0])
	}
}

// TestCopyBounded_TruncatesAndTerminates is the falsifying case for the
// fixed-size buffers: 600 ASCII bytes into the 512-byte `message`
// buffer. It must keep the first 511 and put the NUL at index 511 — one
// byte further and it writes outside the C struct field.
func TestCopyBounded_TruncatesAndTerminates(t *testing.T) {
	const input = 600 // > messageLen, the whole point

	dst := make([]byte, messageLen)
	copyBounded(dst, strings.Repeat("a", input))

	if dst[messageLen-1] != 0 {
		t.Fatalf("dst[%d] = %q, want NUL at the boundary", messageLen-1, dst[messageLen-1])
	}
	if got := cString(dst); got != strings.Repeat("a", messageLen-1) {
		t.Fatalf("truncated to %d bytes, want %d", len(got), messageLen-1)
	}
}

// TestCopyBounded_TruncatesOnRuneBoundary is the sharper falsifying
// case, and the input is chosen so a byte-exact truncation FAILS it: 510
// ASCII bytes followed by an em dash (U+2014, 3 bytes). The 511-byte cut
// lands one byte into that em dash. Splitting it would hand Swift a
// half-encoded rune, which String(validatingCString:) rejects outright —
// so the whole rune has to go.
//
// The input is not hypothetical: internal/indexer's rebuild-required
// messages carry an em dash, and they are exactly what lands in this
// buffer as code 4.
func TestCopyBounded_TruncatesOnRuneBoundary(t *testing.T) {
	const emDash = "—" // 3 bytes; the byte cut lands inside it
	input := strings.Repeat("a", messageLen-2) + emDash + "tail"

	dst := make([]byte, messageLen)
	copyBounded(dst, input)

	got := cString(dst)
	if dst[len(got)] != 0 {
		t.Fatalf("no NUL after the %d copied bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("copyBounded produced invalid UTF-8: %q", got)
	}
	if want := strings.Repeat("a", messageLen-2); got != want {
		t.Fatalf("copyBounded = %d bytes ending %q, want the %d ASCII bytes with the em dash dropped whole",
			len(got), got[len(got)-1:], len(want))
	}
}

// cString reads dst up to its first NUL, the way the C caller does. A
// buffer with no NUL at all comes back whole, so a missing terminator
// shows up as an over-long result rather than a panic.
func cString(dst []byte) string {
	if i := bytes.IndexByte(dst, 0); i >= 0 {
		return string(dst[:i])
	}
	return string(dst)
}
