package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_ChatModuleReserved pins the general chat under the
// reserved chat module: no client declaration may name the module —
// a part on a user type, a dataset added to a part, a bundle body —
// while the catalog's general-chat setup installs the one chat, a
// derived hidden root that is its own type and takes chat writes.
// That root is the type's only carrier: creating an object with it,
// attaching it, and an any.types op through /modify are all refused,
// and no bare object is left behind by the refused create.
func TestServer_ChatModuleReserved(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "ChatReserved")
	base := "/v1/spaces/" + sp.Id

	// Client declarations of the module.
	rec := doJSON(t, e, http.MethodPost, base+"/types", `{"name":"Room","xKey":"room"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	typeId := created.TypeId
	rec = doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts",
		`{"key":"chat","datasets":[{"module":"chat","shared":true}]}`)
	assertStatusCode(t, rec, http.StatusBadRequest, "dataset.module_reserved")
	partId := mustAddPart(t, e, sp.Id, typeId, `{"key":"body","datasets":[{"module":"editor","shared":true}]}`)
	rec = doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts/"+partId+"/datasets",
		`{"module":"chat","shared":true}`)
	assertStatusCode(t, rec, http.StatusBadRequest, "dataset.module_reserved")
	rec = doJSON(t, e, http.MethodPost, base+"/bundles",
		`{"id":"room/v1","derived":true,"parts":[{"key":"chat","datasets":[{"module":"chat","shared":true}]}]}`)
	assertStatusCode(t, rec, http.StatusBadRequest, "dataset.module_reserved")
	rec = doJSON(t, e, http.MethodGet, base+"/bundles/room%2Fv1", "")
	assertStatusCode(t, rec, http.StatusNotFound, "bundle.not_found")

	// The catalog's install: derived, hidden, self-typed, a chat.
	setup := setupUsecase(t, e, "general-chat", sp.Id)
	if len(setup.Bundles) != 1 || setup.Bundles[0].Id != generalChatBundleId {
		t.Fatalf("setup bundles = %+v", setup.Bundles)
	}
	chat := setup.Bundles[0]
	if !chat.Installed || !chat.Bundle.Derived || chat.Bundle.RootId == "" || chat.TypeId != chat.Bundle.RootId {
		t.Fatalf("general chat install: %+v", chat)
	}
	root := chat.Bundle.RootId
	var info api.TypeInfo
	decodeGet(t, e, base+"/types/"+root, &info)
	if !info.Hidden || info.XKey != "general_chat" {
		t.Fatalf("general chat type: %+v", info)
	}
	if msg := chatSend(t, e, base+"/objects/"+root, "hello general", ""); msg.Id == "" {
		t.Fatalf("root does not take chat writes: %+v", msg)
	}
	var ds api.DatasetsResponse
	decodeGet(t, e, base+"/datasets", &ds)
	var owners []string
	for _, s := range ds.Datasets {
		if s.Name == "chat_messages" {
			owners = s.Owners
		}
	}
	if len(owners) != 1 || owners[0] != root {
		t.Fatalf("chat_messages owners = %v, want [%s]", owners, root)
	}
	// Idempotent: a second setup adopts.
	if again := setupUsecase(t, e, "general-chat", sp.Id); again.Bundles[0].Installed || again.Bundles[0].Bundle.RootId != root {
		t.Fatalf("second setup: %+v", again.Bundles[0])
	}

	// The root is the type's only carrier.
	before := countObjects(t, e, sp.Id)
	rec = doJSON(t, e, http.MethodPost, base+"/objects", `{"types":["`+root+`"]}`)
	assertStatusCode(t, rec, http.StatusBadRequest, "type.reserved_carrier")
	if after := countObjects(t, e, sp.Id); after != before {
		t.Fatalf("refused create left an object: %d → %d", before, after)
	}
	other := mustCreateObject(t, e, sp.Id, `{}`)
	rec = doJSON(t, e, http.MethodPost, base+"/properties/"+other+"/attach/"+root, "")
	assertStatusCode(t, rec, http.StatusBadRequest, "type.reserved_carrier")
	rec = doJSON(t, e, http.MethodPost, base+"/modify",
		`{"objectId":"`+other+`","dataset":"objects","records":[{"id":"`+other+`","ops":[{"type":"$addToSet","path":"any.types","value":"`+root+`"}]}]}`)
	assertStatusCode(t, rec, http.StatusBadRequest, "type.reserved_carrier")
	rec = doJSON(t, e, http.MethodPost, base+"/objects/"+other+"/chat/messages", `{"text":"nope"}`)
	assertStatusCode(t, rec, http.StatusBadRequest, "dataset.not_declared")
	// An editor type stays attachable — the rule is the reserved module's.
	rec = doJSON(t, e, http.MethodPost, base+"/properties/"+other+"/attach/"+typeId, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("attach editor type: %d %s", rec.Code, rec.Body.String())
	}
}

// assertStatusCode checks the status and the error code of a refusal.
func assertStatusCode(t *testing.T, rec *httptest.ResponseRecorder, want int, code string) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
	assertErrorCode(t, rec, code)
}

// countObjects counts the space's objects rows.
func countObjects(t *testing.T, e http.Handler, spaceId string) int {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/query", `{"includeTotal":true,"limit":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("objects query: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	return out.Total
}
