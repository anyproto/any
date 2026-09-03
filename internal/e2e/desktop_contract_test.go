package e2e

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// Desktop-shell contract (docs/plans/20260611-desktop-shell-server-contract.md,
// docs/02-server.md § Modes): the shell spawns `run --mode managed --addr
// 127.0.0.1:0` with no nodeconf FILE, which must boot from an arbitrary
// working directory — the old CWD-relative ../test-etc/staging.yml read is
// gone — and announce the KERNEL-RESOLVED address as a plain
// `LISTENING <addr>` STDOUT line followed by `CONTROL_TOKEN <hex>`. The
// first line is the shell's port handshake + readiness gate, the second
// the token that gates its auth and graceful-quit calls (POST /v1/auth,
// POST /v1/shutdown). This test is the server-side mirror of that exact
// lifecycle. Stdout and stderr are captured separately on purpose: the
// shell reads stdout only, so an announce migrating to stderr (e.g. into
// the zap logger) must fail here.
func TestDesktopContract_AnnounceEmbeddedNodeconfShutdown(t *testing.T) {
	bin := buildBinary(t)
	dataDir := t.TempDir()
	captureDir := t.TempDir()
	stdoutPath := filepath.Join(captureDir, "stdout.log")
	stderrPath := filepath.Join(captureDir, "stderr.log")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatalf("create stdout capture: %v", err)
	}
	defer stdoutFile.Close()
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("create stderr capture: %v", err)
	}
	defer stderrFile.Close()

	// The network comes from the sanitized placeholder, not the embedded
	// default: that default is the production network, and the contract
	// under test is the boot/announce/shutdown lifecycle, not which peers
	// the server dials. ANY_NETWORK_NODECONF_PATH is the same override a
	// packaged install would use; the CWD still has no ../test-etc sibling,
	// so the old relative-path regression stays covered — it triggers
	// when the SDK boots, which the token-gated POST /v1/auth below does.
	nodeconfPath := filepath.Join(captureDir, "nodeconf.yml")
	if err := os.WriteFile(nodeconfPath, config.NodeconfPlaceholder(), 0o600); err != nil {
		t.Fatalf("write nodeconf placeholder: %v", err)
	}

	cmd := exec.Command(bin, "run", "--mode", "managed", "--addr", "127.0.0.1:0", "--data-dir", dataDir)
	// A CWD that has no ../test-etc sibling — this is what a packaged
	// install looks like, and what used to fail before the embed.
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "ANY_DATA_DIR="+dataDir, "ANY_NETWORK_NODECONF_PATH="+nodeconfPath)
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	// One Wait for the whole test: the announce poll selects on it so a
	// boot crash fails fast with the exit error instead of burning the
	// 30s deadline and reporting a flake-shaped timeout.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	// Poll the captured STDOUT (only) for the handshake lines.
	addr, token := readHandshake(t, stdoutPath, stderrPath, exited)
	if strings.HasSuffix(addr, ":0") {
		t.Fatalf("announce carries the CONFIG addr, not the resolved one: %q", addr)
	}

	waitForReady(t, addr, 10*time.Second)

	// A managed server boots nothing on its own — the shell posts the
	// credential with the token. This is also the SDK boot the nodeconf
	// regression needs.
	if code, body := controlPost(t, addr, "/v1/auth", `{}`, token); code != http.StatusOK {
		t.Fatalf("POST /v1/auth with the token: %d %s", code, body)
	}

	// The shell's graceful-quit path: token-gated POST /v1/shutdown →
	// clean exit. Without the token nothing happens.
	if code, body := controlPost(t, addr, "/v1/shutdown", "", ""); code != http.StatusForbidden {
		t.Fatalf("POST /v1/shutdown without the token: %d %s, want 403", code, body)
	}
	if code, body := controlPost(t, addr, "/v1/shutdown", "", token); code != http.StatusNoContent {
		t.Fatalf("POST /v1/shutdown with the token: %d %s, want 204", code, body)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("server exited non-zero after graceful shutdown: %v\n%s", err, dumpCaptures(stdoutPath, stderrPath))
		}
	case <-time.After(25 * time.Second):
		t.Fatal("server did not exit within 25s of POST /v1/shutdown")
	}
}

// readHandshake polls the captured stdout for the `LISTENING <addr>`
// and `CONTROL_TOKEN <hex>` lines a managed server prints, failing
// fast if the process exits first.
func readHandshake(t *testing.T, stdoutPath, stderrPath string, exited <-chan error) (addr, token string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(stdoutPath)
		for line := range strings.SplitSeq(string(raw), "\n") {
			if rest, ok := strings.CutPrefix(line, "LISTENING "); ok {
				addr = strings.TrimSpace(rest)
			}
			if rest, ok := strings.CutPrefix(line, "CONTROL_TOKEN "); ok {
				token = strings.TrimSpace(rest)
			}
		}
		if addr != "" && token != "" {
			return addr, token
		}
		select {
		case err := <-exited:
			t.Fatalf("server exited before announcing: %v\n%s", err, dumpCaptures(stdoutPath, stderrPath))
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("no LISTENING + CONTROL_TOKEN lines on stdout within 30s\n%s", dumpCaptures(stdoutPath, stderrPath))
	return "", ""
}

// controlPost issues a POST with an optional JSON body and control
// token, returning the status and body.
func controlPost(t *testing.T, addr, path, body, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set(api.ControlTokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// dumpCaptures renders both capture files for a failure message; a read
// error is reported in place rather than silently yielding an empty dump.
func dumpCaptures(stdoutPath, stderrPath string) string {
	read := func(path string) string {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("<read %s: %v>", filepath.Base(path), err)
		}
		return string(raw)
	}
	return "stdout:\n" + read(stdoutPath) + "\nstderr:\n" + read(stderrPath)
}
