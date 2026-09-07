package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_CatalogEveryUsecaseRootTypes sets every catalog usecase up
// on one real server and pins what each root carries, from its
// declaration alone: the type marker iff it declares a type, `miniapp`
// iff it is one, and its OWN id iff the catalog says selfTyped. A type
// objects carry (wiki, person) is a definition and not an instance of
// itself — it never answers a query for its type and takes none of
// its own parts; a self-typed root (the chat, the contacts layouts
// host) is the instance and takes them.
func TestE2E_CatalogEveryUsecaseRootTypes(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	bin := buildBinary(t)
	p := startPeer(t, bin, "owner")
	defer p.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, p.base+"/v1/spaces", `{"name":"every usecase"}`, http.StatusCreated, &sp)

	var list api.CatalogListResponse
	mustJSON(t, http.MethodGet, p.base+"/v1/catalog", "", http.StatusOK, &list)
	roots := map[string]string{}
	for _, u := range list.Usecases {
		var res api.CatalogSetupResponse
		mustJSON(t, http.MethodPost, p.base+"/v1/catalog/"+u.Id+"/setup", `{"spaceId":"`+sp.Id+`"}`, http.StatusOK, &res)
		for _, b := range res.Bundles {
			if prev, seen := roots[b.Id]; seen && (b.Installed || prev != b.Bundle.RootId) {
				t.Fatalf("%s re-installed during %s: %+v", b.Id, u.Id, b)
			}
			roots[b.Id] = b.Bundle.RootId
		}
	}

	typesOf := func(objectId string) []string {
		resp, raw := doRequest(t, http.MethodPost, p.base+"/v1/spaces/"+sp.Id+"/objects/query",
			`{"filter":{"id":"`+objectId+`"}}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("query %s: %d %s", objectId, resp.StatusCode, raw)
		}
		var q struct {
			Records []struct {
				Any struct {
					Types []string `json:"types"`
				} `json:"any"`
			} `json:"records"`
		}
		if err := json.Unmarshal(raw, &q); err != nil || len(q.Records) != 1 {
			t.Fatalf("query %s: %v %s", objectId, err, raw)
		}
		return q.Records[0].Any.Types
	}

	var selfTyped, typeOnly []string
	for _, u := range list.Usecases {
		for _, b := range u.Bundles {
			types := typesOf(roots[b.Id])
			declares := b.Type != nil || len(b.Parts) > 0
			if slices.Contains(types, "__type__") != declares {
				t.Fatalf("%s: types %v, declares=%v", b.Id, types, declares)
			}
			if slices.Contains(types, "miniapp") != (b.Miniapp != nil) {
				t.Fatalf("%s: types %v, miniapp=%v", b.Id, types, b.Miniapp != nil)
			}
			if slices.Contains(types, roots[b.Id]) != b.SelfTyped {
				t.Fatalf("%s: types %v, selfTyped=%v", b.Id, types, b.SelfTyped)
			}
			if b.SelfTyped {
				selfTyped = append(selfTyped, b.Id)
			} else if declares {
				typeOnly = append(typeOnly, b.Id)
			}
		}
	}
	slices.Sort(selfTyped)
	if !slices.Equal(selfTyped, []string{"system:contacts/v1", "system:general-chat/v1"}) {
		t.Fatalf("self-typed roots = %v", selfTyped)
	}

	// Definitions answer no query for their type and refuse their own
	// parts; instances take them.
	for _, id := range typeOnly {
		resp, raw := doRequest(t, http.MethodPost, p.base+"/v1/spaces/"+sp.Id+"/objects/query",
			`{"filter":{"any.types":"`+roots[id]+`"}}`)
		var q api.QueryResponse
		_ = json.Unmarshal(raw, &q)
		if resp.StatusCode != http.StatusOK || len(q.Records) != 0 {
			t.Fatalf("%s: a query for the type returned its definition: %d %s", id, resp.StatusCode, raw)
		}
	}
	resp, raw := doRequest(t, http.MethodPost,
		p.base+"/v1/spaces/"+sp.Id+"/objects/"+roots["system:person/v1"]+"/editor/editor_blocks/blocks",
		`{"type":"paragraph","text":"not a person"}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "dataset.not_declared") {
		t.Fatalf("editor write on the person definition: %d %s", resp.StatusCode, raw)
	}
	resp, raw = doRequest(t, http.MethodPost,
		p.base+"/v1/spaces/"+sp.Id+"/objects/"+roots["system:general-chat/v1"]+"/chat/messages", `{"text":"hi"}`)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("chat send on the chat root: %d %s", resp.StatusCode, raw)
	}
	contacts := roots["system:contacts/v1"]
	resp, raw = doRequest(t, http.MethodPost, p.base+"/v1/spaces/"+sp.Id+"/upsert",
		`{"objectId":"`+contacts+`","dataset":"`+contacts+`_layouts","records":[{"id":"person","fields":{"blocks":["a"]}}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upsert layouts on the contacts root: %d %s", resp.StatusCode, raw)
	}
}
