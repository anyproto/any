package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// TestE2E_ManagedLifecycle is the host-owned lifecycle at the binary
// level: a managed server starts unauthorized and announces its
// control token; the host logs in, signs out in place (the listener
// stays up), logs the same account back in on the cached device key,
// and `any stop` finds the account by its held lock — a managed
// account dir carries no wallet — and stops it with a signal.
func TestE2E_ManagedLifecycle(t *testing.T) {
	bin := buildBinary(t)
	dataDir := t.TempDir()
	captureDir := t.TempDir()
	stdoutPath := filepath.Join(captureDir, "stdout.log")
	stderrPath := filepath.Join(captureDir, "stderr.log")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stdoutFile.Close()
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stderrFile.Close()
	nodeconfPath := filepath.Join(captureDir, "nodeconf.yml")
	if err := os.WriteFile(nodeconfPath, config.NodeconfPlaceholder(), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "run", "--mode", "managed", "--addr", "127.0.0.1:0", "--data-dir", dataDir)
	cmd.Env = append(os.Environ(), "ANY_DATA_DIR="+dataDir, "ANY_NETWORK_NODECONF_PATH="+nodeconfPath, "ANY_INDEX_EMBEDDER=none")
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	addr, token := readHandshake(t, stdoutPath, stderrPath, exited)
	waitForReady(t, addr, 10*time.Second)
	base := "http://" + addr

	// Unauthorized: engine routes 401, status reports the mode and bits.
	resp, _ := doRequest(t, http.MethodGet, base+"/v1/spaces", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pre-auth GET /v1/spaces: %d", resp.StatusCode)
	}
	var st api.AuthStatusResponse
	mustJSON(t, http.MethodGet, base+"/v1/auth", "", http.StatusOK, &st)
	if st.Authorized || st.Mode != "managed" || !st.Capabilities.Deauthorize || !st.Capabilities.Shutdown {
		t.Fatalf("managed status: %+v", st)
	}

	// Login needs the token; the reply carries the phrase once.
	if code, body := controlPost(t, addr, "/v1/auth", `{}`, ""); code != http.StatusForbidden {
		t.Fatalf("login without the token: %d %s", code, body)
	}
	code, body := controlPost(t, addr, "/v1/auth", `{}`, token)
	if code != http.StatusOK {
		t.Fatalf("login: %d %s", code, body)
	}
	var login api.AuthResponse
	if err := json.Unmarshal([]byte(body), &login); err != nil {
		t.Fatal(err)
	}
	if login.AccountId == "" || login.Mnemonic == "" || !login.Created {
		t.Fatalf("login reply: %+v", login)
	}
	acctDir := config.AccountDir(dataDir, login.AccountId)
	if _, err := os.Stat(config.WalletPath(config.Config{}, acctDir)); !os.IsNotExist(err) {
		t.Fatalf("managed login must not write a wallet (stat err %v)", err)
	}
	if _, err := os.Stat(config.AddrPath(acctDir)); err != nil {
		t.Fatalf("server.addr not recorded: %v", err)
	}

	// Sign out in place through the CLI, then back in over HTTP.
	out, err := exec.Command(bin, "--addr", addr, "--control-token", token, "auth", "logout").CombinedOutput()
	if err != nil {
		t.Fatalf("any auth logout: %v\n%s", err, out)
	}
	resp, _ = doRequest(t, http.MethodGet, base+"/v1/spaces", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout GET /v1/spaces: %d", resp.StatusCode)
	}
	code, body = controlPost(t, addr, "/v1/auth", `{"mnemonic":`+strconv.Quote(login.Mnemonic)+`}`, token)
	if code != http.StatusOK {
		t.Fatalf("re-login: %d %s", code, body)
	}
	var again api.AuthResponse
	if err := json.Unmarshal([]byte(body), &again); err != nil {
		t.Fatal(err)
	}
	if again.AccountId != login.AccountId || again.Created {
		t.Fatalf("re-login reply: %+v", again)
	}

	// `any stop` resolves the account by its held lock and signals it.
	stopOut, err := anyStop(t, bin, dataDir)
	if err != nil {
		t.Fatalf("any stop: %v\n%s", err, stopOut)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("server exited non-zero after `any stop`: %v\n%s", err, dumpCaptures(stdoutPath, stderrPath))
		}
	case <-time.After(25 * time.Second):
		t.Fatal("server did not exit within 25s of `any stop`")
	}
}
