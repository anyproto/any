package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/email"
)

// TestServer_Email_RoundTrip drives the email surface through echo
// against a real SDK: mailbox derive → batch ingest → re-ingest
// (idempotency + label diff) → query rows (derived fields, sort) →
// patch → delete.
func TestServer_Email_RoundTrip(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"EmailTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	spaceId := sp.Id

	// Mailbox derive is deterministic and normalizing: same id for
	// the same address regardless of case/whitespace.
	mailbox := emailMailbox(t, e, spaceId, "User@Example.COM")
	if again := emailMailbox(t, e, spaceId, "  user@example.com "); again != mailbox {
		t.Fatalf("mailbox derive not deterministic: %q vs %q", mailbox, again)
	}
	base := "/v1/spaces/" + spaceId + "/objects/" + mailbox

	// Ingest a two-message page.
	batch := `{"messages": [
		{"id": "msg-older", "threadId": "t1", "internalDate": 1786000000000,
		 "from": {"name": "Alice", "address": "Alice@Example.com"},
		 "to": [{"address": "user@example.com"}],
		 "subject": "older", "bodyText": "first body",
		 "labelIds": ["INBOX", "UNREAD"], "historyId": "100"},
		{"id": "msg-newer", "threadId": "t1", "internalDate": 1786000001000,
		 "from": {"address": "bob@example.com"},
		 "subject": "newer", "bodyText": "second body",
		 "labelIds": ["INBOX"], "historyId": "100"}
	]}`
	res := emailIngest(t, e, base, batch)
	if len(res.Created) != 2 || len(res.Updated) != 0 || len(res.Unchanged) != 0 {
		t.Fatalf("first ingest: created=%v updated=%v unchanged=%v", res.Created, res.Updated, res.Unchanged)
	}
	if res.VersionId == "" || res.ChangeId == "" {
		t.Fatalf("first ingest: missing versionId/changeId: %+v", res)
	}

	// Identical re-ingest: everything unchanged, NO change committed.
	res = emailIngest(t, e, base, batch)
	if len(res.Unchanged) != 2 || len(res.Created) != 0 || len(res.Updated) != 0 {
		t.Fatalf("re-ingest: created=%v updated=%v unchanged=%v", res.Created, res.Updated, res.Unchanged)
	}
	if res.VersionId != "" {
		t.Fatalf("re-ingest committed a change: %+v", res)
	}

	// Read back via /query — newest-first on internalDate, derived
	// fields stamped, participants folded from from+to (normalized),
	// client-side participants filter works.
	rows := emailQuery(t, e, spaceId, mailbox, `{"sort": ["-internalDate"]}`)
	if len(rows) != 2 {
		t.Fatalf("query: got %d rows, want 2", len(rows))
	}
	if id0 := rows[0]["id"]; id0 != "msg-newer" {
		t.Errorf("sort: rows[0].id = %v, want msg-newer", id0)
	}
	older := rows[1]
	if older["creator"] != d.account {
		t.Errorf("creator = %v, want %s", older["creator"], d.account)
	}
	if older["createdAt"] == nil || older["modifiedAt"] == nil {
		t.Errorf("createdAt/modifiedAt unstamped: %v", older)
	}
	wantParticipants := []any{"alice@example.com", "user@example.com"}
	gotParticipants, _ := older["participants"].([]any)
	if len(gotParticipants) != 2 || gotParticipants[0] != wantParticipants[0] || gotParticipants[1] != wantParticipants[1] {
		t.Errorf("participants = %v, want %v", gotParticipants, wantParticipants)
	}
	filtered := emailQuery(t, e, spaceId, mailbox,
		`{"filter": {"participants": "alice@example.com"}}`)
	if len(filtered) != 1 || filtered[0]["id"] != "msg-older" {
		t.Errorf("participants filter: %v", filtered)
	}

	// Label-only change on one id: updated (not created), and the
	// stored labels converge.
	relabeled := `{"messages": [
		{"id": "msg-older", "threadId": "t1", "internalDate": 1786000000000,
		 "from": {"name": "Alice", "address": "Alice@Example.com"},
		 "to": [{"address": "user@example.com"}],
		 "subject": "older", "bodyText": "first body",
		 "labelIds": ["INBOX"], "historyId": "200"}
	]}`
	res = emailIngest(t, e, base, relabeled)
	if len(res.Updated) != 1 || res.Updated[0] != "msg-older" {
		t.Fatalf("relabel ingest: created=%v updated=%v unchanged=%v", res.Created, res.Updated, res.Unchanged)
	}
	rows = emailQuery(t, e, spaceId, mailbox, `{"filter": {"id": "msg-older"}}`)
	labels, _ := rows[0]["labelIds"].([]any)
	if len(labels) != 1 || labels[0] != "INBOX" {
		t.Errorf("labels after relabel = %v, want [INBOX]", labels)
	}
	if rows[0]["historyId"] != "200" {
		t.Errorf("historyId after relabel = %v, want 200", rows[0]["historyId"])
	}
	// Immutable content stays put even if the ingest carried it.
	if rows[0]["bodyText"] != "first body" {
		t.Errorf("bodyText changed on update: %v", rows[0]["bodyText"])
	}

	// PATCH — the incremental label-flip path.
	patchRec := doJSON(t, e, http.MethodPatch, base+"/email/messages/msg-newer",
		`{"labelIds": ["INBOX", "STARRED"]}`)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", patchRec.Code, patchRec.Body.String())
	}
	rows = emailQuery(t, e, spaceId, mailbox, `{"filter": {"labelIds": "STARRED"}}`)
	if len(rows) != 1 || rows[0]["id"] != "msg-newer" {
		t.Errorf("labelIds filter after patch: %v", rows)
	}

	// DELETE — expunge.
	delRec := doJSON(t, e, http.MethodDelete, base+"/email/messages/msg-older", "")
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", delRec.Code, delRec.Body.String())
	}
	rows = emailQuery(t, e, spaceId, mailbox, `{}`)
	if len(rows) != 1 || rows[0]["id"] != "msg-newer" {
		t.Errorf("after delete: %v", rows)
	}
	delRec = doJSON(t, e, http.MethodDelete, base+"/email/messages/msg-older", "")
	if delRec.Code != http.StatusNotFound {
		t.Errorf("re-delete: %d, want 404", delRec.Code)
	}
}

func TestServer_Email_IngestValidation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"EmailValidation"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}
	mailbox := emailMailbox(t, e, sp.Id, "v@example.com")
	base := "/v1/spaces/" + sp.Id + "/objects/" + mailbox

	cases := []struct {
		name, body, wantCode string
	}{
		{"empty batch", `{"messages": []}`, "request.missing_field"},
		{"bad id", `{"messages": [{"id": "with.dot", "threadId": "t", "internalDate": 1}]}`, api.ErrEmailInvalid},
		{"duplicate id", `{"messages": [
			{"id": "a", "threadId": "t", "internalDate": 1},
			{"id": "a", "threadId": "t", "internalDate": 2}]}`, api.ErrEmailInvalid},
		{"missing threadId", `{"messages": [{"id": "a", "internalDate": 1}]}`, api.ErrEmailInvalid},
		{"missing internalDate", `{"messages": [{"id": "a", "threadId": "t"}]}`, api.ErrEmailInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPost, base+"/email/messages", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
			var env api.ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if env.Error.Code != tc.wantCode {
				t.Errorf("error code = %q, want %q", env.Error.Code, tc.wantCode)
			}
		})
	}

	// Batch-size cap.
	big := `{"messages": [`
	for i := 0; i <= email.MaxIngestBatch; i++ {
		if i > 0 {
			big += ","
		}
		big += `{"id": "id` + strconv.Itoa(i) + `", "threadId": "t", "internalDate": 1}`
	}
	big += `]}`
	rec = doJSON(t, e, http.MethodPost, base+"/email/messages", big)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized batch: %d, want 400", rec.Code)
	}

	// PATCH with no mutable field.
	rec = doJSON(t, e, http.MethodPatch, base+"/email/messages/whatever", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty patch: %d, want 400 (%s)", rec.Code, rec.Body.String())
	}

	// Mailbox without address.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/email/mailbox", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("mailbox without address: %d, want 400", rec.Code)
	}
}

// --- helpers ----------------------------------------------------------------

func emailMailbox(t *testing.T, e http.Handler, spaceId, address string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet,
		"/v1/spaces/"+spaceId+"/email/mailbox?address="+url.QueryEscape(address), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET mailbox: %d %s", rec.Code, rec.Body.String())
	}
	var out api.EmailMailboxResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode mailbox: %v", err)
	}
	if out.ObjectId == "" {
		t.Fatal("empty mailbox objectId")
	}
	return out.ObjectId
}

func emailIngest(t *testing.T, e http.Handler, base, body string) api.EmailIngestResult {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, base+"/email/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST ingest: %d %s", rec.Code, rec.Body.String())
	}
	var out api.EmailIngestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode ingest result: %v", err)
	}
	if len(out.Rejections) > 0 {
		t.Fatalf("ingest rejections: %+v", out.Rejections)
	}
	return out
}

// emailQuery reads message records back through the generic per-object
// query endpoint — the one read path the design mandates.
func emailQuery(t *testing.T, e http.Handler, spaceId, objectId, body string) []map[string]any {
	t.Helper()
	full := `{"objectId": "` + objectId + `", "dataset": "` + email.Dataset + `"`
	if body != "" && body != "{}" {
		full += ", " + body[1:len(body)-1]
	}
	full += `}`
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query", full)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST query: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	return out.Records
}

