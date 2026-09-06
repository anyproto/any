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
// the module in a part — the production shape is a client-registered
// bundle (page/v1, chat/v1); the tests mint a plain user type with one
// shared part per module instead.

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
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types",
		`{"name":"`+module+` host","xKey":"`+module+`_host","weight":10,"layout":{"type":"page"}}`)
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

// mustCreateModuleObject creates an object carrying the module type
// (installing the type on first use) and returns its id.
func mustCreateModuleObject(t testing.TB, e http.Handler, spaceId, module string) string {
	t.Helper()
	typeId := installModuleType(t, e, spaceId, module)
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects", `{"types":["`+typeId+`"]}`)
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
