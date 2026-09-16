// Cross-peer backlinks: the link index is consumer-side, so each peer
// builds its own from the synced records. A link the owner writes into
// an editor block must show up in the joiner's index, with the block
// as its source place — and disappear there when the owner deletes the
// block.
package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

func backlinksOn(t *testing.T, p *peer, spaceId, objectId string) (api.BacklinksResponse, bool) {
	t.Helper()
	resp, raw := doRequest(t, http.MethodGet, p.base+"/v1/spaces/"+spaceId+"/objects/"+objectId+"/backlinks", "")
	if resp.StatusCode != http.StatusOK {
		return api.BacklinksResponse{}, false
	}
	var out api.BacklinksResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: decode backlinks: %v (%s)", p.name, err, raw)
	}
	return out, true
}

func TestE2E_MultipeerLinks(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"links"}`, http.StatusCreated, &sp)

	var target api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{"type":"page","initialProperties":{"any":{"name":"target"}}}`, http.StatusCreated, &target)
	page := createModuleObject(t, owner.base, sp.Id, "editor")
	blocks := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + page + "/editor/editor_blocks/blocks"
	var blk api.ModifyResult
	mustJSON(t, http.MethodPost, blocks,
		`{"type":"paragraph","text":"[Target](any://o/`+sp.Id+`/`+target.ObjectId+`)"}`, http.StatusCreated, &blk)
	blockId := blk.RecordIds[0]

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// Both peers index the card edge from the same synced block.
	hasCard := func(p *peer) bool {
		bl, ok := backlinksOn(t, p, sp.Id, target.ObjectId)
		if !ok {
			return false
		}
		for _, l := range bl.Object {
			if l.Source.ObjectId == page && l.Source.RecordId == blockId && l.Kind == api.LinkKindCard {
				return true
			}
		}
		return false
	}
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		return hasCard(owner) && hasCard(joiner)
	}) {
		t.Fatalf("card edge never indexed on both peers")
	}

	// The owner deletes the block; the joiner's index drops the edge.
	mustStatus(t, http.MethodDelete, blocks+"/"+blockId, "", http.StatusOK)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		return !hasCard(owner) && !hasCard(joiner)
	}) {
		t.Fatalf("card edge never evicted on both peers")
	}
}
