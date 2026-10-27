package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_UserChatType pins the chat module as an ordinary part
// module: a user type declares `{"module": "chat"}` next to an editor
// part, each of its objects is a chat with a body, a bundle may declare
// it, every declaring type owns the shared chat_messages collection,
// and the catalog's general chat still installs as the derived hidden
// root it is. The module admits only its canonical dataset, and a copy
// (`from`) of a chat-bearing object starts with an empty transcript.
func TestServer_UserChatType(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "UserChat")
	base := "/v1/spaces/" + sp.Id

	rec := doJSON(t, e, http.MethodPost, base+"/types", `{"name":"Room","xKey":"room"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	typeId := created.TypeId
	mustAddPart(t, e, sp.Id, typeId, `{"key":"body","datasets":[{"module":"editor"}]}`)
	mustAddPart(t, e, sp.Id, typeId, `{"key":"chat","datasets":[{"module":"chat"}]}`)
	// Canonical-only: a keyed chat dataset is refused.
	rec = doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts",
		`{"key":"thread","datasets":[{"key":"thread","module":"chat"}]}`)
	assertStatusCode(t, rec, http.StatusBadRequest, "dataset.decl_invalid")

	// Each object of the type is a chat with a body.
	room := mustCreateObject(t, e, sp.Id, `{"type":"`+typeId+`"}`)
	rec = doJSON(t, e, http.MethodPut, base+"/objects/"+room+"/editor/editor_blocks/markdown", `{"content":"# Agenda"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT body: %d %s", rec.Code, rec.Body.String())
	}
	chatSend(t, e, base+"/objects/"+room, "hello room", "")
	other := mustCreateObject(t, e, sp.Id, `{"type":"`+typeId+`"}`)
	chatSend(t, e, base+"/objects/"+other, "hello other", "")
	if n := len(chatList(t, e, base+"/objects/"+room).Messages); n != 1 {
		t.Fatalf("room messages = %d, want 1", n)
	}
	// A bundle declares the module like any other.
	bundle := ensureBundle(t, e, sp.Id, `{"id":"room/v1","parts":[{"key":"chat","datasets":[{"module":"chat"}]}]}`)
	if !bundle.Installed {
		t.Fatalf("bundle install: %+v", bundle)
	}
	chatSend(t, e, base+"/objects/"+bundle.Bundle.RootId, "hello bundle", "")

	// The catalog's general chat: derived, hidden, its own type.
	setup := setupUsecase(t, e, "general-chat", sp.Id)
	if len(setup.Bundles) != 1 || setup.Bundles[0].Id != generalChatBundleId {
		t.Fatalf("setup bundles = %+v", setup.Bundles)
	}
	general := setup.Bundles[0]
	if !general.Installed || !general.Bundle.Derived || general.Bundle.RootId == "" || general.TypeId != general.Bundle.RootId {
		t.Fatalf("general chat install: %+v", general)
	}
	root := general.Bundle.RootId
	var info api.TypeInfo
	decodeGet(t, e, base+"/types/"+root, &info)
	if !info.Hidden || info.XKey != "general_chat" {
		t.Fatalf("general chat type: %+v", info)
	}
	chatSend(t, e, base+"/objects/"+root, "hello general", "")
	if again := setupUsecase(t, e, "general-chat", sp.Id); again.Bundles[0].Installed || again.Bundles[0].Bundle.RootId != root {
		t.Fatalf("second setup: %+v", again.Bundles[0])
	}
	// The general chat's hidden type is an ordinary carrier: an object
	// created with it, or retyped to it, is one more chat with the chat
	// layout — the general chat is the registry's rootId, not a type.
	another := mustCreateObject(t, e, sp.Id, `{"type":"`+root+`"}`)
	chatSend(t, e, base+"/objects/"+another, "hello another", "")
	rec = doJSON(t, e, http.MethodPost, base+"/properties/"+other+"/type/"+root, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("set the general chat's type: %d %s", rec.Code, rec.Body.String())
	}

	// Every declaring type owns the shared collection.
	var ds api.DatasetsResponse
	decodeGet(t, e, base+"/datasets", &ds)
	var owners []string
	for _, s := range ds.Datasets {
		if s.Name == "chat_messages" {
			owners = s.Owners
		}
	}
	want := map[string]bool{typeId: true, bundle.Bundle.RootId: true, root: true}
	if len(owners) != len(want) {
		t.Fatalf("chat_messages owners = %v, want %v", owners, want)
	}
	for _, o := range owners {
		if !want[o] {
			t.Fatalf("chat_messages owners = %v, want %v", owners, want)
		}
	}

	// The write gate is the type's declaration.
	plain := mustCreateObject(t, e, sp.Id, `{"type":"page"}`)
	rec = doJSON(t, e, http.MethodPost, base+"/objects/"+plain+"/chat/messages", `{"text":"nope"}`)
	assertStatusCode(t, rec, http.StatusBadRequest, "dataset.not_declared")

	// A copy carries the body, never the transcript.
	copied := mustCreateObject(t, e, sp.Id, `{"from":"`+room+`"}`)
	if got, want := markdownOf(t, e, sp.Id, copied), markdownOf(t, e, sp.Id, room); got != want {
		t.Errorf("copied body = %q, want %q", got, want)
	}
	if n := len(chatList(t, e, base+"/objects/"+copied).Messages); n != 0 {
		t.Fatalf("copy carries %d messages, want 0", n)
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

// TestServer_DerivedObjectUndeletable pins the delete mapping: a derived
// bundle root (the general chat) is permanent and must say so, rather
// than reporting the object as missing. An ordinary object still
// deletes, and an unknown id still 404s.
func TestServer_DerivedObjectUndeletable(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "DerivedDelete")
	base := "/v1/spaces/" + sp.Id

	chatRoot := generalChatRoot(t, e, sp.Id)
	rec := doJSON(t, e, http.MethodDelete, base+"/objects/"+chatRoot, "")
	assertStatusCode(t, rec, http.StatusConflict, "object.derived_undeletable")
	// The refusal must leave the root readable: the SDK reclaims local
	// state right after its own DeleteTree, so the derived check running
	// first is what this pins.
	doJSONExpect(t, e, http.MethodGet, base+"/objects/"+chatRoot, http.StatusOK)

	rec = doJSON(t, e, http.MethodPost, base+"/objects", `{"type":"page"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var obj api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	doJSONExpect(t, e, http.MethodDelete, base+"/objects/"+obj.ObjectId, http.StatusNoContent)

	rec = doJSON(t, e, http.MethodDelete, base+"/objects/"+obj.ObjectId, "")
	assertStatusCode(t, rec, http.StatusNotFound, "sdk.not_found")
}
