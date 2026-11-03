package e2e

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// Module-declaring types for the e2e suite: a chat or editor
// collection is writable on an object only when one of its types
// declares the module in a part. The tests mint one plain user type
// per space and module with a single part declaring it; the general
// chat (setupGeneralChat) is the catalog's derived root, used where a
// test is about that root. A user type's declaration is its own tree,
// so a joiner waits for it (waitChatDeclared) before its first write.

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
		fmt.Sprintf(`{"key":%q,"datasets":[{"module":%q}]}`, module, module),
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
		fmt.Sprintf(`{"type":%q}`, typeId), http.StatusCreated, &resp)
	id, _ := resp["objectId"].(string)
	if id == "" {
		t.Fatalf("createModuleObject %s: objectId empty: %+v", module, resp)
	}
	return id
}

// modulePartsBody is the bundle `parts` declaration installing one
// module part on the bundle root — the shape a client's
// document bundle carries.
func modulePartsBody(module string) string {
	return fmt.Sprintf(`[{"key":%q,"datasets":[{"module":%q}]}]`, module, module)
}

// setupGeneralChat runs the catalog's general-chat setup against the
// given peer and returns the derived chat root.
func setupGeneralChat(t *testing.T, base, spaceId string) string {
	t.Helper()
	var out api.CatalogSetupResponse
	mustJSON(t, http.MethodPost, base+"/v1/catalog/general-chat/setup",
		fmt.Sprintf(`{"spaceId":%q}`, spaceId), http.StatusOK, &out)
	for _, b := range out.Bundles {
		if b.Id == "system:general-chat/v1" && b.Bundle.RootId != "" {
			return b.Bundle.RootId
		}
	}
	t.Fatalf("setup general-chat: no chat root in %+v", out)
	return ""
}

// waitChatDeclared blocks until the peer at base lists the space's chat
// type among the chat_messages owners: the declaration is its own tree,
// so a joiner can hold a message before the part that admits its
// writes.
func waitChatDeclared(t *testing.T, base, spaceId string) {
	t.Helper()
	e2eModuleTypesMu.Lock()
	typeId := e2eModuleTypes[spaceId+"|chat"]
	e2eModuleTypesMu.Unlock()
	if typeId == "" {
		t.Fatalf("waitChatDeclared: no chat type installed in %s", spaceId)
	}
	if !pollUntil(3*time.Minute, func() bool {
		var ds api.DatasetsResponse
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+spaceId+"/datasets", "", http.StatusOK, &ds)
		for _, s := range ds.Datasets {
			if s.Name == "chat_messages" && slices.Contains(s.Owners, typeId) {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("peer never saw chat type %s declared in %s", typeId, spaceId)
	}
}
