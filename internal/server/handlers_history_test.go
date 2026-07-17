package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_History drives the version-history surface through echo
// against a real SDK, using the built-in chat dataset as the workload:
// send/edit/delete messages, then list history (filters, pagination,
// coalescing), view past versions (object and record scope), and diff
// (range, per-change effect, record scope). Versions are the changeIds
// the write endpoints already return.
func TestServer_History(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	histBase := base + "/history"

	send := func(text string) (msgId, changeId string) {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, base+"/chat/messages", fmt.Sprintf(`{"text":%q}`, text))
		if rec.Code != http.StatusCreated {
			t.Fatalf("send %q: %d %s", text, rec.Code, rec.Body.String())
		}
		res := decodeModifyResult(t, rec.Body.Bytes())
		if res.ChangeId == "" || len(res.RecordIds) == 0 {
			t.Fatalf("send %q: missing changeId/recordIds: %+v", text, res)
		}
		return res.RecordIds[0], res.ChangeId
	}

	msg1, v1 := send("first")
	msg2, v2 := send("second")

	// Edit msg1.
	rec := doJSON(t, e, http.MethodPatch, base+"/chat/messages/"+msg1, `{"text":"first (edited)"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	v3 := decodeModifyResult(t, rec.Body.Bytes()).ChangeId

	// Delete msg2.
	rec = doJSON(t, e, http.MethodDelete, base+"/chat/messages/"+msg2, "")
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

	// ---- list: descending, chat dataset only ----
	list := getHistory(histBase + "?dataset=chat_messages")
	if len(list.Changes) != 4 {
		t.Fatalf("want 4 chat changes, got %d: %+v", len(list.Changes), list.Changes)
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
	page1 := getHistory(histBase + "?dataset=chat_messages&limit=2")
	if len(page1.Changes) != 2 || page1.Cursor == "" {
		t.Fatalf("page1: %+v", page1)
	}
	page2 := getHistory(histBase + "?dataset=chat_messages&limit=10&cursor=" + page1.Cursor)
	if len(page2.Changes) != 2 || page2.Cursor != "" {
		t.Fatalf("page2: %+v", page2)
	}
	if page2.Changes[0].Version != v2 {
		t.Errorf("page2 head = %s, want %s", page2.Changes[0].Version, v2)
	}

	// ---- record filter ----
	recList := getHistory(histBase + "?dataset=chat_messages&recordId=" + msg1)
	if len(recList.Changes) != 2 {
		t.Fatalf("record filter: want 2 changes, got %+v", recList.Changes)
	}

	// ---- coalescing: same author, rapid writes → one group ----
	grouped := getHistory(histBase + "?dataset=chat_messages&coalesce=true")
	if len(grouped.Changes) != 1 {
		t.Fatalf("coalesced: want 1 group, got %d: %+v", len(grouped.Changes), grouped.Changes)
	}
	if grouped.Changes[0].GroupSize != 4 || grouped.Changes[0].Version != v4 {
		t.Errorf("group = %+v, want head %s size 4", grouped.Changes[0], v4)
	}

	// ---- ViewAt v2: both messages alive, msg1 unedited ----
	rec = doJSON(t, e, http.MethodGet, histBase+"/"+v2+"?dataset=chat_messages", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("viewAt: %d %s", rec.Code, rec.Body.String())
	}
	var view api.HistoryViewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if len(view.Datasets) != 1 || view.Datasets[0].Dataset != "chat_messages" {
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
	if texts[msg1] != "first" {
		t.Errorf("msg1 at v2 = %q, want %q (pre-edit)", texts[msg1], "first")
	}

	// ---- RecordAt: pre-edit text; tombstone after delete; absent early ----
	getRecord := func(version, recordId string) api.HistoryRecordResponse {
		t.Helper()
		rec := doJSON(t, e, http.MethodGet,
			histBase+"/"+version+"/datasets/chat_messages/records/"+recordId, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("recordAt: %d %s", rec.Code, rec.Body.String())
		}
		var out api.HistoryRecordResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode record response: %v", err)
		}
		return out
	}

	r := getRecord(v2, msg1)
	if !r.Exists || r.Deleted {
		t.Fatalf("msg1@v2: %+v", r)
	}
	var m struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(r.Record, &m); err != nil || m.Text != "first" {
		t.Errorf("msg1@v2 text = %q (err %v), want %q", m.Text, err, "first")
	}

	r = getRecord(v4, msg2)
	if !r.Exists || !r.Deleted {
		t.Errorf("msg2@v4 should be a tombstone: %+v", r)
	}
	r = getRecord(v1, msg2)
	if r.Exists {
		t.Errorf("msg2@v1 should not exist: %+v", r)
	}

	// ---- Diff v2..v4: msg1 changed (text), msg2 deleted ----
	rec = doJSON(t, e, http.MethodGet,
		histBase+"/diff?base="+v2+"&version="+v4+"&dataset=chat_messages", "")
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
	if kinds[msg1] != "changed" || kinds[msg2] != "deleted" {
		t.Errorf("diff kinds = %v, want msg1 changed / msg2 deleted", kinds)
	}

	// ---- per-change effect diff (base empty) ----
	rec = doJSON(t, e, http.MethodGet, histBase+"/diff?version="+v3+"&dataset=chat_messages", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("effect diff: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &diff); err != nil {
		t.Fatalf("decode effect diff: %v", err)
	}
	if len(diff.Datasets) != 1 || len(diff.Datasets[0].Records) != 1 || diff.Datasets[0].Records[0].Id != msg1 {
		t.Fatalf("effect diff should touch only msg1: %+v", diff.Datasets)
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
}
