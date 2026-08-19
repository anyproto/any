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

	t.Run("setup registered in the bundles registry", func(t *testing.T) {
		// Materializing bao runs the creation side of the setup split:
		// its bundle is installed, and the space's general chat is the
		// winning root of its own bundle. Both rows are readable
		// through the generic dataset surface on the spaceIndex object.
		var info map[string]any
		mustJSON(t, http.MethodGet, base+"/v1/spaces/"+baoId, "", http.StatusOK, &info)
		indexId, _ := info["spaceIndexObjectId"].(string)
		chatId, _ := info["generalChatObjectId"].(string)
		if indexId == "" || chatId == "" {
			t.Fatalf("space response missing ids: %+v", info)
		}

		var out struct {
			Records []struct {
				Id     string   `json:"id"`
				RootId string   `json:"rootId"`
				Roots  []string `json:"roots"`
			} `json:"records"`
		}
		mustJSON(t, http.MethodPost, base+"/v1/spaces/"+baoId+"/query",
			`{"objectId":"`+indexId+`","dataset":"bundles"}`, http.StatusOK, &out)

		rows := map[string]string{}
		for _, r := range out.Records {
			if r.RootId == "" {
				t.Errorf("bundle %q has no rootId: %+v", r.Id, r)
			}
			if len(r.Roots) == 0 || r.Roots[0] != r.RootId {
				t.Errorf("bundle %q winner not claimed in roots: %+v", r.Id, r)
			}
			rows[r.Id] = r.RootId
		}
		if rows["bao/v1"] == "" {
			t.Errorf("bao bundle not registered: %+v", out.Records)
		}
		if rows["general-chat/v1"] != chatId {
			t.Errorf("general chat bundle root %q != generalChatObjectId %q", rows["general-chat/v1"], chatId)
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
