package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/server"
)

// TestTopLevelName pins the exemption key of the address discovery:
// nested commands resolve to their top-level group, so `run` (and its
// hidden children), `init` and `stop` never probe the data dir.
func TestTopLevelName(t *testing.T) {
	root := newRootCmd()
	for path, want := range map[string]string{
		"run":        "run",
		"init":       "init",
		"stop":       "stop",
		"auth login": "auth",
		"auth":       "auth",
		"status":     "status",
	} {
		args := strings.Fields(path)
		cmd, _, err := root.Find(args)
		if err != nil {
			t.Fatalf("find %q: %v", path, err)
		}
		if got := topLevelName(cmd); got != want {
			t.Errorf("topLevelName(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestDiscoverAddr pins the default-address rule: exactly one running
// account under the data dir with a recorded address → that address;
// none or several → "" (the caller falls back to the default). A
// managed host's ANY_MODE in the environment must not break it.
func TestDiscoverAddr(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ANY_DATA_DIR", root)
	t.Setenv("ANY_ACCOUNT", "")
	os.Unsetenv("ANY_ACCOUNT")
	t.Setenv("ANY_MODE", "managed")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if got := discoverAddr(); got != "" {
		t.Fatalf("empty root: %q, want \"\"", got)
	}

	one := config.AccountDir(root, "Aone")
	if err := os.MkdirAll(one, 0o700); err != nil {
		t.Fatal(err)
	}
	lock1, err := server.Acquire(one)
	if err != nil {
		t.Fatal(err)
	}
	defer lock1.Release()
	if err := os.WriteFile(config.AddrPath(one), []byte("127.0.0.1:4343\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := discoverAddr(); got != "127.0.0.1:4343" {
		t.Fatalf("one running: %q", got)
	}

	two := config.AccountDir(root, "Atwo")
	if err := os.MkdirAll(two, 0o700); err != nil {
		t.Fatal(err)
	}
	lock2, err := server.Acquire(two)
	if err != nil {
		t.Fatal(err)
	}
	defer lock2.Release()
	if got := discoverAddr(); got != "" {
		t.Fatalf("two running, no selector: %q, want \"\"", got)
	}
	t.Setenv("ANY_ACCOUNT", "Aone")
	if got := discoverAddr(); got != "127.0.0.1:4343" {
		t.Fatalf("two running, ANY_ACCOUNT selects: %q", got)
	}
}
