// TestE2E_DerivedSpaces pins the well-known derived-space surface:
// GET /v1/spaces/derived resolves the embedded registry to
// deterministic ids without creating anything; POST
// /v1/spaces/derived/:name materializes lazily and idempotently; and
// derived spaces are permanent — DELETE refuses with 409
// space.derived_undeletable both before materialization (registry
// pre-check) and after (SDK row-flag guard). Contract in docs/03-api.md
// § Spaces (Derived spaces).
package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"
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
	}
	type derivedList struct {
		Spaces []derivedRow `json:"spaces"`
	}

	var baoId string
	t.Run("list resolves ids without creating", func(t *testing.T) {
		var list derivedList
		mustJSON(t, http.MethodGet, base+"/v1/spaces/derived", "", http.StatusOK, &list)
		if len(list.Spaces) == 0 {
			t.Fatalf("registry empty: %+v", list)
		}
		for _, row := range list.Spaces {
			if row.Name == "bao" {
				baoId = row.SpaceId
			}
			if row.SpaceId == "" {
				t.Errorf("row %q has empty spaceId", row.Name)
			}
			if row.Created {
				t.Errorf("row %q reports created before any materialization", row.Name)
			}
		}
		if baoId == "" {
			t.Fatalf("registry has no bao entry: %+v", list)
		}
	})

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
		if info["generalChatObjectId"] == nil || info["generalChatObjectId"] == "" {
			t.Errorf("single-space response missing generalChatObjectId: %+v", info)
		}

		// Idempotent: same id on repeat.
		var again map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/derived/bao", "", http.StatusCreated, &again)
		if again["id"] != baoId {
			t.Errorf("repeat materialization id %v != %v", again["id"], baoId)
		}
	})

	t.Run("list reports created", func(t *testing.T) {
		var list derivedList
		mustJSON(t, http.MethodGet, base+"/v1/spaces/derived", "", http.StatusOK, &list)
		for _, row := range list.Spaces {
			if row.Name == "bao" && !row.Created {
				t.Errorf("bao still reports created=false after materialization")
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
