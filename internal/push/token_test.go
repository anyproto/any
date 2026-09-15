package push

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestTokenFile_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), tokenFileName)

	// Missing file → no token, no error.
	tok, err := loadTokenFile(path)
	if err != nil || tok != nil {
		t.Fatalf("missing file: tok=%+v err=%v", tok, err)
	}

	if err := saveTokenFile(path, deviceToken{Platform: "ios", Token: "apns-token-1"}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("token file mode = %v, want 0600", fi.Mode().Perm())
		}
	}

	tok, err = loadTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok == nil || tok.Platform != "ios" || tok.Token != "apns-token-1" {
		t.Errorf("round-trip = %+v", tok)
	}

	// Overwrite (token rotation) keeps the latest.
	if err := saveTokenFile(path, deviceToken{Platform: "android", Token: "fcm-2"}); err != nil {
		t.Fatal(err)
	}
	tok, err = loadTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok == nil || tok.Platform != "android" || tok.Token != "fcm-2" {
		t.Errorf("after rotation = %+v", tok)
	}

	// Remove is idempotent.
	if err := removeTokenFile(path); err != nil {
		t.Fatal(err)
	}
	if err := removeTokenFile(path); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	if tok, err = loadTokenFile(path); err != nil || tok != nil {
		t.Errorf("after remove: tok=%+v err=%v", tok, err)
	}
}

func TestTokenFile_CorruptAndEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), tokenFileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTokenFile(path); err == nil {
		t.Error("corrupt file should error")
	}

	// A file with an empty token reads as "no token".
	if err := saveTokenFile(path, deviceToken{Platform: "ios"}); err != nil {
		t.Fatal(err)
	}
	tok, err := loadTokenFile(path)
	if err != nil || tok != nil {
		t.Errorf("empty token should read as absent: tok=%+v err=%v", tok, err)
	}
}

// TestEnqueue_NeverBlocks pins the fire-and-forget contract: with no
// notify loop draining (service not started), Enqueue must drop on
// overflow rather than block the caller.
func TestEnqueue_NeverBlocks(t *testing.T) {
	s := New(nil, t.TempDir())
	done := make(chan struct{})
	go func() {
		for i := 0; i < notifyQueueCap+16; i++ {
			s.Enqueue("sp", []string{"chats"}, []byte("{}"), "g", false)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second): // generous — the loop is pure channel sends
		t.Fatal("Enqueue blocked on a full queue")
	}
}
