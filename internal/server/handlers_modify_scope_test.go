package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_PropertyScope drives scoped property definitions through
// the HTTP surface: declare a local-scope property, see the scope in
// the listing, and verify value writes auto-route (a local-scoped
// value gets a VersionId but no DAG ChangeId).
func TestServer_PropertyScope(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"ScopeTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types",
		`{"name":"Doc","xKey":"doc"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var typeResp api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &typeResp); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	propsURL := fmt.Sprintf("/v1/spaces/%s/types/%s/properties", sp.Id, typeResp.TypeId)

	// Declare one synced + one local property.
	rec = doJSON(t, e, http.MethodPost, propsURL, `{"name":"Title","kind":"string"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add synced prop: %d %s", rec.Code, rec.Body.String())
	}
	var titleProp api.AddPropertyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &titleProp)

	rec = doJSON(t, e, http.MethodPost, propsURL, `{"name":"Pin","kind":"boolean","scope":"local"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add local prop: %d %s", rec.Code, rec.Body.String())
	}
	var pinProp api.AddPropertyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pinProp)

	// Derived is reserved; junk is junk.
	for _, scope := range []string{"derived", "bogus"} {
		rec = doJSON(t, e, http.MethodPost, propsURL,
			fmt.Sprintf(`{"name":"Bad","kind":"string","scope":%q}`, scope))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("scope %q: status=%d, want 400: %s", scope, rec.Code, rec.Body.String())
		}
	}

	// The listing surfaces the declared scope.
	rec = doJSON(t, e, http.MethodGet, propsURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list props: %d %s", rec.Code, rec.Body.String())
	}
	var list api.PropertiesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	scopes := map[string]string{}
	for _, p := range list.Properties {
		scopes[p.Id] = p.Scope
	}
	if scopes[titleProp.PropId] != "synced" {
		t.Errorf("title scope = %q, want synced", scopes[titleProp.PropId])
	}
	if scopes[pinProp.PropId] != "local" {
		t.Errorf("pin scope = %q, want local", scopes[pinProp.PropId])
	}

	// Value writes auto-route by declared scope: a local value commits
	// with a VersionId but no DAG ChangeId.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		fmt.Sprintf(`{"type":%q}`, typeResp.TypeId))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var obj api.ObjectsCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &obj)

	setURL := fmt.Sprintf("/v1/spaces/%s/properties/%s/set/%s", sp.Id, obj.ObjectId, typeResp.TypeId)
	rec = doJSON(t, e, http.MethodPost, setURL,
		fmt.Sprintf(`{"patch":{%q:true}}`, pinProp.PropId))
	if rec.Code != http.StatusOK {
		t.Fatalf("set local prop: %d %s", rec.Code, rec.Body.String())
	}
	res := decodeModifyResult(t, rec.Body.Bytes())
	if res.VersionId == "" {
		t.Errorf("local set: empty versionId: %+v", res)
	}
	if res.ChangeId != "" {
		t.Errorf("local set: got DAG changeId %q, local writes must not mint one", res.ChangeId)
	}
}

// TestServer_ModifyLocalScope_ChatUnread exercises the local-scope
// record write route end-to-end on the chat built-in: the schema
// declares device-local unread flags on chat_messages, POST /modify
// with {"scope":"local"} flips them, and the query path filters on
// them — the app-side half of read-tracking materialization.
func TestServer_ModifyLocalScope_ChatUnread(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	first := chatSend(t, e, base, "first", "")
	second := chatSend(t, e, base, "second", "")

	modifyURL := "/v1/spaces/" + spaceId + "/modify"
	localFlag := func(msgId string, val bool) string {
		return fmt.Sprintf(`{
			"objectId": %q, "dataset": "chat_messages", "scope": "local",
			"records": [{"id": %q, "ops": [{"type": "$set", "path": "unread", "value": %v}]}]
		}`, objectId, msgId, val)
	}

	// Flag the second message unread on this device.
	rec := doJSON(t, e, http.MethodPost, modifyURL, localFlag(second.Id, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("local modify: %d %s", rec.Code, rec.Body.String())
	}
	res := decodeModifyResult(t, rec.Body.Bytes())
	if len(res.Rejections) > 0 {
		t.Fatalf("local modify rejected: %+v", res.Rejections)
	}
	if res.VersionId == "" {
		t.Errorf("local modify: empty versionId")
	}
	if res.ChangeId != "" {
		t.Errorf("local modify: got DAG changeId %q, local writes must not mint one", res.ChangeId)
	}

	// The flag is a normal record field on the read path: filterable.
	qBody, _ := json.Marshal(map[string]any{
		"objectId": objectId,
		"dataset":  "chat_messages",
		"filter":   map[string]any{"unread": true},
		"sort":     []string{"_ver.id"},
	})
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query", string(qBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("query unread: %d %s", rec.Code, rec.Body.String())
	}
	var qr struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &qr); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if len(qr.Records) != 1 {
		t.Fatalf("unread filter: got %d records, want 1: %s", len(qr.Records), rec.Body.String())
	}
	got := decodeChatMsg(t, qr.Records[0])
	if got.Id != second.Id {
		t.Errorf("unread filter returned %q, want %q", got.Id, second.Id)
	}

	// Explicit {"scope":"synced"} routes exactly like the absent-scope
	// default — same DAG write (non-empty changeId), the documented
	// default label must never start failing.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "chat_messages", "scope": "synced",
		"records": [{"id": %q, "ops": [{"type": "$set", "path": "text", "value": "first (edited)"}]}]
	}`, objectId, first.Id))
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit synced modify: %d %s", rec.Code, rec.Body.String())
	}
	res = decodeModifyResult(t, rec.Body.Bytes())
	if len(res.Rejections) > 0 {
		t.Fatalf("explicit synced modify rejected: %+v", res.Rejections)
	}
	if res.ChangeId == "" {
		t.Errorf("explicit synced modify: expected a DAG changeId")
	}
	if msg := getChatMsg(t, e, base, first.Id); msg.Text != "first (edited)" {
		t.Errorf("explicit synced modify did not land: text = %q", msg.Text)
	}

	// Route enforcement, both directions.
	// Local write to a synced field: op-level rejection, value intact.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "chat_messages", "scope": "local",
		"records": [{"id": %q, "ops": [{"type": "$set", "path": "text", "value": "smuggled"}]}]
	}`, objectId, first.Id))
	if rec.Code != http.StatusOK {
		t.Fatalf("local-to-synced modify: %d %s", rec.Code, rec.Body.String())
	}
	res = decodeModifyResult(t, rec.Body.Bytes())
	if len(res.Rejections) == 0 {
		t.Errorf("local write to synced field: expected op rejection, got %+v", res)
	}
	if msg := getChatMsg(t, e, base, first.Id); msg.Text != "first (edited)" {
		t.Errorf("rejected local op landed: text = %q", msg.Text)
	}

	// Synced write to the local field: the whole change is refused (400).
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "chat_messages",
		"records": [{"id": %q, "ops": [{"type": "$set", "path": "unread", "value": false}]}]
	}`, objectId, second.Id))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("synced write to local field: status=%d, want 400: %s", rec.Code, rec.Body.String())
	}

	// Boundary validation: unsupported scopes and local-scope shape.
	for name, body := range map[string]string{
		"account scope": fmt.Sprintf(`{"objectId": %q, "dataset": "chat_messages", "scope": "account",
			"records": [{"id": %q, "ops": [{"type": "$set", "path": "unread", "value": true}]}]}`, objectId, second.Id),
		"bogus scope": fmt.Sprintf(`{"objectId": %q, "dataset": "chat_messages", "scope": "sideways",
			"records": [{"id": %q, "ops": [{"type": "$set", "path": "unread", "value": true}]}]}`, objectId, second.Id),
		"local upsert": fmt.Sprintf(`{"objectId": %q, "dataset": "chat_messages", "scope": "local",
			"records": [{"id": "fresh", "upsert": true, "ops": [{"type": "$set", "path": "unread", "value": true}]}]}`, objectId),
		"local empty id": fmt.Sprintf(`{"objectId": %q, "dataset": "chat_messages", "scope": "local",
			"records": [{"ops": [{"type": "$set", "path": "unread", "value": true}]}]}`, objectId),
		"local traceIds": fmt.Sprintf(`{"objectId": %q, "dataset": "chat_messages", "scope": "local", "traceIds": ["t1"],
			"records": [{"id": %q, "ops": [{"type": "$set", "path": "unread", "value": true}]}]}`, objectId, second.Id),
		"local objects dataset": fmt.Sprintf(`{"objectId": %q, "dataset": "objects", "scope": "local",
			"records": [{"id": %q, "ops": [{"type": "$set", "path": "sometype.someprop", "value": true}]}]}`, objectId, objectId),
	} {
		rec = doJSON(t, e, http.MethodPost, modifyURL, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400: %s", name, rec.Code, rec.Body.String())
		}
	}

	// Clearing the flag via local $unset drops it from the filter.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "chat_messages", "scope": "local",
		"records": [{"id": %q, "ops": [{"type": "$unset", "path": "unread"}]}]
	}`, objectId, second.Id))
	if rec.Code != http.StatusOK {
		t.Fatalf("local unset: %d %s", rec.Code, rec.Body.String())
	}
	res = decodeModifyResult(t, rec.Body.Bytes())
	if len(res.Rejections) > 0 {
		t.Fatalf("local unset rejected: %+v", res.Rejections)
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/query", string(qBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("query after unset: %d %s", rec.Code, rec.Body.String())
	}
	qr.Records = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &qr); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if len(qr.Records) != 0 {
		t.Errorf("after unset: got %d unread records, want 0", len(qr.Records))
	}

	// Discovery: the datasets doc reports the flag as x-scope local.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/datasets", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("datasets: %d %s", rec.Code, rec.Body.String())
	}
	var ds struct {
		Datasets []struct {
			Name   string `json:"name"`
			Schema struct {
				Properties map[string]struct {
					XScope string `json:"x-scope"`
				} `json:"properties"`
			} `json:"schema"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ds); err != nil {
		t.Fatalf("decode datasets: %v", err)
	}
	found := false
	for _, d := range ds.Datasets {
		if d.Name != "chat_messages" {
			continue
		}
		found = true
		if sc := d.Schema.Properties["unread"].XScope; sc != "local" {
			t.Errorf("chat_messages.unread x-scope = %q, want local", sc)
		}
	}
	if !found {
		t.Error("chat_messages dataset missing from discovery")
	}
}
