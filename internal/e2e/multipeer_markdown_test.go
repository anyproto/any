package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// TestE2E_MultipeerJoinerMarkdownWrite isolates a reported asymmetry in
// per-object-tree writes: owner→joiner pushes work, joiner→owner pushes
// don't, and the joiner's own subsequent read of his write returns
// records=[]. The test sequence:
//
//  1. Owner creates a space + an object, writes initial markdown.
//  2. Joiner joins, becomes active.
//  3. Joiner reads markdown — expects owner's content.
//  4. Joiner writes new markdown via PUT /markdown — expects 200 with
//     a non-empty inserted list.
//  5. Joiner reads markdown back — expects HIS new content (not the
//     owner's, not empty).
//  6. Owner reads markdown — expects joiner's new content within the
//     usual headsync window.
//
// The interesting failure mode the diagnosis chased: step (5) returns
// the previous content (or empty), step (6) never converges. Either
// would prove the bug is in the SDK's per-tree write path; both passing
// would push the issue out to the middleware (or whatever production
// repro setup the user is on).
func TestE2E_MultipeerJoinerMarkdownWrite(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer test takes ~60s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	// 1. Owner: space + object + initial markdown.
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"md-write"}`, http.StatusCreated, &sp)

	var objResp map[string]any
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &objResp)
	objectID, _ := objResp["objectId"].(string)
	if objectID == "" {
		t.Fatalf("objectId empty: %+v", objResp)
	}

	ownerInitial := "OWNER LINE 1\n\nOWNER LINE 2"
	body, _ := json.Marshal(map[string]string{"content": ownerInitial})
	var setResp map[string]any
	mustJSON(t, http.MethodPut,
		owner.base+"/v1/spaces/"+sp.Id+"/objects/"+objectID+"/editor/markdown",
		string(body), http.StatusOK, &setResp)
	if ins, _ := setResp["inserted"].([]any); len(ins) != 2 {
		t.Fatalf("owner initial set: inserted=%v want 2 entries", ins)
	}

	// 2. Joiner joins as writer.
	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// 3. Joiner reads the owner's markdown — sanity that owner→joiner
	// per-tree sync still works at all on this fixture.
	mdURL := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + objectID + "/editor/markdown"
	if !pollUntil(2*time.Minute, func() bool {
		var got map[string]any
		resp, raw := doRequest(t, http.MethodGet, mdURL, "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			return false
		}
		c, _ := got["content"].(string)
		return c == ownerInitial
	}) {
		t.Fatalf("joiner never saw owner's initial markdown")
	}

	// 4. Joiner writes new markdown.
	joinerNew := "BOB EDIT 1\n\nBOB EDIT 2"
	body, _ = json.Marshal(map[string]string{"content": joinerNew})
	var joinerSet map[string]any
	mustJSON(t, http.MethodPut, mdURL, string(body), http.StatusOK, &joinerSet)
	t.Logf("joiner set: %+v", joinerSet)

	// inserted+updated together must cover at least one of the new
	// blocks. (Owner's "OWNER LINE 1" -> "BOB EDIT 1" is an Update on
	// the same lexid; "OWNER LINE 2" -> "BOB EDIT 2" likewise.)
	insLen := lenAny(joinerSet["inserted"])
	updLen := lenAny(joinerSet["updated"])
	if insLen+updLen == 0 {
		t.Fatalf("joiner set wrote nothing: %+v", joinerSet)
	}

	// 5. Joiner reads back IMMEDIATELY after his own write. This is the
	// critical assertion: a successful PUT must be observable to the
	// same peer that issued it. If this fails, the bug is local
	// projection — the change committed to the tree but didn't land
	// in the materialised collection.
	var got map[string]any
	resp, raw := doRequest(t, http.MethodGet, mdURL, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("joiner read after write: status=%d body=%s", resp.StatusCode, string(raw))
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("joiner read after write: parse: %v body=%s", err, string(raw))
	}
	gotContent, _ := got["content"].(string)
	if !strings.Contains(gotContent, "BOB EDIT 1") || !strings.Contains(gotContent, "BOB EDIT 2") {
		t.Fatalf("joiner can't read his own write back: got %q want to contain BOB EDIT 1 + BOB EDIT 2", gotContent)
	}

	// 6. Owner converges to the joiner's content. The recently-fixed
	// broadcast-ctx bug aside, the only path back to owner is the
	// per-tree HeadUpdate broadcast triggered by AddContent.
	ownerMdURL := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + objectID + "/editor/markdown"
	if !pollUntil(2*time.Minute, func() bool {
		var got map[string]any
		resp, raw := doRequest(t, http.MethodGet, ownerMdURL, "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			return false
		}
		c, _ := got["content"].(string)
		return strings.Contains(c, "BOB EDIT 1") && strings.Contains(c, "BOB EDIT 2")
	}) {
		t.Fatalf("owner never converged to joiner's markdown")
	}
}

// TestE2E_MultipeerJoinerDeletePropagation isolates the joiner→owner
// Delete path from markdown.Set's modify+delete pair. The joiner makes
// a single sp.Delete call against a record the owner created, and the
// owner must see the tombstone within the usual headsync window.
//
// markdown.Set masks this because its modify-then-delete pair on the
// same broadcast cycle made it look like both reached the owner; in
// practice only the modify did. Repro'ing with a bare Delete (no
// Modify before it) makes the gap visible: if Delete tombstones don't
// propagate, the record stays live on the owner forever.
func TestE2E_MultipeerJoinerDeletePropagation(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer test takes ~60s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	// Owner: space + object + initial markdown (gives us records to
	// delete). Use markdown only to seed records easily; the path
	// under test is sp.Delete, not markdown.Set.
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"delete-prop"}`, http.StatusCreated, &sp)

	var objResp map[string]any
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &objResp)
	objectID, _ := objResp["objectId"].(string)
	if objectID == "" {
		t.Fatalf("objectId empty: %+v", objResp)
	}

	body, _ := json.Marshal(map[string]string{"content": "alpha\n\nbeta"})
	var ownerSet map[string]any
	mustJSON(t, http.MethodPut,
		owner.base+"/v1/spaces/"+sp.Id+"/objects/"+objectID+"/editor/markdown",
		string(body), http.StatusOK, &ownerSet)
	insertedAny, _ := ownerSet["inserted"].([]any)
	if len(insertedAny) != 2 {
		t.Fatalf("owner initial markdown set: inserted=%v want 2 entries", insertedAny)
	}
	ownerBlockIds := make([]string, 0, 2)
	for _, v := range insertedAny {
		if s, ok := v.(string); ok {
			ownerBlockIds = append(ownerBlockIds, s)
		}
	}
	t.Logf("owner inserted block ids: %v", ownerBlockIds)

	// Joiner joins, becomes active, and waits for the owner's blocks
	// to land before issuing his Delete. This avoids racing the
	// initial cold-pull with the delete itself.
	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	mdURL := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + objectID + "/editor/markdown"
	if !pollUntil(2*time.Minute, func() bool {
		var got map[string]any
		resp, raw := doRequest(t, http.MethodGet, mdURL, "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			return false
		}
		c, _ := got["content"].(string)
		return strings.Contains(c, "alpha") && strings.Contains(c, "beta")
	}) {
		t.Fatalf("joiner never saw owner's initial markdown")
	}

	// Joiner: bare Delete on the first owner-allocated block id. No
	// Modify before it. The path under test is the broadcast of a
	// delete-only change.
	delBody, _ := json.Marshal(map[string]any{
		"objectId":  objectID,
		"dataset":   "editor_blocks",
		"recordIds": []string{ownerBlockIds[0]},
	})
	var delResp map[string]any
	mustJSON(t, http.MethodPost, joiner.base+"/v1/spaces/"+sp.Id+"/delete-records",
		string(delBody), http.StatusOK, &delResp)
	t.Logf("joiner delete result: %+v", delResp)

	// Joiner sees the deletion locally (sanity).
	if !pollUntil(15*time.Second, func() bool {
		var got map[string]any
		resp, raw := doRequest(t, http.MethodGet, mdURL, "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			return false
		}
		c, _ := got["content"].(string)
		return !strings.Contains(c, "alpha") && strings.Contains(c, "beta")
	}) {
		t.Fatalf("joiner local delete did not project: query still shows alpha")
	}

	// The actual assertion: owner must see the tombstone.
	ownerMdURL := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + objectID + "/editor/markdown"
	ownerEditorBlocksQueryURL := owner.base + "/v1/spaces/" + sp.Id + "/query"
	queryBody := fmt.Sprintf(`{"objectId":%q,"dataset":"editor_blocks"}`, objectID)
	if !pollUntil(2*time.Minute, func() bool {
		var got map[string]any
		resp, raw := doRequest(t, http.MethodGet, ownerMdURL, "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			return false
		}
		c, _ := got["content"].(string)
		return !strings.Contains(c, "alpha") && strings.Contains(c, "beta")
	}) {
		var got map[string]any
		resp, raw := doRequest(t, http.MethodGet, ownerMdURL, "")
		_ = json.Unmarshal(raw, &got)
		mdContent, _ := got["content"].(string)

		// Also probe the raw editor_blocks dataset to see whether the
		// delete tombstoned the record on the owner (raw query
		// returns tombstones; markdown.List filters them).
		_, rawBlocks := doRequest(t, http.MethodPost, ownerEditorBlocksQueryURL, queryBody)
		t.Fatalf("owner never saw the tombstone\n  /markdown content=%q (status=%d)\n  /query editor_blocks raw=%s",
			mdContent, resp.StatusCode, string(rawBlocks))
	}
}

func lenAny(v any) int {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case []any:
		return len(x)
	case []string:
		return len(x)
	}
	return 0
}

// avoid unused-import warning if the helpers above are tweaked.
var _ = fmt.Sprintf
