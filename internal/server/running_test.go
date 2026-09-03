package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anyproto/any/internal/config"
)

// TestFindRunning pins the lock probe: a held lock is a running server
// (pid and addr read from the files beside it), a released one is not
// even though server.pid still names a pid, and the account filter
// picks by dir name. Held in-process — flock locks are per open file
// description, so a second handle in the same process is refused like
// another process would be.
func TestFindRunning(t *testing.T) {
	root := t.TempDir()
	acctDir := config.AccountDir(root, "Aone")
	staleDir := config.AccountDir(root, "Astale")
	for _, dir := range []string{acctDir, staleDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	lock, err := Acquire(acctDir)
	if err != nil {
		t.Fatal(err)
	}
	writeAddrFile(acctDir, "127.0.0.1:4242")
	// A stale account dir: lock file present, nobody holding it.
	if l, err := Acquire(staleDir); err != nil {
		t.Fatal(err)
	} else if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	// Noise that must be ignored: a dir without a lock, a plain file.
	if err := os.MkdirAll(filepath.Join(root, "models"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	running, err := FindRunning(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 {
		t.Fatalf("running = %+v, want exactly the held lock", running)
	}
	got := running[0]
	if got.Account != "Aone" || got.Dir != acctDir || got.PID != os.Getpid() || got.Addr != "127.0.0.1:4242" {
		t.Fatalf("running = %+v", got)
	}

	if running, err = FindRunning(root, "Astale"); err != nil || len(running) != 0 {
		t.Fatalf("stale dir must not read as running: %+v err=%v", running, err)
	}
	if running, err = FindRunning(root, "Aone"); err != nil || len(running) != 1 {
		t.Fatalf("account filter: %+v err=%v", running, err)
	}

	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if running, err = FindRunning(root, ""); err != nil || len(running) != 0 {
		t.Fatalf("released lock must not read as running: %+v err=%v", running, err)
	}

	// A root that does not exist is simply empty.
	if running, err = FindRunning(filepath.Join(root, "missing"), ""); err != nil || len(running) != 0 {
		t.Fatalf("missing root: %+v err=%v", running, err)
	}
}
