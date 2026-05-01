// Package e2e exercises the compiled `any` binary against the staging
// network: it builds cmd/any, runs `any run` against a temp data dir,
// and drives every shipped endpoint (real and 501) over real HTTP.
//
// The test is gated on the same staging.yml fixture the SDK uses for
// its own integration tests; it skips silently when the file is absent.
// Set ANY_E2E_KEEP=1 to keep the temp data dir on success for poking.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

const stagingFixture = "../../../test-etc/staging.yml"

// absStagingPath returns the absolute path to the staging fixture so a
// spawned `any` binary (which has a different CWD than this test) can
// locate it. The default DefaultNodeconfPath is CWD-relative; surfacing
// it explicitly via a config.yaml is what an out-of-tree caller would
// also have to do.
func absStagingPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(stagingFixture)
	if err != nil {
		t.Fatalf("resolve staging path: %v", err)
	}
	return abs
}

// TestE2E_FullFlow boots the binary, drives every implemented endpoint
// plus a representative 501, and shuts down via POST /v1/shutdown.
func TestE2E_FullFlow(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	t.Logf("data dir: %s", dataDir)
	t.Logf("listen:   %s", addr)

	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)

	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr

	t.Run("GET /v1/health", func(t *testing.T) {
		var h map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/health", "", http.StatusOK, &h)
		if h["status"] != "ok" {
			t.Errorf("status = %v, want ok", h["status"])
		}
		if h["account"] == nil || h["account"] == "" {
			t.Errorf("account empty: %+v", h)
		}
	})

	t.Run("GET /v1/account", func(t *testing.T) {
		var acc map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/account", "", http.StatusOK, &acc)
		if acc["id"] == nil || acc["id"] == "" {
			t.Errorf("id empty: %+v", acc)
		}
		// Metadata is reserved (omitempty) until the SDK wires it.
		if _, ok := acc["metadata"]; ok {
			t.Errorf("metadata should be omitted, got %+v", acc)
		}
	})

	t.Run("GET /v1/spaces empty", func(t *testing.T) {
		var list map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces", "", http.StatusOK, &list)
		spaces, _ := list["spaces"].([]any)
		if len(spaces) != 0 {
			t.Errorf("expected empty list, got %+v", spaces)
		}
	})

	var spaceID string
	t.Run("POST /v1/spaces creates", func(t *testing.T) {
		var created map[string]any
		body := `{"name":"E2E","description":"e2e-smoke"}`
		mustJSON(t, http.MethodPost, base+"/v1/spaces", body, http.StatusCreated, &created)
		id, _ := created["id"].(string)
		if id == "" {
			t.Fatalf("no id in %+v", created)
		}
		spaceID = id
		if got := created["name"]; got != "E2E" {
			t.Errorf("name = %v, want E2E", got)
		}
		if got := created["status"]; got != "active" {
			t.Errorf("status = %v, want active", got)
		}
	})

	if spaceID == "" {
		t.Fatal("no spaceID — skipping the rest")
	}

	t.Run("types + properties demo flow", func(t *testing.T) {
		// Mirrors any-sync-sdk2/sdk_test.go: TestSDK_TypesAndProperties
		// against the real binary over loopback HTTP.
		var typeResp map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/types",
			`{"name":"Movie","description":"A film"}`,
			http.StatusCreated, &typeResp)
		typeID, _ := typeResp["typeId"].(string)
		if typeID == "" {
			t.Fatalf("typeId empty: %+v", typeResp)
		}

		var propResp map[string]any
		mustJSON(t, http.MethodPost,
			base+"/v1/spaces/"+spaceID+"/types/"+typeID+"/properties",
			`{"name":"Title","kind":"string","xKey":"title"}`,
			http.StatusCreated, &propResp)
		propID, _ := propResp["propId"].(string)
		if propID == "" {
			t.Fatalf("propId empty: %+v", propResp)
		}

		var objResp map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/objects",
			fmt.Sprintf(`{"types":[%q]}`, typeID),
			http.StatusCreated, &objResp)
		objectID, _ := objResp["objectId"].(string)
		if objectID == "" {
			t.Fatalf("objectId empty: %+v", objResp)
		}

		var modify map[string]any
		mustJSON(t, http.MethodPost,
			base+"/v1/spaces/"+spaceID+"/properties/"+objectID+"/base/"+typeID,
			fmt.Sprintf(`{"patch":{%q:"Casablanca"}}`, propID),
			http.StatusOK, &modify)
		if modify["versionId"] == "" || modify["changeId"] == "" {
			t.Errorf("modify result incomplete: %+v", modify)
		}

		var got map[string]any
		mustJSON(t, http.MethodGet,
			base+"/v1/spaces/"+spaceID+"/properties/"+objectID, "",
			http.StatusOK, &got)
		record, _ := got["record"].(map[string]any)
		typeNode, _ := record[typeID].(map[string]any)
		if typeNode[propID] != "Casablanca" {
			t.Errorf("record[%s][%s] = %v, want Casablanca; full=%+v",
				typeID, propID, typeNode[propID], record)
		}
	})

	t.Run("GET /v1/spaces/:id", func(t *testing.T) {
		var got map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+spaceID, "", http.StatusOK, &got)
		if got["id"] != spaceID {
			t.Errorf("id mismatch: got %v want %v", got["id"], spaceID)
		}
	})

	t.Run("GET /v1/spaces lists newly-created", func(t *testing.T) {
		var list map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces", "", http.StatusOK, &list)
		spaces, _ := list["spaces"].([]any)
		if len(spaces) != 1 {
			t.Fatalf("want 1 space, got %d: %+v", len(spaces), spaces)
		}
	})

	t.Run("DELETE /v1/spaces/:id soft-deletes", func(t *testing.T) {
		mustStatus(t, http.MethodDelete, base+"/v1/spaces/"+spaceID, "", http.StatusNoContent)

		var list map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces", "", http.StatusOK, &list)
		spaces, _ := list["spaces"].([]any)
		if len(spaces) != 1 {
			t.Fatalf("want 1 (deleted) row in list, got %d", len(spaces))
		}
		entry, _ := spaces[0].(map[string]any)
		if entry["status"] != "deleted" {
			t.Errorf("status = %v, want deleted", entry["status"])
		}
	})

	t.Run("501 routes", func(t *testing.T) {
		cases := []struct{ method, path string }{
			{http.MethodPut, "/v1/account/metadata"},
			{http.MethodPost, "/v1/spaces/join"},
			{http.MethodPost, "/v1/spaces/derive"},
			{http.MethodPost, "/v1/spaces/one-to-one"},
			{http.MethodDelete, "/v1/spaces/" + spaceID + "/types/t1"},
			{http.MethodDelete, "/v1/spaces/" + spaceID + "/types/t1/properties/p1"},
			{http.MethodPatch, "/v1/spaces/" + spaceID + "/types/t1/properties/p1"},
			{http.MethodPost, "/v1/spaces/" + spaceID + "/properties/o1/account/t1"},
			{http.MethodGet, "/v1/spaces/" + spaceID + "/members"},
			{http.MethodPost, "/v1/spaces/" + spaceID + "/acl/invite"},
			{http.MethodGet, "/v1/spaces/" + spaceID + "/sync-status"},
		}
		for _, tc := range cases {
			var env map[string]any
			body := "{}"
			if tc.method == http.MethodGet {
				body = ""
			}
			mustJSON(t, tc.method, base+tc.path, body, http.StatusNotImplemented, &env)
			errObj, _ := env["error"].(map[string]any)
			if errObj["code"] != "sdk.not_implemented" {
				t.Errorf("%s %s: code = %v, want sdk.not_implemented", tc.method, tc.path, errObj["code"])
			}
		}
	})

	t.Run("POST /v1/shutdown", func(t *testing.T) {
		mustStatus(t, http.MethodPost, base+"/v1/shutdown", "", http.StatusNoContent)
	})

	if err := srv.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server didn't exit cleanly: %v\n%s", err, srv.output())
	}
	if srv.exitErr != nil && !errors.Is(srv.exitErr, &exec.ExitError{}) {
		t.Logf("server exit: %v", srv.exitErr)
	}

	if os.Getenv("ANY_E2E_KEEP") == "" {
		_ = os.RemoveAll(dataDir)
	}
}

// TestE2E_AnyStatus exercises the CLI subcommand path against the
// running server. Confirms the CLI talks JSON to the same endpoint a
// raw HTTP client does.
func TestE2E_AnyStatus(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	out, err := exec.Command(bin, "--addr", addr, "status").CombinedOutput()
	if err != nil {
		t.Fatalf("any status: %v\n%s", err, out)
	}
	var h map[string]any
	if err := json.Unmarshal(out, &h); err != nil {
		t.Fatalf("decode any-status output: %v\nraw:\n%s", err, out)
	}
	if h["status"] != "ok" {
		t.Errorf("status field = %v", h["status"])
	}

	// And `any stop` triggers shutdown.
	stopOut, err := exec.Command(bin, "--addr", addr, "stop").CombinedOutput()
	if err != nil {
		t.Fatalf("any stop: %v\n%s", err, stopOut)
	}
	if err := srv.waitExit(15 * time.Second); err != nil {
		t.Fatalf("server didn't exit after `any stop`: %v\n%s", err, srv.output())
	}
}

// --- helpers --------------------------------------------------------------

type runningServer struct {
	cmd     *exec.Cmd
	out     *bytes.Buffer
	done    chan struct{}
	exitErr error
}

func startServer(t *testing.T, bin, addr, dataDir string) *runningServer {
	t.Helper()
	// Drop a config.yaml that pins network.nodeconfPath to an absolute
	// path. The binary's compile-time default is CWD-relative, which
	// won't resolve when the test exec's it from a different working
	// directory.
	cfgPath := filepath.Join(dataDir, "config.yaml")
	cfgBody := fmt.Sprintf("network:\n  nodeconfPath: %s\n", absStagingPath(t))
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(bin, "run", "--config", cfgPath, "--data-dir", dataDir, "--addr", addr)
	// Inherit env but force ANY_DATA_DIR so the binary never picks up the
	// developer's home dir.
	cmd.Env = append(os.Environ(), "ANY_DATA_DIR="+dataDir)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	rs := &runningServer{cmd: cmd, out: &buf, done: make(chan struct{})}
	go func() {
		rs.exitErr = cmd.Wait()
		close(rs.done)
	}()
	return rs
}

func (s *runningServer) stop(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
		return
	default:
	}
	if runtime.GOOS == "windows" {
		_ = s.cmd.Process.Kill()
	} else {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-s.done:
	case <-time.After(15 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.done
	}
	if t.Failed() {
		t.Logf("server output:\n%s", s.out.String())
	}
}

func (s *runningServer) waitExit(d time.Duration) error {
	select {
	case <-s.done:
		return nil
	case <-time.After(d):
		return fmt.Errorf("timed out waiting %s for exit", d)
	}
}

func (s *runningServer) output() string { return s.out.String() }

func waitForReady(t *testing.T, addr string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			// also wait for /v1/health to actually 200 — the listener may
			// be up before the SDK finishes booting.
			r, err := http.Get("http://" + addr + "/v1/health")
			if err == nil {
				r.Body.Close()
				if r.StatusCode == http.StatusOK {
					return
				}
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("server at %s never became ready", addr)
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "any")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "github.com/anyproto/any/cmd/any")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	return out
}

func mustJSON(t *testing.T, method, url, body string, wantStatus int, out any) {
	t.Helper()
	resp, raw := doRequest(t, method, url, body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, url, resp.StatusCode, wantStatus, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode: %v body=%s", method, url, err, raw)
		}
	}
}

func mustStatus(t *testing.T, method, url, body string, wantStatus int) {
	t.Helper()
	resp, raw := doRequest(t, method, url, body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, url, resp.StatusCode, wantStatus, raw)
	}
}

func doRequest(t *testing.T, method, url, body string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}
