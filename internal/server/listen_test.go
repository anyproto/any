package server

import (
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/anyproto/any/internal/config"
)

func TestValidateLoopback(t *testing.T) {
	cases := []struct {
		addr    string
		wantErr bool
	}{
		{"127.0.0.1:7001", false},
		{"127.0.0.2:7001", false},
		{"[::1]:7001", false},
		{"0.0.0.0:7001", true},
		{"192.168.1.5:7001", true},
		{"localhost:7001", true}, // named hosts rejected
		{"bad", true},            // no port
	}
	for _, tc := range cases {
		err := ValidateLoopback(tc.addr)
		if tc.wantErr != (err != nil) {
			t.Errorf("ValidateLoopback(%q) err=%v wantErr=%v", tc.addr, err, tc.wantErr)
		}
	}
}

func listenPort(t *testing.T, addr, root string) int {
	t.Helper()
	ln, err := listen(addr, root)
	if err != nil {
		t.Fatalf("listen(%q): %v", addr, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestListen_ZeroPortReusesPreviousPort(t *testing.T) {
	root := t.TempDir()
	first := listenPort(t, "127.0.0.1:0", root)
	if got := readListenPort(config.ListenPortPath(root)); got != first {
		t.Fatalf("listen.port = %d, want %d", got, first)
	}
	if again := listenPort(t, "127.0.0.1:0", root); again != first {
		t.Fatalf("restart bound %d, want the previous %d", again, first)
	}
}

func TestListen_EmptyPortIsSticky(t *testing.T) {
	root := t.TempDir()
	first := listenPort(t, "127.0.0.1:", root)
	if got := readListenPort(config.ListenPortPath(root)); got != first {
		t.Fatalf("listen.port = %d, want %d", got, first)
	}
	if again := listenPort(t, "127.0.0.1:", root); again != first {
		t.Fatalf("restart bound %d, want the previous %d", again, first)
	}
}

func TestListen_TakenPreviousPortFallsBack(t *testing.T) {
	root := t.TempDir()
	holder, err := listen("127.0.0.1:0", root)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	taken := holder.Addr().(*net.TCPAddr).Port

	got := listenPort(t, "127.0.0.1:0", root)
	if got == taken {
		t.Fatalf("bound the taken port %d", taken)
	}
	if saved := readListenPort(config.ListenPortPath(root)); saved != got {
		t.Fatalf("listen.port = %d, want the fallback port %d", saved, got)
	}
}

func TestListen_ExplicitPortIsNotRecorded(t *testing.T) {
	root := t.TempDir()
	free := listenPort(t, "127.0.0.1:0", t.TempDir())
	if got := listenPort(t, "127.0.0.1:"+strconv.Itoa(free), root); got != free {
		t.Fatalf("bound %d, want %d", got, free)
	}
	if _, err := os.Stat(config.ListenPortPath(root)); !os.IsNotExist(err) {
		t.Fatalf("explicit port wrote listen.port: %v", err)
	}
}

func TestListen_ExplicitPortTakenFails(t *testing.T) {
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if ln, err := listen(holder.Addr().String(), t.TempDir()); err == nil {
		ln.Close()
		t.Fatal("explicit taken port bound")
	}
}

func TestListen_CorruptRecordIsReplaced(t *testing.T) {
	root := t.TempDir()
	path := config.ListenPortPath(root)
	if err := os.WriteFile(path, []byte("not a port\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := listenPort(t, "127.0.0.1:0", root)
	if saved := readListenPort(path); saved != got {
		t.Fatalf("listen.port = %d, want %d", saved, got)
	}
}
