//go:build unix

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/auth"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerP2PFileHolderGone proves the download contract when
// the only holder of a not-durable file goes away: B's reads answer
// 409 file.not_available, never a 2xx with an empty body, while the
// bytes B already holds stay readable; once the holder is back,
// restarted or resumed from a freeze, B downloads the file within the
// peer backoff. Two offline devices of one account pair over LAN p2p.
// Gated like TestE2E_MultipeerP2PColdRestore.
func TestE2E_MultipeerP2PFileHolderGone(t *testing.T) {
	if os.Getenv("ANY_E2E_P2P") == "" {
		t.Skip("set ANY_E2E_P2P=1 to run the real-mDNS multipeer test")
	}
	if testing.Short() {
		t.Skip("multipeer test takes minutes; rerun without -short")
	}

	nodeconfPath := writeDeadNodeconf(t)
	mnemonic, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	bin := buildBinary(t)
	devA := startPeerOffline(t, bin, "devA", nodeconfPath, mnemonic)
	defer devA.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, devA.base+"/v1/spaces",
		`{"name":"file-holder-gone"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, devA.base+"/v1/spaces/"+sp.Id+"/objects",
		`{"type":"page"}`, http.StatusCreated, &obj)

	// Offline, A's attach leaves A the only holder of both files.
	const size = 3_000_000
	content1, content2 := benchPayload(size, 1), benchPayload(size, 2)
	attachBase := devA.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId + "/files"
	f1 := attachFile(t, attachBase+"?name=one.bin", "application/octet-stream", content1)
	f2 := attachFile(t, attachBase+"?name=two.bin", "application/octet-stream", content2)

	var pa api.P2PStatusResponse
	mustJSON(t, http.MethodGet, devA.base+"/v1/debug/p2p", "", http.StatusOK, &pa)

	devB := startPeerOffline(t, bin, "devB", nodeconfPath, mnemonic)
	defer devB.stop(t)

	// B converges both rows: not durable, nothing local. A cold restore
	// over the LAN alone can take two minutes.
	converge := time.Now()
	filesB := devB.base + "/v1/spaces/" + sp.Id + "/files"
	for _, f := range []api.FileInfo{f1, f2} {
		var got api.FileInfo
		if !pollUntil(180*time.Second, func() bool {
			resp, raw, err := tryRequest(http.MethodGet, filesB+"/"+f.FileId, "")
			return err == nil && resp.StatusCode == http.StatusOK && json.Unmarshal(raw, &got) == nil
		}) {
			t.Fatalf("B never got the row of %s", f.Name)
		}
		if got.Durable || got.Cached || got.Size != size {
			t.Fatalf("B's row of %s = %+v, want durable:false cached:false size:%d", f.Name, got, size)
		}
	}
	t.Logf("B got both rows %s after B started", time.Since(converge).Round(time.Millisecond))

	// A read before the two pair for this space may 409 legitimately. A
	// serves the file only to a peer it lists with the space, so B's
	// view of A is not enough.
	var pb api.P2PStatusResponse
	mustJSON(t, http.MethodGet, devB.base+"/v1/debug/p2p", "", http.StatusOK, &pb)
	if !pollUntil(90*time.Second, func() bool {
		return p2pPeerHasSpace(t, devB, pa.PeerId, sp.Id) && p2pPeerHasSpace(t, devA, pb.PeerId, sp.Id)
	}) {
		t.Fatalf("A (%s) and B (%s) never listed each other with the space over p2p", pa.PeerId, pb.PeerId)
	}
	t.Logf("B paired with A for the space %s after B started", time.Since(converge).Round(time.Millisecond))

	url1 := filesB + "/" + f1.FileId + "/content"
	url2 := filesB + "/" + f2.FileId + "/content"
	tailRange := fmt.Sprintf("bytes %d-%d/%d", size-1024, size-1, size)
	expectContent(t, url1, "bytes=-1024", http.StatusPartialContent, content1[size-1024:], tailRange)

	devA.stop(t)

	// The first byte asked for is neither local nor fetchable.
	e := expectNotAvailable(t, url1, "")
	t.Logf("B, file 1 with A stopped: 409 %q", e.Message)
	expectNotAvailable(t, url1, "bytes=0-99")
	// The tail B read is local and still served.
	expectContent(t, url1, "bytes=-1024", http.StatusPartialContent, content1[size-1024:], tailRange)
	e = expectNotAvailable(t, url2, "")
	t.Logf("B, file 2 with A stopped: 409 %q", e.Message)

	// A comes back as the same device, and B gets both files from it.
	restartPeer(t, bin, devA)
	start := time.Now()
	var again api.P2PStatusResponse
	mustJSON(t, http.MethodGet, devA.base+"/v1/debug/p2p", "", http.StatusOK, &again)
	if again.PeerId != pa.PeerId {
		t.Fatalf("restarted A has peer id %s, want %s", again.PeerId, pa.PeerId)
	}
	paired := time.Duration(-1)
	if !pollUntil(2*time.Minute, func() bool {
		if paired < 0 && p2pPeerHasSpace(t, devB, pa.PeerId, sp.Id) {
			paired = time.Since(start)
		}
		resp, body, err := getContent(url1, "")
		return err == nil && resp.StatusCode == http.StatusOK && bytes.Equal(body, content1)
	}) {
		t.Fatalf("B did not download file 1 within 2m of A's restart (B saw A with the space after %s)", paired)
	}
	t.Logf("B downloaded file 1 %s after A restarted (B saw A with the space after %s)", time.Since(start).Round(time.Millisecond), paired)
	expectContent(t, url1, "", http.StatusOK, content1, "")

	if !pollUntil(time.Minute, func() bool {
		resp, body, err := getContent(url2, "")
		return err == nil && resp.StatusCode == http.StatusOK && bytes.Equal(body, content2)
	}) {
		t.Fatalf("B did not download file 2 within 1m of downloading file 1")
	}
	t.Logf("B downloaded file 2 %s after A restarted", time.Since(start).Round(time.Millisecond))

	// A frozen stays on B's LAN list, so B's failed check backs A off:
	// once A resumes, B asks again within the backoff, not minutes later.
	content3 := benchPayload(size, 3)
	f3 := attachFile(t, attachBase+"?name=three.bin", "application/octet-stream", content3)
	if !pollUntil(60*time.Second, func() bool {
		resp, _, err := tryRequest(http.MethodGet, filesB+"/"+f3.FileId, "")
		return err == nil && resp.StatusCode == http.StatusOK
	}) {
		t.Fatalf("B never got the row of %s", f3.Name)
	}
	url3 := filesB + "/" + f3.FileId + "/content"
	if err := devA.srv.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("freeze A: %v", err)
	}
	e = expectNotAvailable(t, url3, "")
	t.Logf("B, file 3 with A frozen: 409 %q", e.Message)
	if err := devA.srv.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatalf("resume A: %v", err)
	}
	start = time.Now()
	if !pollUntil(90*time.Second, func() bool {
		resp, body, err := getContent(url3, "")
		return err == nil && resp.StatusCode == http.StatusOK && bytes.Equal(body, content3)
	}) {
		t.Fatalf("B did not download file 3 within 90s of A resuming")
	}
	t.Logf("B downloaded file 3 %s after A resumed", time.Since(start).Round(time.Millisecond))
}

// p2pPeerHasSpace reports whether p lists peerId as connected with spaceId.
func p2pPeerHasSpace(t *testing.T, p *peer, peerId, spaceId string) bool {
	t.Helper()
	var st api.P2PStatusResponse
	mustJSON(t, http.MethodGet, p.base+"/v1/debug/p2p", "", http.StatusOK, &st)
	for _, pp := range st.Peers {
		if pp.PeerId == peerId && pp.Connected && slices.Contains(pp.SpaceIds, spaceId) {
			return true
		}
	}
	return false
}

// restartPeer boots p again on its own data dir and config, so it comes
// back as the same device, and waits until its account is booted.
func restartPeer(t *testing.T, bin string, p *peer) {
	t.Helper()
	p.srv = execServer(t, bin, filepath.Join(p.dataDir, "config.yaml"), p.dataDir, p.addr)
	waitForReady(t, p.addr, 60*time.Second)
	var st api.AuthStatusResponse
	if !pollUntil(60*time.Second, func() bool {
		mustJSON(t, http.MethodGet, p.base+"/v1/auth", "", http.StatusOK, &st)
		return st.Authorized
	}) {
		t.Fatalf("peer %s: account not booted after restart: %+v", p.name, st)
	}
}

// getContent GETs url and reads the whole body. err is the transport or
// body error: a body cut short surfaces as io.ErrUnexpectedEOF.
func getContent(url, rangeHeader string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := e2eClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, err = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes(), err
}

// expectContent asserts the status, the whole body and, when set, the
// Content-Range of a content read.
func expectContent(t *testing.T, url, rangeHeader string, status int, want []byte, contentRange string) {
	t.Helper()
	resp, body, err := getContent(url, rangeHeader)
	if resp == nil {
		t.Fatalf("GET %s (Range %q): %v", url, rangeHeader, err)
	}
	if resp.StatusCode != status {
		t.Fatalf("GET %s (Range %q): status=%d want=%d body=%.300s", url, rangeHeader, resp.StatusCode, status, body)
	}
	if err != nil {
		t.Fatalf("GET %s (Range %q): status %d, Content-Length %s, body cut after %d bytes: %v",
			url, rangeHeader, resp.StatusCode, resp.Header.Get("Content-Length"), len(body), err)
	}
	if got := resp.Header.Get("Content-Range"); got != contentRange {
		t.Fatalf("GET %s (Range %q): Content-Range=%q want %q", url, rangeHeader, got, contentRange)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("GET %s (Range %q): %d body bytes, want %d matching bytes", url, rangeHeader, len(body), len(want))
	}
}

// expectNotAvailable asserts a complete 409 file.not_available envelope
// with none of the headers that describe file bytes.
func expectNotAvailable(t *testing.T, url, rangeHeader string) api.APIError {
	t.Helper()
	resp, body, err := getContent(url, rangeHeader)
	if resp == nil {
		t.Fatalf("GET %s (Range %q): %v", url, rangeHeader, err)
	}
	if resp.StatusCode != http.StatusConflict || err != nil {
		t.Fatalf("GET %s (Range %q): status=%d Content-Length=%s, %d body bytes (read err: %v), want a 409 envelope",
			url, rangeHeader, resp.StatusCode, resp.Header.Get("Content-Length"), len(body), err)
	}
	for _, h := range []string{"Content-Range", "Content-Disposition"} {
		if v := resp.Header.Get(h); v != "" {
			t.Fatalf("GET %s (Range %q): 409 carries %s: %s", url, rangeHeader, h, v)
		}
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("GET %s (Range %q): 409 Content-Type=%q", url, rangeHeader, ct)
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("GET %s (Range %q): 409 body is not an envelope: %v (%.300s)", url, rangeHeader, err, body)
	}
	if env.Error.Code != api.ErrFileNotAvailable {
		t.Fatalf("GET %s (Range %q): code=%q want %q", url, rangeHeader, env.Error.Code, api.ErrFileNotAvailable)
	}
	return env.Error
}
