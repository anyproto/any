package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anyproto/any/internal/config"
)

// pinNodeconf reports the configured conf's networkId and pins its bytes,
// so an engine booted after the file changes still joins that network.
func TestPinNodeconf(t *testing.T) {
	raw := config.NodeconfPlaceholder()
	want, err := config.NodeconfNetworkId(raw)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "nodeconf.yml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Network.NodeconfPath = path

	got, err := pinNodeconf(&cfg)
	if err != nil || got != want {
		t.Fatalf("pinNodeconf = %q, %v; want %q", got, err, want)
	}
	if err := os.WriteFile(path, []byte("networkId: other"), 0o600); err != nil {
		t.Fatal(err)
	}
	booted, err := config.LoadNodeconf(cfg.Network)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := config.NodeconfNetworkId(booted); id != want {
		t.Errorf("engine would boot %q after the file changed, want the pinned %q", id, want)
	}
}

// A conf naming no network fails startup instead of serving an empty id.
func TestPinNodeconfRejectsNoNetworkId(t *testing.T) {
	cfg := config.Defaults()
	cfg.Network.Nodeconf = "nodes: []"
	if _, err := pinNodeconf(&cfg); err == nil {
		t.Fatal("want an error for a nodeconf without networkId")
	}
}
