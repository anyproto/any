// TestE2E_DerivedSpaces pins the well-known derived-space surface:
// GET /v1/spaces/derived resolves the embedded registry to
// deterministic ids without creating anything; POST
// /v1/spaces/derived/:name materializes lazily and idempotently,
// seeding the registry display name; and derived spaces are permanent
// — DELETE refuses with 409 space.derived_undeletable both before
// materialization (registry pre-check) and after (SDK row-flag guard).
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

	t.Run("create materializes at the resolved id", func(t *testing.T) {
		var info map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/derived/bao", "", http.StatusCreated, &info)
		if info["id"] != baoId {
			t.Errorf("materialized id %v != resolved id %v", info["id"], baoId)
		}
		if info["derived"] != true {
			t.Errorf("derived flag missing on SpaceInfo: %+v", info)
		}

		// Idempotent: same id on repeat.
		var again map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/derived/bao", "", http.StatusCreated, &again)
		if again["id"] != baoId {
			t.Errorf("repeat materialization id %v != %v", again["id"], baoId)
		}
	})

	t.Run("setup is the client's to register", func(t *testing.T) {
		// Materializing a derived space installs nothing: the server
		// keeps no catalog, so its registry is empty until a client
		// ensures its own bundle.
		var empty api.BundleListResponse
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+baoId+"/bundles", "", http.StatusOK, &empty)
		if len(empty.Bundles) != 0 {
			t.Fatalf("derived space pre-installed bundles: %+v", empty.Bundles)
		}

		var res api.BundleEnsureResponse
		mustJSON(t, http.MethodPost, base+"/v1/spaces/"+baoId+"/bundles",
			`{"id":"bao/v1","name":"bao","parts":`+modulePartsBody("editor")+`}`,
			http.StatusOK, &res)
		if !res.Installed || res.Bundle.RootId == "" {
			t.Fatalf("ensure did not install: %+v", res)
		}

		var list api.BundleListResponse
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+baoId+"/bundles", "", http.StatusOK, &list)
		if len(list.Bundles) != 1 || list.Bundles[0].Id != "bao/v1" ||
			list.Bundles[0].RootId != res.Bundle.RootId || list.Bundles[0].Name != "bao" {
			t.Fatalf("registry after install: %+v", list.Bundles)
		}
	})

	t.Run("display name seeded", func(t *testing.T) {
		// SetMetadata mirrors back to the row asynchronously — poll.
		deadline := time.Now().Add(10 * time.Second)
		for {
			var info map[string]any
			mustJSON(t, http.MethodGet, base+"/v1/spaces/"+baoId, "", http.StatusOK, &info)
			if info["name"] == "bao" {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("name = %v, want bao (registry DisplayName)", info["name"])
			}
			time.Sleep(200 * time.Millisecond)
		}
	})

	t.Run("list reports created with status", func(t *testing.T) {
		var list derivedList
		mustJSON(t, http.MethodGet, base+"/v1/spaces/derived", "", http.StatusOK, &list)
		for _, row := range list.Spaces {
			if row.Name != "bao" {
				continue
			}
			if !row.Created || row.Status != "active" {
				t.Errorf("bao row after materialization = %+v, want created=true status=active", row)
			}
		}
	})

	t.Run("delete after materialization refused", func(t *testing.T) {
		var env map[string]any
		mustJSON(t, http.MethodDelete, base+"/v1/spaces/"+baoId, "", http.StatusConflict, &env)
		errObj, _ := env["error"].(map[string]any)
		if errObj["code"] != "space.derived_undeletable" {
			t.Errorf("code = %v, want space.derived_undeletable", errObj["code"])
		}
		// The row survives, active and flagged.
		var info map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+baoId, "", http.StatusOK, &info)
		if info["status"] != "active" {
			t.Errorf("status = %v, want active", info["status"])
		}
		if info["derived"] != true {
			t.Errorf("derived flag lost after refused delete: %+v", info)
		}
	})
}
