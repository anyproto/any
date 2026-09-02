package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLoadOrCreateDeviceKey pins the cache contract: minted once
// (0600, in a dir created on demand), the same bytes on every later
// read, and a corrupt or foreign file refused rather than silently
// re-minted — a re-mint would fork this install's peerId.
func TestLoadOrCreateDeviceKey(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "Aacct")

	k1, created, err := loadOrCreateDeviceKey(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !created || len(k1) == 0 {
		t.Fatalf("first call: created=%v len=%d", created, len(k1))
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(deviceKeyPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("device.key mode = %o, want 600", fi.Mode().Perm())
		}
	}

	k2, created, err := loadOrCreateDeviceKey(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if created || !bytes.Equal(k1, k2) {
		t.Fatalf("second call: created=%v equal=%v", created, bytes.Equal(k1, k2))
	}

	for name, body := range map[string]string{
		"corrupt":  "{",
		"version":  `{"version":2,"deviceKey":"AAAA"}`,
		"emptykey": `{"version":1,"deviceKey":""}`,
	} {
		if err := os.WriteFile(deviceKeyPath(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadOrCreateDeviceKey(ctx, dir); err == nil {
			t.Errorf("%s device.key must be refused, not re-minted", name)
		}
	}
}
