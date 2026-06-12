package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func waitDownload(t *testing.T, d *modelDownload) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d.Status() == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("download did not finish: %v", d.Status())
}

func TestModelDownload_HappyPath(t *testing.T) {
	body := []byte(strings.Repeat("gguf-bytes ", 1000))
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	d := startModelDownload(srv.URL, dest, hex.EncodeToString(sum[:]), srv.Client())
	defer d.Close()
	waitDownload(t, d)

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Error("downloaded content differs")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error(".part should be renamed away")
	}
}

func TestModelDownload_ShaMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not the expected bytes"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "m.gguf")
	d := startModelDownload(srv.URL, dest, strings.Repeat("ab", 32), srv.Client())
	defer d.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := d.Status(); err != nil && strings.Contains(err.Error(), "sha256 mismatch") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := d.Status(); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("want sha256 mismatch error, got %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("dest must not exist on mismatch")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error(".part must be removed on mismatch")
	}
}

func TestModelDownload_Resume(t *testing.T) {
	body := []byte(strings.Repeat("0123456789", 500))
	sum := sha256.Sum256(body)
	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		if gotRange == "" {
			w.Write(body)
			return
		}
		off, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(gotRange, "bytes="), "-"), 10, 64)
		if err != nil {
			t.Errorf("bad range %q", gotRange)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(off, 10)+"-")
		w.WriteHeader(http.StatusPartialContent)
		w.Write(body[off:])
	}))
	defer srv.Close()

	// Pre-seed half the body as an interrupted .part.
	dest := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(dest+".part", body[:2500], 0o644); err != nil {
		t.Fatal(err)
	}

	d := startModelDownload(srv.URL, dest, hex.EncodeToString(sum[:]), srv.Client())
	defer d.Close()
	waitDownload(t, d)

	if gotRange != "bytes=2500-" {
		t.Errorf("want Range: bytes=2500-, got %q", gotRange)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Error("resumed content differs")
	}
}

func TestModelDownload_StatusProgress(t *testing.T) {
	d := &modelDownload{got: 256 << 20, total: 640 << 20}
	err := d.Status()
	if err == nil {
		t.Fatal("in-flight download must report a status error")
	}
	if !strings.Contains(err.Error(), "40%") || !strings.Contains(err.Error(), "256/640 MB") {
		t.Errorf("unexpected progress string: %v", err)
	}
	d.done = true
	if d.Status() != nil {
		t.Error("done download must report nil")
	}
}
