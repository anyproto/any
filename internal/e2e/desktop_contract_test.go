package e2e

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/config"
)

// Desktop-shell contract (docs/plans/20260611-desktop-shell-server-contract.md):
// `--addr 127.0.0.1:0` with no nodeconf FILE must boot from an arbitrary
// working directory — the old CWD-relative ../test-etc/staging.yml read is
// gone — and announce the KERNEL-RESOLVED address as a plain
// `LISTENING <addr>` STDOUT line. That line is the
// any-ui desktop shell's port handshake + readiness gate; POST /v1/shutdown
// is its graceful-quit path. This test is the server-side mirror of that
// exact lifecycle. Stdout and stderr are captured separately on purpose:
// the shell reads stdout only, so the announce migrating to stderr (e.g.
// into the zap logger) must fail here.
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

	// The nodeconf regression only triggers when the SDK boots, which needs
	// an account — init one (no nodeconf required for init).
	//
	// The network comes from the sanitized placeholder, not the embedded
	// default: that default is the production network, and the contract
	// under test is the boot/announce/shutdown lifecycle, not which peers
	// the server dials. ANY_NETWORK_NODECONF_PATH is the same override a
	// packaged install would use; the CWD still has no ../test-etc sibling,
	// so the old relative-path regression stays covered.
	nodeconfPath := filepath.Join(captureDir, "nodeconf.yml")
	if err := os.WriteFile(nodeconfPath, config.NodeconfPlaceholder(), 0o600); err != nil {
		t.Fatalf("write nodeconf placeholder: %v", err)
	}
	initCmd := exec.Command(bin, "init", "--data-dir", dataDir)
	initCmd.Env = append(os.Environ(), "ANY_DATA_DIR="+dataDir)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("any init: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "run", "--addr", "127.0.0.1:0", "--data-dir", dataDir)
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

	// Poll the captured STDOUT (only) for the announce line.
	var addr string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && addr == "" {
		raw, _ := os.ReadFile(stdoutPath)
		for line := range strings.SplitSeq(string(raw), "\n") {
			if rest, ok := strings.CutPrefix(line, "LISTENING "); ok {
				addr = strings.TrimSpace(rest)
				break
			}
		}
		if addr != "" {
			break
		}
		select {
		case err := <-exited:
			t.Fatalf("server exited before announcing: %v\n%s", err, dumpCaptures(stdoutPath, stderrPath))
		case <-time.After(100 * time.Millisecond):
		}
	}
	if addr == "" {
		t.Fatalf("no LISTENING line on stdout within 30s\n%s", dumpCaptures(stdoutPath, stderrPath))
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
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("server exited non-zero after graceful shutdown: %v\n%s", err, dumpCaptures(stdoutPath, stderrPath))
		}
	case <-time.After(25 * time.Second):
		t.Fatal("server did not exit within 25s of POST /v1/shutdown")
	}
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
