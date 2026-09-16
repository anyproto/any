// Files v2 coverage through the real binary: raw-body attach (inline
// + content-addressed tiers), plain-HTTP download with Range/206,
// list/get/stats/status, the payload-row query bridge, pin/retry/
// offload semantics, variants, the status SSE stream, and the
// account-wide cache endpoints. The staging fixture has no fileV2
// nodes, so non-inline files stay in the offline-first `inflight`
// state — assertions branch on durability rather than assuming a
// backup completed.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

func TestE2E_FilesBinary(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, base+"/v1/spaces",
		`{"name":"files-binary"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{"type":"page"}`, http.StatusCreated, &obj)

	filesBase := base + "/v1/spaces/" + sp.Id + "/files"
	attachBase := base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId + "/files"

	// Open the status stream before attaching so the attach transitions
	// are observable.
	sseCtx, cancelSSE := context.WithCancel(context.Background())
	defer cancelSSE()
	frames, errc := openSSE(sseCtx, t, http.MethodGet, filesBase+"/subscribe", "")
	waitSSE(t, frames, errc, "ready", 10*time.Second)

	// --- attach: inline tier (< 4096 bytes) --------------------------
	inlineContent := []byte("hello inline files v2")
	inlineInfo := attachFile(t, attachBase+"?name=note.txt", "text/plain; charset=utf-8", inlineContent)
	if !inlineInfo.Inline || !inlineInfo.Durable || !inlineInfo.Cached {
		t.Errorf("inline file flags = %+v, want inline+durable+cached", inlineInfo)
	}
	if inlineInfo.RootCid != "" {
		t.Errorf("inline file has rootCid %q, want none", inlineInfo.RootCid)
	}
	if inlineInfo.Size != int64(len(inlineContent)) {
		t.Errorf("inline size = %d, want %d", inlineInfo.Size, len(inlineContent))
	}
	if inlineInfo.Name != "note.txt" || inlineInfo.Mime != "text/plain" {
		t.Errorf("inline meta = name %q mime %q, want note.txt / text/plain", inlineInfo.Name, inlineInfo.Mime)
	}

	// --- attach: content-addressed tier ------------------------------
	bigContent := make([]byte, 300_000)
	rnd := rand.New(rand.NewSource(42))
	rnd.Read(bigContent)
	bigInfo := attachFile(t, attachBase+"?name=blob.bin", "application/x-e2e-blob", bigContent)
	if bigInfo.Inline {
		t.Errorf("300KB file came back inline: %+v", bigInfo)
	}
	if bigInfo.RootCid == "" {
		t.Errorf("content-addressed file missing rootCid")
	}
	if !bigInfo.Cached {
		t.Errorf("fresh attach not cached: %+v", bigInfo)
	}
	if bigInfo.Size != int64(len(bigContent)) {
		t.Errorf("size = %d, want %d", bigInfo.Size, len(bigContent))
	}

	// The stream saw at least one local transition for the big file.
	sawStatus := waitSSE(t, frames, errc, "status", 15*time.Second)
	var streamed api.FileStatus
	if err := json.Unmarshal(sawStatus.Data, &streamed); err != nil {
		t.Fatalf("decode status frame: %v (%s)", err, sawStatus.Data)
	}
	if streamed.FileId == "" {
		t.Errorf("status frame missing fileId: %s", sawStatus.Data)
	}

	// --- get / list ---------------------------------------------------
	var got api.FileInfo
	mustJSON(t, http.MethodGet, filesBase+"/"+bigInfo.FileId, "", http.StatusOK, &got)
	if got.FileId != bigInfo.FileId || got.ObjectId != obj.ObjectId {
		t.Errorf("get mismatch: %+v", got)
	}

	var list api.FileListResponse
	mustJSON(t, http.MethodGet, filesBase, "", http.StatusOK, &list)
	if len(list.Files) != 2 {
		t.Errorf("space listing = %d files, want 2: %+v", len(list.Files), list.Files)
	}
	mustJSON(t, http.MethodGet, filesBase+"?objectId="+obj.ObjectId+"&limit=1", "", http.StatusOK, &list)
	if len(list.Files) != 1 {
		t.Errorf("limited listing = %d files, want 1", len(list.Files))
	}

	// --- download: full + headers ------------------------------------
	resp := doRawGet(t, filesBase+"/"+bigInfo.FileId+"/content", "")
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: %d %s", resp.StatusCode, body)
	}
	if !bytes.Equal(body, bigContent) {
		t.Fatalf("downloaded %d bytes, differ from uploaded %d", len(body), len(bigContent))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-e2e-blob" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `inline; filename=blob.bin` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if cl := resp.ContentLength; cl != int64(len(bigContent)) {
		t.Errorf("Content-Length = %d, want %d", cl, len(bigContent))
	}

	// --- download: Range / 206 ---------------------------------------
	resp = doRawGet(t, filesBase+"/"+bigInfo.FileId+"/content", "bytes=100-4099")
	body = readAll(t, resp)
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range download: %d, want 206", resp.StatusCode)
	}
	if !bytes.Equal(body, bigContent[100:4100]) {
		t.Fatalf("range bytes differ (got %d bytes)", len(body))
	}

	// Inline files download identically.
	resp = doRawGet(t, filesBase+"/"+inlineInfo.FileId+"/content", "")
	if body = readAll(t, resp); !bytes.Equal(body, inlineContent) {
		t.Fatalf("inline download differs: %q", body)
	}

	// --- status + stats -----------------------------------------------
	var st api.FileStatus
	mustJSON(t, http.MethodGet, filesBase+"/"+inlineInfo.FileId+"/status", "", http.StatusOK, &st)
	if st.State != api.FileStateDurable || !st.Cached {
		t.Errorf("inline status = %+v, want durable+cached", st)
	}
	mustJSON(t, http.MethodGet, filesBase+"/"+bigInfo.FileId+"/status", "", http.StatusOK, &st)
	if st.State != api.FileStateDurable && st.State != api.FileStateInFlight && st.State != api.FileStateLimited {
		t.Errorf("big status state = %q", st.State)
	}

	var stats api.FileStats
	mustJSON(t, http.MethodGet, filesBase+"/stats", "", http.StatusOK, &stats)
	if stats.Total != 2 {
		t.Errorf("stats = %+v, want total 2", stats)
	}

	// --- pin / retry / offload -----------------------------------------
	mustStatus(t, http.MethodPost, filesBase+"/"+bigInfo.FileId+"/pin", "", http.StatusNoContent)
	mustStatus(t, http.MethodPost, filesBase+"/"+bigInfo.FileId+"/retry", "", http.StatusNoContent)

	// Re-read durability: with no fileV2 nodes in staging the file is
	// still local-only and offload must refuse; if the network ever
	// grows fileV2 nodes the file may be durable, in which case offload
	// succeeds and a re-download refetches.
	mustJSON(t, http.MethodGet, filesBase+"/"+bigInfo.FileId, "", http.StatusOK, &got)
	if got.Durable {
		mustStatus(t, http.MethodPost, filesBase+"/"+bigInfo.FileId+"/offload", "", http.StatusNoContent)
		resp = doRawGet(t, filesBase+"/"+bigInfo.FileId+"/content", "")
		if body = readAll(t, resp); !bytes.Equal(body, bigContent) {
			t.Fatalf("post-offload refetch differs (%d bytes, status %d)", len(body), resp.StatusCode)
		}
	} else {
		var env map[string]any
		mustJSON(t, http.MethodPost, filesBase+"/"+bigInfo.FileId+"/offload", "", http.StatusConflict, &env)
		errObj, _ := env["error"].(map[string]any)
		if errObj["code"] != api.ErrFileNotDurable {
			t.Errorf("offload code = %v, want %s", errObj["code"], api.ErrFileNotDurable)
		}
	}

	// Inline offload is a documented no-op.
	mustStatus(t, http.MethodPost, filesBase+"/"+inlineInfo.FileId+"/offload", "", http.StatusNoContent)

	// --- variants -------------------------------------------------------
	thumb := []byte("tiny thumbnail bytes")
	thumbInfo := attachFile(t,
		attachBase+"?name=blob-thumb.png&variant=thumb&variantOf="+bigInfo.FileId,
		"image/png", thumb)
	if thumbInfo.Variant != "thumb" || thumbInfo.VariantOf != bigInfo.FileId {
		t.Errorf("variant meta = %+v", thumbInfo)
	}
	resp = doRawGet(t, filesBase+"/"+bigInfo.FileId+"/content?variant=thumb", "")
	if body = readAll(t, resp); !bytes.Equal(body, thumb) {
		t.Fatalf("variant download differs: status %d, %d bytes", resp.StatusCode, len(body))
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("variant Content-Type = %q, want image/png", ct)
	}

	// Broken pairing → 400 file.variant_invalid, before any upload work.
	var env map[string]any
	mustJSON(t, http.MethodPost, attachBase+"?variant=thumb", "x", http.StatusBadRequest, &env)
	errObj, _ := env["error"].(map[string]any)
	if errObj["code"] != api.ErrFileVariantInvalid {
		t.Errorf("pairing code = %v, want %s", errObj["code"], api.ErrFileVariantInvalid)
	}

	// --- attach to a missing object → 404 file.not_found ---------------
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects/nope/files",
		"x", http.StatusNotFound, &env)
	errObj, _ = env["error"].(map[string]any)
	if errObj["code"] != api.ErrFileNotFound {
		t.Errorf("bad object code = %v, want %s", errObj["code"], api.ErrFileNotFound)
	}

	// --- payload-row query bridge --------------------------------------
	var qr api.QueryResponse
	mustJSON(t, http.MethodPost,
		base+"/v1/spaces/"+sp.Id+"/objects/"+obj.ObjectId+"/files/query",
		`{"includeTotal":true}`, http.StatusOK, &qr)
	if qr.Total == nil || *qr.Total != 3 {
		t.Errorf("files query total = %v, want 3 (2 files + variant)", qr.Total)
	}
	// Rows carry the cleartext fields; sealed meta must not leak.
	var row map[string]any
	if len(qr.Records) == 0 {
		t.Fatalf("files query returned no records")
	}
	if err := json.Unmarshal(qr.Records[0], &row); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	if row["objectId"] != obj.ObjectId {
		t.Errorf("row objectId = %v", row["objectId"])
	}

	// Query against an object with no files yet → 404 file.not_found.
	var obj2 api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+sp.Id+"/objects",
		`{"type":"page"}`, http.StatusCreated, &obj2)
	mustJSON(t, http.MethodPost,
		base+"/v1/spaces/"+sp.Id+"/objects/"+obj2.ObjectId+"/files/query",
		"", http.StatusNotFound, &env)
	errObj, _ = env["error"].(map[string]any)
	if errObj["code"] != api.ErrFileNotFound {
		t.Errorf("empty-object query code = %v, want %s", errObj["code"], api.ErrFileNotFound)
	}

	// --- account-wide cache endpoints -----------------------------------
	var cache api.FileCacheInfo
	mustJSON(t, http.MethodGet, base+"/v1/files/cache", "", http.StatusOK, &cache)
	if cache.Size <= 0 {
		t.Errorf("cache size = %d, want > 0 (a 300KB CARv2 is local)", cache.Size)
	}
	var freed api.FileCacheFreeResult
	mustJSON(t, http.MethodPost, base+"/v1/files/cache/free",
		`{"bytes":1}`, http.StatusOK, &freed)
	if freed.Freed < 0 {
		t.Errorf("freed = %d", freed.Freed)
	}
	mustJSON(t, http.MethodPost, base+"/v1/files/cache/free",
		`{"bytes":0}`, http.StatusBadRequest, &env)
	mustStatus(t, http.MethodPost, base+"/v1/files/cache/sweep", "", http.StatusNoContent)

	// --- delete -----------------------------------------------------------
	// Deleting the original cascades to its variant; the inline file
	// survives. A second delete (or an unknown id) is a 404.
	mustStatus(t, http.MethodDelete, filesBase+"/"+bigInfo.FileId, "", http.StatusNoContent)
	mustJSON(t, http.MethodGet, filesBase+"/"+bigInfo.FileId, "", http.StatusNotFound, &env)
	mustJSON(t, http.MethodGet, filesBase+"/"+thumbInfo.FileId, "", http.StatusNotFound, &env)
	errObj, _ = env["error"].(map[string]any)
	if errObj["code"] != api.ErrFileNotFound {
		t.Errorf("deleted variant code = %v, want %s", errObj["code"], api.ErrFileNotFound)
	}
	mustJSON(t, http.MethodGet, filesBase, "", http.StatusOK, &list)
	if len(list.Files) != 1 {
		t.Errorf("post-delete listing = %d files, want 1: %+v", len(list.Files), list.Files)
	}
	mustJSON(t, http.MethodDelete, filesBase+"/"+bigInfo.FileId, "", http.StatusNotFound, &env)
	mustJSON(t, http.MethodDelete, filesBase+"/no-such-file", "", http.StatusNotFound, &env)

	mustStatus(t, http.MethodDelete, filesBase+"/"+inlineInfo.FileId, "", http.StatusNoContent)
	mustJSON(t, http.MethodGet, filesBase+"/stats", "", http.StatusOK, &stats)
	if stats.Total != 0 {
		t.Errorf("post-delete stats = %+v, want total 0", stats)
	}
}

// attachFile POSTs raw bytes to url with the given content type and
// decodes the 201 FileInfo receipt.
func attachFile(t *testing.T, url, contentType string, content []byte) api.FileInfo {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("build attach request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := e2eClient.Do(req)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("attach %s: %d %s", url, resp.StatusCode, raw)
	}
	var info api.FileInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("decode attach reply: %v (%s)", err, raw)
	}
	if info.FileId == "" {
		t.Fatalf("attach reply missing fileId: %s", raw)
	}
	return info
}

// doRawGet GETs url (optionally with a Range header) and returns the
// raw response; the caller reads and closes via readAll.
func doRawGet(t *testing.T, url, rangeHeader string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := e2eClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return raw
}
