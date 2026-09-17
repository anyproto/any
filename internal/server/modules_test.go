package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// Module-declaring types for tests. A chat or editor collection is
// writable on an object only when one of the object's types declares
// the module in a part. For the editor the tests mint a plain user
// type with one shared part; for chat — a reserved module — the only
// declaration is the catalog's general-chat install, so the chat
// "type" is the general-chat root and the chat object is the root
// itself (one chat per space).

var (
	moduleTypesMu sync.Mutex
	moduleTypes   = map[string]string{} // spaceId + "|" + module → typeId
)

// installModuleType creates (once per space and module) a user type
// whose single part shares the module's canonical collection and
// returns its id.
func installModuleType(t testing.TB, e http.Handler, spaceId, module string) string {
	t.Helper()
	key := spaceId + "|" + module
	moduleTypesMu.Lock()
	defer moduleTypesMu.Unlock()
	if id, ok := moduleTypes[key]; ok {
		return id
	}
	if module == "chat" {
		root := generalChatRoot(t, e, spaceId)
		moduleTypes[key] = root
		return root
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types",
		`{"name":"`+module+` host","xKey":"`+module+`_host","layout":{"type":"page"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %s type: %d %s", module, rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types/"+created.TypeId+"/parts",
		`{"key":"`+module+`","name":"`+module+`","datasets":[{"module":"`+module+`","shared":true}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("declare %s part: %d %s", module, rec.Code, rec.Body.String())
	}
	moduleTypes[key] = created.TypeId
	return created.TypeId
}

// plainType creates (once per space) a user type with no parts: every
// object has a type, and this is the one that declares no dataset —
// what a test carries when it needs the write gate to refuse.
func plainType(t testing.TB, e http.Handler, spaceId string) string {
	t.Helper()
	key := spaceId + "|plain"
	moduleTypesMu.Lock()
	defer moduleTypesMu.Unlock()
	if id, ok := moduleTypes[key]; ok {
		return id
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types",
		`{"name":"Plain","xKey":"plain"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plain type: %d %s", rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	moduleTypes[key] = created.TypeId
	return created.TypeId
}

// mustCreateModuleObject creates an object carrying the module type
// (installing the type on first use) and returns its id.
func mustCreateModuleObject(t testing.TB, e http.Handler, spaceId, module string) string {
	t.Helper()
	typeId := installModuleType(t, e, spaceId, module)
	if module == "chat" {
		return typeId // the general-chat root is the space's one chat
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects", `{"type":"`+typeId+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %s object: %d %s", module, rec.Code, rec.Body.String())
	}
	var obj api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	return obj.ObjectId
}

// mustAddPart declares a part on a type and returns its id.
func mustAddPart(t testing.TB, e http.Handler, spaceId, typeId, body string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types/"+typeId+"/parts", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add part: %d %s", rec.Code, rec.Body.String())
	}
	var out api.AddPartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.PartId == "" {
		t.Fatalf("decode part: %v %s", err, rec.Body.String())
	}
	return out.PartId
}

// firstPartId returns the id of the type's first part.
func firstPartId(t testing.TB, e http.Handler, spaceId, typeId string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/types/"+typeId+"/parts", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list parts: %d %s", rec.Code, rec.Body.String())
	}
	var parts api.TypePartsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &parts); err != nil || len(parts.Parts) == 0 {
		t.Fatalf("decode parts: %v %s", err, rec.Body.String())
	}
	return parts.Parts[0].Id
}

// generalChatRoot sets the catalog's general-chat usecase up in the
// space and returns the derived root — the space's chat object and,
// being self-typed, the one type declaring the chat module.
func generalChatRoot(t testing.TB, e http.Handler, spaceId string) string {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/catalog/general-chat/setup", `{"spaceId":"`+spaceId+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup general-chat: %d %s", rec.Code, rec.Body.String())
	}
	var out api.CatalogSetupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode setup: %v", err)
	}
	for _, b := range out.Bundles {
		if b.Id == generalChatBundleId {
			return b.Bundle.RootId
		}
	}
	t.Fatalf("setup general-chat returned no %s: %+v", generalChatBundleId, out)
	return ""
}

// generalChatBundleId is the catalog's chat install.
const generalChatBundleId = "system:general-chat/v1"
