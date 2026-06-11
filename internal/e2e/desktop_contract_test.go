package e2e

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Desktop-shell contract (docs/plans/20260611-desktop-shell-server-contract.md):
// `--addr 127.0.0.1:0` with NO nodeconf config must boot from an arbitrary
// working directory (the embedded staging fallback — the old CWD-relative
// ../test-etc/staging.yml read is gone) and announce the KERNEL-RESOLVED
// address as a plain `LISTENING <addr>` stdout line. That line is the
// any-ui desktop shell's port handshake + readiness gate; POST /v1/shutdown
// is its graceful-quit path. This test is the server-side mirror of that
// exact lifecycle.
func TestDesktopContract_AnnounceEmbeddedNodeconfShutdown(t *testing.T) {
	bin := buildBinary(t)
	dataDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "stdout.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	defer logFile.Close()

	cmd := exec.Command(bin, "run", "--addr", "127.0.0.1:0", "--data-dir", dataDir)
	// A CWD that has no ../test-etc sibling — this is what a packaged
	// install looks like, and what used to fail before the embed.
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "ANY_DATA_DIR="+dataDir)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	// Poll the captured stdout for the announce line.
	var addr string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && addr == "" {
		raw, _ := os.ReadFile(logPath)
		for line := range strings.SplitSeq(string(raw), "\n") {
			if rest, ok := strings.CutPrefix(line, "LISTENING "); ok {
				addr = strings.TrimSpace(rest)
				break
			}
		}
		if addr == "" {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if addr == "" {
		raw, _ := os.ReadFile(logPath)
		t.Fatalf("no LISTENING line on stdout within 30s; output:\n%s", string(raw))
	}
	if strings.HasSuffix(addr, ":0") {
		t.Fatalf("announce carries the CONFIG addr, not the resolved one: %q", addr)
	}

	waitForReady(t, addr, 10*time.Second)

	// The shell's graceful-quit path: POST /v1/shutdown → clean exit.
	resp, err := http.Post("http://"+addr+"/v1/shutdown", "", nil)
	if err != nil {
		t.Fatalf("POST /v1/shutdown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("shutdown status = %d, want 204", resp.StatusCode)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server exited non-zero after graceful shutdown: %v", err)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("server did not exit within 25s of POST /v1/shutdown")
	}
}
