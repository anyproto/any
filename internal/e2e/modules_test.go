package e2e

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// Module-declaring types for the e2e suite: a chat or editor
// collection is writable on an object only when one of its types
// declares the module in a part. Production clients register a bundle
// (chat/v1, page/v1); the tests mint one plain user type per space
// and module, with a single shared part.

var (
	e2eModuleTypesMu sync.Mutex
	e2eModuleTypes   = map[string]string{} // spaceId + "|" + module → typeId
)

// installModuleType creates (once per space and module) a user type
// whose single part shares the module's canonical collection. Always
// called against the peer that created the space.
func installModuleType(t *testing.T, base, spaceId, module string) string {
	t.Helper()
	key := spaceId + "|" + module
	e2eModuleTypesMu.Lock()
	defer e2eModuleTypesMu.Unlock()
	if id, ok := e2eModuleTypes[key]; ok {
		return id
	}
	var created map[string]any
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceId+"/types",
		fmt.Sprintf(`{"name":%q,"xKey":%q}`, module+" host", module+"_host"), http.StatusCreated, &created)
	typeId, _ := created["typeId"].(string)
	if typeId == "" {
		t.Fatalf("installModuleType %s: typeId empty: %+v", module, created)
	}
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceId+"/types/"+typeId+"/parts",
		fmt.Sprintf(`{"key":%q,"datasets":[{"module":%q,"shared":true}]}`, module, module),
		http.StatusCreated, new(map[string]any))
	e2eModuleTypes[key] = typeId
	return typeId
}

// createModuleObject creates an object carrying the module type and
// returns its id.
func createModuleObject(t *testing.T, base, spaceId, module string) string {
	t.Helper()
	typeId := installModuleType(t, base, spaceId, module)
	var resp map[string]any
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceId+"/objects",
		fmt.Sprintf(`{"types":[%q]}`, typeId), http.StatusCreated, &resp)
	id, _ := resp["objectId"].(string)
	if id == "" {
		t.Fatalf("createModuleObject %s: objectId empty: %+v", module, resp)
	}
	return id
}

// modulePartsBody is the bundle `parts` declaration installing one
// shared module part on the bundle root — the shape the well-known
// chat/v1 and page/v1 bundles carry.
func modulePartsBody(module string) string {
	return fmt.Sprintf(`[{"key":%q,"datasets":[{"module":%q,"shared":true}]}]`, module, module)
}
