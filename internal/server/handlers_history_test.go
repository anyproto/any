package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// An unclassified SDK error must not reach the client: its text can name
// internal types and filesystem paths (docs/06-errors.md). Only
// version_not_found passes an SDK message through, and it names the
// version the caller asked for.
func TestHistoryErrorHidesSdkText(t *testing.T) {
	const secret = "boltdb: /home/someone/.any/sdk.db bucket \"tree\""
	e := echo.New()

	newCtx := func() (echo.Context, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		return e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec), rec
	}
	decode := func(t *testing.T, rec *httptest.ResponseRecorder) api.ErrorEnvelope {
		t.Helper()
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v (%s)", err, rec.Body.String())
		}
		return env
	}

	c, rec := newCtx()
	if err := historyError(c, errors.New(secret)); err != nil {
		t.Fatalf("historyError returned %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	env := decode(t, rec)
	if env.Error.Code != "internal" || env.Error.Message != "internal error" {
		t.Errorf("envelope = %+v, want the fixed internal error", env.Error)
	}
	if strings.Contains(rec.Body.String(), "boltdb") || strings.Contains(rec.Body.String(), "/home/") {
		t.Errorf("SDK text reached the client: %s", rec.Body.String())
	}

	// The deliberate pass-through still carries its message.
	c, rec = newCtx()
	if err := historyError(c, fmt.Errorf("version bafy…: %w", space.ErrVersionNotFound)); err != nil {
		t.Fatalf("historyError returned %v", err)
	}
	if env = decode(t, rec); env.Error.Code != "history.version_not_found" || env.Error.Message == "" {
		t.Errorf("version_not_found envelope = %+v, want the SDK message", env.Error)
	}
}

// TestServer_History drives the version-history surface through echo
// against a real SDK, using the built-in editor dataset as the
// workload (chat opts out of history via SkipHistory):
// create/patch/delete blocks, then list history
// (filters, pagination, coalescing), view past versions (object and
// record scope), and diff (range, per-change effect, record scope).
// Versions are the changeIds the write endpoints already return.
func TestServer_History(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	histBase := base + "/history"

	create := func(text string) (blockId, changeId string) {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, base+"/editor/editor_blocks/blocks",
			fmt.Sprintf(`{"type":"paragraph","text":%q}`, text))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: %d %s", text, rec.Code, rec.Body.String())
		}
		res := decodeModifyResult(t, rec.Body.Bytes())
		if res.ChangeId == "" || len(res.RecordIds) == 0 {
			t.Fatalf("create %q: missing changeId/recordIds: %+v", text, res)
		}
		return res.RecordIds[0], res.ChangeId
	}

	blk1, v1 := create("first")
	blk2, v2 := create("second")

	// Patch blk1.
	rec := doJSON(t, e, http.MethodPatch, base+"/editor/editor_blocks/blocks/"+blk1, `{"set":{"text":"first (edited)"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	v3 := decodeModifyResult(t, rec.Body.Bytes()).ChangeId

	// Delete blk2.
	rec = doJSON(t, e, http.MethodDelete, base+"/editor/editor_blocks/blocks/"+blk2, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	v4 := decodeModifyResult(t, rec.Body.Bytes()).ChangeId

	getHistory := func(path string) api.HistoryListResponse {
		t.Helper()
		rec := doJSON(t, e, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		var out api.HistoryListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		return out
	}

	// ---- list: descending, editor dataset only ----
	list := getHistory(histBase + "?dataset=editor_blocks")
	if len(list.Changes) != 4 {
		t.Fatalf("want 4 editor changes, got %d: %+v", len(list.Changes), list.Changes)
	}
	wantOrder := []string{v4, v3, v2, v1}
	for i, want := range wantOrder {
		if list.Changes[i].Version != want {
			t.Errorf("changes[%d] = %s, want %s", i, list.Changes[i].Version, want)
		}
		if list.Changes[i].Author == "" {
			t.Errorf("changes[%d] author unstamped", i)
		}
		if list.Changes[i].GroupSize != 1 {
			t.Errorf("changes[%d] groupSize = %d, want 1", i, list.Changes[i].GroupSize)
		}
	}

	// ---- pagination ----
	page1 := getHistory(histBase + "?dataset=editor_blocks&limit=2")
	if len(page1.Changes) != 2 || page1.Cursor == "" {
		t.Fatalf("page1: %+v", page1)
	}
	page2 := getHistory(histBase + "?dataset=editor_blocks&limit=10&cursor=" + page1.Cursor)
	if len(page2.Changes) != 2 || page2.Cursor != "" {
		t.Fatalf("page2: %+v", page2)
	}
	if page2.Changes[0].Version != v2 {
		t.Errorf("page2 head = %s, want %s", page2.Changes[0].Version, v2)
	}

	// ---- record filter ----
	recList := getHistory(histBase + "?dataset=editor_blocks&recordId=" + blk1)
	if len(recList.Changes) != 2 {
		t.Fatalf("record filter: want 2 changes, got %+v", recList.Changes)
	}

	// ---- coalescing: same author, rapid writes → one group ----
	grouped := getHistory(histBase + "?dataset=editor_blocks&coalesce=true")
	if len(grouped.Changes) != 1 {
		t.Fatalf("coalesced: want 1 group, got %d: %+v", len(grouped.Changes), grouped.Changes)
	}
	if grouped.Changes[0].GroupSize != 4 || grouped.Changes[0].Version != v4 {
		t.Errorf("group = %+v, want head %s size 4", grouped.Changes[0], v4)
	}

	// ---- ViewAt v2: both blocks alive, blk1 unedited ----
	rec = doJSON(t, e, http.MethodGet, histBase+"/"+v2+"?dataset=editor_blocks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("viewAt: %d %s", rec.Code, rec.Body.String())
	}
	var view api.HistoryViewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if len(view.Datasets) != 1 || view.Datasets[0].Dataset != "editor_blocks" {
		t.Fatalf("view datasets: %+v", view.Datasets)
	}
	if len(view.Datasets[0].Records) != 2 {
		t.Fatalf("view records: want 2, got %d", len(view.Datasets[0].Records))
	}
	texts := map[string]string{}
	for _, raw := range view.Datasets[0].Records {
		var m struct {
			Id   string `json:"id"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode record: %v", err)
		}
		texts[m.Id] = m.Text
	}
	if texts[blk1] != "first" {
		t.Errorf("blk1 at v2 = %q, want %q (pre-edit)", texts[blk1], "first")
	}

	// ---- RecordAt: pre-edit text; tombstone after delete; absent early ----
	getRecord := func(version, recordId string) api.HistoryRecordResponse {
		t.Helper()
		rec := doJSON(t, e, http.MethodGet,
			histBase+"/"+version+"/datasets/editor_blocks/records/"+recordId, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("recordAt: %d %s", rec.Code, rec.Body.String())
		}
		var out api.HistoryRecordResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode record response: %v", err)
		}
		return out
	}

	r := getRecord(v2, blk1)
	if !r.Exists || r.Deleted {
		t.Fatalf("blk1@v2: %+v", r)
	}
	var m struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(r.Record, &m); err != nil || m.Text != "first" {
		t.Errorf("blk1@v2 text = %q (err %v), want %q", m.Text, err, "first")
	}

	r = getRecord(v4, blk2)
	if !r.Exists || !r.Deleted {
		t.Errorf("blk2@v4 should be a tombstone: %+v", r)
	}
	r = getRecord(v1, blk2)
	if r.Exists {
		t.Errorf("blk2@v1 should not exist: %+v", r)
	}

	// ---- Diff v2..v4: blk1 changed (text), blk2 deleted ----
	rec = doJSON(t, e, http.MethodGet,
		histBase+"/diff?base="+v2+"&version="+v4+"&dataset=editor_blocks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("diff: %d %s", rec.Code, rec.Body.String())
	}
	var diff api.HistoryDiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &diff); err != nil {
		t.Fatalf("decode diff: %v", err)
	}
	if len(diff.Datasets) != 1 {
		t.Fatalf("diff datasets: %+v", diff.Datasets)
	}
	kinds := map[string]string{}
	for _, rd := range diff.Datasets[0].Records {
		kinds[rd.Id] = rd.Kind
	}
	if kinds[blk1] != "changed" || kinds[blk2] != "deleted" {
		t.Errorf("diff kinds = %v, want blk1 changed / blk2 deleted", kinds)
	}

	// ---- per-change effect diff (base empty) ----
	rec = doJSON(t, e, http.MethodGet, histBase+"/diff?version="+v3+"&dataset=editor_blocks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("effect diff: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &diff); err != nil {
		t.Fatalf("decode effect diff: %v", err)
	}
	if len(diff.Datasets) != 1 || len(diff.Datasets[0].Records) != 1 || diff.Datasets[0].Records[0].Id != blk1 {
		t.Fatalf("effect diff should touch only blk1: %+v", diff.Datasets)
	}

	// ---- validation & error mapping ----
	rec = doJSON(t, e, http.MethodGet, histBase+"?recordId=x", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("recordId without dataset: %d, want 400", rec.Code)
	}
	rec = doJSON(t, e, http.MethodGet, histBase+"/diff?version="+v3+"&recordIds=a,b", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("recordIds without dataset: %d, want 400", rec.Code)
	}
	rec = doJSON(t, e, http.MethodGet, histBase+"/not-a-change-id", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown version: %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err == nil {
		if env.Error.Code != "history.version_not_found" {
			t.Errorf("unknown version code = %q", env.Error.Code)
		}
	}
	rec = doJSON(t, e, http.MethodGet, histBase+"?limit=abc", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad limit: %d, want 400", rec.Code)
	}

	// ---- SkipHistory: chat writes leave no history ----
	// Chat renders live records only (SYN-86) — no per-message
	// timelines, so chat_messages opts out of the history index.
	chatSpace, chatObj := setupChatFixture(t, e)
	chatBase := "/v1/spaces/" + chatSpace + "/objects/" + chatObj
	msg := chatSend(t, e, chatBase, "hello", "")
	if msg.Id == "" {
		t.Fatalf("chat send failed")
	}
	chatHist := chatBase + "/history"
	if got := getHistory(chatHist + "?dataset=chat_messages"); len(got.Changes) != 0 {
		t.Errorf("chat_messages history should be empty (SkipHistory), got %+v", got.Changes)
	}
	for _, c := range getHistory(chatHist).Changes {
		if c.Dataset == "chat_messages" {
			t.Errorf("unfiltered history leaked a chat change: %+v", c)
		}
	}
}
