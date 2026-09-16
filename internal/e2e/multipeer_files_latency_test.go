// Alice→Bob file latency benchmark. Two `any` peers share a space over
// a LOCAL any-sync network whose fileV2 broker is backed by a real
// object store (Google Cloud Storage / S3); Alice attaches files of
// several sizes and the test measures, per file, how long until:
//
//   - attach       — Alice's POST returns (includes the SDK's
//     best-effort synchronous backup try);
//   - durable      — Alice records the broker's custody receipt
//     (encrypt → CAR → PUT to the object store → sign);
//   - row @ bob    — Bob's replica materializes the payload row (pure
//     CRDT sync latency; timestamped via Bob's
//     files/query/subscribe SSE stream, not polling);
//   - content @ bob — Bob completes a byte-identical download (public
//     read from the object store; the headline
//     "Alice sent → Bob has the file" number);
//   - re-download  — Bob's second download (bytes now local) for
//     loopback throughput reference.
//
// The measured runs are STEADY-STATE: a warm-up attach first creates
// the per-object payloads tree, resolves the broker's public-read base
// URL, and primes connections, so one-time setup cost doesn't pollute
// the numbers. Sync is left to the reactive path — no forced SyncHeads
// rounds — because the point is the latency a real client sees.
//
// Gating: needs a local-infra nodeconf WITH fileV2 nodes at the repo
// root as `local.yml` (gitignored, like staging.yml), or pointed to by
// ANY_E2E_FILES_NODECONF. Skips otherwise. File sizes are overridable
// via ANY_E2E_FILES_SIZES (comma-separated byte counts).
//
// Run:
//
//	ANY_E2E_FILES_NODECONF=/path/to/local.yml \
//	  go test ./internal/e2e/ -run TestE2E_MultipeerFileLatency -v
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// filesLatencyNodeconf resolves the local-infra nodeconf the benchmark
// runs against and skips the test when it is absent or has no fileV2
// nodes (without a broker Bob can never fetch non-inline content, so
// the measurement would be meaningless).
func filesLatencyNodeconf(t *testing.T) string {
	t.Helper()
	path := os.Getenv("ANY_E2E_FILES_NODECONF")
	if path == "" {
		path = "../../local.yml"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve nodeconf path: %v", err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Skipf("local-infra nodeconf not found at %s (set ANY_E2E_FILES_NODECONF): %v", abs, err)
	}
	if !strings.Contains(string(raw), "fileV2") {
		t.Skipf("nodeconf %s has no fileV2 nodes — file backup/download can't run", abs)
	}
	return abs
}

// latencySizes returns the payload sizes to measure. Default: 2 KB
// (inline tier — pure CRDT, no object store), 1 MB, 10 MB.
func latencySizes(t *testing.T) []int {
	raw := os.Getenv("ANY_E2E_FILES_SIZES")
	if raw == "" {
		return []int{2 << 10, 1 << 20, 10 << 20}
	}
	var out []int
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n <= 0 {
			t.Fatalf("ANY_E2E_FILES_SIZES: bad size %q", part)
		}
		out = append(out, n)
	}
	return out
}

// latencyHTTP is a timeout-less client for the benchmark's uploads and
// download polls — a 10MB transfer must not race e2eClient's request
// timeout, and error-path polls are cheap.
var latencyHTTP = &http.Client{}

type latencyResult struct {
	size       int
	attach     time.Duration // Alice POST round-trip
	durable    time.Duration // t0 → custody receipt on Alice ("≤ attach" when synchronous)
	syncedRow  time.Duration // t0 → payload row materialized on Bob
	content    time.Duration // t0 → byte-identical download completed on Bob
	redownload time.Duration // second (locally cached) download on Bob
}

func TestE2E_MultipeerFileLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("benchmark test takes minutes; rerun without -short")
	}
	nodeconf := filesLatencyNodeconf(t)

	bin := buildBinary(t)
	alice := startPeerConf(t, bin, "alice", nodeconf)
	defer alice.stop(t)
	bob := startPeerConf(t, bin, "bob", nodeconf)
	defer bob.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, alice.base+"/v1/spaces",
		`{"name":"file-latency"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, alice.base+"/v1/spaces/"+sp.Id+"/objects",
		`{"type":"page"}`, http.StatusCreated, &obj)

	joinSpace(t, alice, bob, sp.Id, api.SpacePermissionReader)

	aliceFiles := alice.base + "/v1/spaces/" + sp.Id + "/files"
	bobFiles := bob.base + "/v1/spaces/" + sp.Id + "/files"
	attachURL := alice.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId + "/files"

	// --- warm-up: payloads tree + broker base URL + first sync -------
	warm := benchAttach(t, attachURL+"?name=warmup.bin", benchPayload(8<<10, 1))
	if !pollUntil(120*time.Second, func() bool {
		resp, _ := doRequest(t, http.MethodGet, bobFiles+"/"+warm.FileId, "")
		return resp.StatusCode == http.StatusOK
	}) {
		t.Fatalf("warm-up row never reached bob — is the shared space syncing on this network?")
	}
	if !warm.Durable && !pollUntil(120*time.Second, func() bool {
		var st api.FileStatus
		mustJSON(t, http.MethodGet, aliceFiles+"/"+warm.FileId+"/status", "", http.StatusOK, &st)
		return st.State == api.FileStateDurable
	}) {
		t.Fatalf("warm-up file never became durable — is the fileV2 broker's object store reachable?")
	}
	// Content fetch across peers needs the broker's objstore to expose a
	// PUBLIC read base ({base}/blob/…): s3compat/s3aws with
	// publicReadBaseUrl on a public bucket. The `local` provider serves
	// HMAC-signed GETs only, which the SDK's fetch ladder cannot consume
	// (presigned + P2P are SDK roadmap). On such infra the non-inline
	// content@bob leg is reported n/a instead of failing the benchmark —
	// row-sync/durable timings and the inline end-to-end path still run.
	publicRead := true
	if lat, _ := benchDownload(t, bobFiles+"/"+warm.FileId+"/content", benchPayload(8<<10, 1), 60*time.Second, time.Time{}); lat < 0 {
		_, raw := doRequest(t, http.MethodGet, bobFiles+"/"+warm.FileId+"/content", "")
		if strings.Contains(string(raw), "no public read base") {
			publicRead = false
			t.Logf("broker advertises no public read base — content@bob measured for inline files only (%s)", raw)
		} else {
			t.Fatalf("warm-up content never downloadable on bob: %s", raw)
		}
	}

	// --- watchers: event-timestamped, opened once ----------------------
	// Bob's payload-row stream: a `changes` frame carrying the fileId is
	// the moment the row materialized in his replica.
	sseCtx, cancelSSE := context.WithCancel(context.Background())
	defer cancelSSE()
	bobRows, bobRowsErr := openSSE(sseCtx, t, http.MethodPost,
		bob.base+"/v1/spaces/"+sp.Id+"/objects/"+obj.ObjectId+"/files/query/subscribe", "{}")
	waitSSE(t, bobRows, bobRowsErr, "ready", 15*time.Second)
	waitSSE(t, bobRows, bobRowsErr, "snapshot", 15*time.Second)

	// Alice's durability stream: the `status` frame flipping the fileId
	// to durable is the custody-receipt moment.
	aliceStatus, aliceStatusErr := openSSE(sseCtx, t, http.MethodGet,
		aliceFiles+"/subscribe", "")
	waitSSE(t, aliceStatus, aliceStatusErr, "ready", 15*time.Second)

	// --- measured runs ---------------------------------------------------
	const perFileDeadline = 3 * time.Minute
	var results []latencyResult
	for i, size := range latencySizes(t) {
		content := benchPayload(size, int64(100+i))
		name := fmt.Sprintf("bench-%d.bin", size)

		t0 := time.Now()
		info := benchAttach(t, attachURL+"?name="+name, content)
		attachDur := time.Since(t0)
		if info.Size != int64(size) {
			t.Fatalf("attach size = %d, want %d", info.Size, size)
		}

		rowCh := make(chan time.Duration, 1)
		go func() { rowCh <- watchFrameFor(bobRows, info.FileId, "", t0, perFileDeadline) }()

		durCh := make(chan time.Duration, 1)
		if info.Durable {
			// Backup completed inside the attach request (or inline tier).
			durCh <- attachDur
		} else {
			go func() { durCh <- watchFrameFor(aliceStatus, info.FileId, api.FileStateDurable, t0, perFileDeadline) }()
		}

		measureContent := publicRead || info.Inline
		contentCh := make(chan time.Duration, 1)
		if measureContent {
			go func() {
				lat, _ := benchDownload(t, bobFiles+"/"+info.FileId+"/content", content, perFileDeadline, t0)
				contentCh <- lat
			}()
		} else {
			contentCh <- -1 // n/a: no public read base and not inline
		}

		res := latencyResult{size: size, attach: attachDur}
		res.syncedRow = <-rowCh
		res.durable = <-durCh
		res.content = <-contentCh
		checks := map[string]time.Duration{"row@bob": res.syncedRow, "durable": res.durable}
		if measureContent {
			checks["content@bob"] = res.content
		}
		for what, v := range checks {
			if v < 0 {
				t.Fatalf("size %s: %s not reached within %s (inline=%v, durable-at-attach=%v)",
					benchSize(size), what, perFileDeadline, info.Inline, info.Durable)
			}
		}
		// Second download: bytes are local on Bob now — loopback+disk reference.
		res.redownload = -1
		if res.content >= 0 {
			re0 := time.Now()
			if lat, _ := benchDownload(t, bobFiles+"/"+info.FileId+"/content", content, 60*time.Second, re0); lat >= 0 {
				res.redownload = lat
			}
		}
		results = append(results, res)

		t.Logf("size %-8s attach=%-10s durable=%-10s row@bob=%-10s content@bob=%-10s redownload=%s",
			benchSize(size), rd(res.attach), rd(res.durable), rd(res.syncedRow), rd(res.content), rd(res.redownload))
	}

	// --- summary ---------------------------------------------------------
	t.Log("=== Alice→Bob file latency (steady-state, reactive sync, no forced rounds) ===")
	t.Logf("%-10s %-12s %-12s %-12s %-14s %-12s %s", "size", "attach", "durable", "row@bob", "content@bob", "redownload", "bob throughput")
	for _, r := range results {
		thr := "n/a"
		if r.content > 0 {
			thr = fmt.Sprintf("%.1f MB/s", float64(r.size)/r.content.Seconds()/(1<<20))
		}
		t.Logf("%-10s %-12s %-12s %-12s %-14s %-12s %s",
			benchSize(r.size), rd(r.attach), rd(r.durable), rd(r.syncedRow), rd(r.content), rd(r.redownload), thr)
	}
}

// benchPayload builds a deterministic pseudo-random payload (seeded so
// re-downloads can re-derive the expected bytes without holding copies).
func benchPayload(size int, seed int64) []byte {
	buf := make([]byte, size)
	rand.New(rand.NewSource(seed)).Read(buf)
	return buf
}

// benchAttach uploads content as the raw POST body via the timeout-less
// client (a 10MB body + synchronous backup try must not race the
// e2eClient request timeout).
func benchAttach(t *testing.T, url string, content []byte) api.FileInfo {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("build attach: %v", err)
	}
	resp, err := latencyHTTP.Do(req)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("attach %s: %d %s", url, resp.StatusCode, raw)
	}
	var info api.FileInfo
	if err := json.Unmarshal(raw, &info); err != nil || info.FileId == "" {
		t.Fatalf("attach reply: %v (%s)", err, raw)
	}
	return info
}

// benchDownload polls url until a full byte-identical download
// succeeds, returning the latency measured from t0 (zero t0 = from
// first attempt). Returns (-1, 0) on deadline. The second return is
// the duration of the successful transfer alone.
func benchDownload(t *testing.T, url string, want []byte, deadline time.Duration, t0 time.Time) (time.Duration, time.Duration) {
	t.Helper()
	if t0.IsZero() {
		t0 = time.Now()
	}
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		reqStart := time.Now()
		resp, err := latencyHTTP.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if rerr == nil && bytes.Equal(body, want) {
				return time.Since(t0), time.Since(reqStart)
			}
			// A short/mismatched read mid-availability — retry.
		} else if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		time.Sleep(25 * time.Millisecond)
	}
	return -1, 0
}

// watchFrameFor reads SSE frames until one mentions fileId (and, when
// state is non-empty, that state string too — the durable flip),
// returning the latency from t0, or -1 on deadline. Frames for other
// files (earlier uploads, warm-up churn) are skipped.
func watchFrameFor(frames <-chan sseFrame, fileId, state string, t0 time.Time, deadline time.Duration) time.Duration {
	timeout := time.After(deadline)
	for {
		select {
		case f := <-frames:
			data := string(f.Data)
			if !strings.Contains(data, fileId) {
				continue
			}
			if state != "" && !strings.Contains(data, `"state":"`+state+`"`) {
				continue
			}
			return time.Since(t0)
		case <-timeout:
			return -1
		}
	}
}

// benchSize renders a byte count human-readably for the report.
func benchSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.0fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// rd rounds a duration for compact log output; negative = "n/a".
func rd(d time.Duration) string {
	switch {
	case d < 0:
		return "n/a"
	case d < 10*time.Millisecond:
		return d.Round(10 * time.Microsecond).String()
	default:
		return d.Round(time.Millisecond).String()
	}
}
