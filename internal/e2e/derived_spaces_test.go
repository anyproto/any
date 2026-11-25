// TestE2E_DerivedSpaces pins the well-known derived-space surface on an
// account that has no derived space, which is every new installation:
// GET /v1/spaces/derived resolves the embedded registry to
// deterministic ids and creates nothing, POST /v1/spaces/derived/:name
// creates nothing either, and the id stays undeletable. The account's
// bao space is a regular space created for that use. What the server
// does with a derived space an account already has is pinned in the
// server package (TestDerived_*).
// Contract in docs/03-api.md § Spaces (Derived spaces).
package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

func TestE2E_DerivedSpaces(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)
	srv := startServer(t, bin, addr, dataDir)
	defer srv.stop(t)
	waitForReady(t, addr, 30*time.Second)

	base := "http://" + addr

	type derivedRow struct {
		Name    string `json:"name"`
		SpaceId string `json:"spaceId"`
		Created bool   `json:"created"`
		Status  string `json:"status"`
	}
	type derivedList struct {
		Spaces []derivedRow `json:"spaces"`
	}

	// Resolve bao's id up front — every subtest depends on it, so a
	// registry failure aborts the whole test instead of cascading.
	var baoId string
	{
		var list derivedList
		mustJSON(t, http.MethodGet, base+"/v1/spaces/derived", "", http.StatusOK, &list)
		for _, row := range list.Spaces {
			if row.SpaceId == "" {
				t.Fatalf("row %q has empty spaceId", row.Name)
			}
			if row.Created || row.Status != "" {
				t.Fatalf("row %q reports created/status before any materialization: %+v", row.Name, row)
			}
			if row.Name == "bao" {
				baoId = row.SpaceId
			}
		}
		if baoId == "" {
			t.Fatalf("registry has no bao entry: %+v", list)
		}
	}

	t.Run("delete before materialization refused", func(t *testing.T) {
		var env map[string]any
		mustJSON(t, http.MethodDelete, base+"/v1/spaces/"+baoId, "", http.StatusConflict, &env)
		errObj, _ := env["error"].(map[string]any)
		if errObj["code"] != "space.derived_undeletable" {
			t.Errorf("code = %v, want space.derived_undeletable", errObj["code"])
		}
	})

	t.Run("unknown name is 404", func(t *testing.T) {
		var env map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/derived/nope", "", http.StatusNotFound, &env)
		errObj, _ := env["error"].(map[string]any)
		if errObj["code"] != "space.derived_unknown" {
			t.Errorf("code = %v, want space.derived_unknown", errObj["code"])
		}
	})

	t.Run("the route creates nothing", func(t *testing.T) {
		var env map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/derived/bao", "", http.StatusNotFound, &env)
		errObj, _ := env["error"].(map[string]any)
		if errObj["code"] != "space.derived_not_created" {
			t.Errorf("code = %v, want space.derived_not_created", errObj["code"])
		}
		var list derivedList
		mustJSON(t, http.MethodGet, base+"/v1/spaces/derived", "", http.StatusOK, &list)
		for _, row := range list.Spaces {
			if row.Created || row.Status != "" {
				t.Errorf("row %q after the refused call: %+v", row.Name, row)
			}
		}
	})

	// The bao space of a new installation: a regular space, created
	// for the bao use.
	var bao api.SpaceInfo
	t.Run("the bao space is a regular space", func(t *testing.T) {
		mustJSON(t, http.MethodPost, base+"/v1/spaces", `{"name":"bao","anySpaceType":"bao"}`, http.StatusCreated, &bao)
		if bao.Id == "" || bao.Id == baoId || bao.AnySpaceType != "bao" || bao.Derived {
			t.Fatalf("created bao space: %+v", bao)
		}
		var list api.SpaceListResponse
		mustJSON(t, http.MethodGet, base+"/v1/spaces", "", http.StatusOK, &list)
		found := false
		for _, row := range list.Spaces {
			if row.AnySpaceType == "bao" {
				found = row.Id == bao.Id
			}
		}
		if !found {
			t.Fatalf("the space list does not find the bao space by its use: %+v", list.Spaces)
		}
	})
	if bao.Id == "" {
		t.Fatal("no bao space to go on with")
	}

	t.Run("setup is the client's to register", func(t *testing.T) {
		// Creating the space installs nothing: the server keeps no
		// catalog, so its registry is empty until a client ensures its
		// own bundle.
		var empty api.BundleListResponse
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+bao.Id+"/bundles", "", http.StatusOK, &empty)
		if len(empty.Bundles) != 0 {
			t.Fatalf("a new space pre-installed bundles: %+v", empty.Bundles)
		}

		var res api.BundleEnsureResponse
		mustJSON(t, http.MethodPost, base+"/v1/spaces/"+bao.Id+"/bundles",
			`{"id":"bao/v1","name":"bao","parts":`+modulePartsBody("editor")+`}`,
			http.StatusOK, &res)
		if !res.Installed || res.Bundle.RootId == "" {
			t.Fatalf("ensure did not install: %+v", res)
		}

		var list api.BundleListResponse
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+bao.Id+"/bundles", "", http.StatusOK, &list)
		if len(list.Bundles) != 1 || list.Bundles[0].Id != "bao/v1" ||
			list.Bundles[0].RootId != res.Bundle.RootId || list.Bundles[0].Name != "bao" {
			t.Fatalf("registry after install: %+v", list.Bundles)
		}
	})
}
