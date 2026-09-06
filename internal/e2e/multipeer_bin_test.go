// Cross-peer move-to-bin: the type membership and the two move stamps
// travel in one change, so a joiner sees `bin` together with
// `bin.movedAt` / `bin.movedBy` naming the mover, and a restore issued
// on the other side clears the whole namespace everywhere.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bin"
)

// binRow is the decode target for an objects row's membership and
// bin namespace. Bin is nil when the row carries no `bin` key at all.
type binRow struct {
	Id  string `json:"id"`
	Any struct {
		Types []string `json:"types"`
	} `json:"any"`
	Bin map[string]any `json:"bin"`
}

// binRowOn reads one objects row off a peer; ok=false until the peer
// has the object.
func binRowOn(t *testing.T, p *peer, spaceId, objectId string) (binRow, bool) {
	t.Helper()
	resp, raw := doRequest(t, http.MethodPost,
		p.base+"/v1/spaces/"+spaceId+"/objects/query",
		fmt.Sprintf(`{"filter":{"id":%q}}`, objectId))
	if resp.StatusCode != http.StatusOK {
		return binRow{}, false
	}
	var qr struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(raw, &qr); err != nil || len(qr.Records) == 0 {
		return binRow{}, false
	}
	var row binRow
	if err := json.Unmarshal(qr.Records[0], &row); err != nil {
		t.Fatalf("%s: decode objects row: %v (%s)", p.name, err, qr.Records[0])
	}
	return row, row.Id == objectId
}

func (r binRow) carriesBin() bool {
	for _, tp := range r.Any.Types {
		if tp == bin.TypeId {
			return true
		}
	}
	return false
}

func TestE2E_MultipeerBin(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer test takes minutes; rerun without -short")
	}

	binPath := buildBinary(t)
	owner := startPeer(t, binPath, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, binPath, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces", `{"name":"bin"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{"initialProperties":{"any":{"name":"Trash me"}}}`, http.StatusCreated, &obj)
	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	peers := []*peer{owner, joiner}
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, peers, func() bool {
		_, ok := binRowOn(t, joiner, sp.Id, obj.ObjectId)
		return ok
	}) {
		t.Fatal("joiner never saw the object")
	}

	// The owner moves it to the bin; the joiner sees the type AND both
	// stamps, the mover being the owner.
	ownerId := accountId(t, owner.base)
	var res api.ModifyResult
	mustJSON(t, http.MethodPost,
		fmt.Sprintf("%s/v1/spaces/%s/properties/%s/attach/%s", owner.base, sp.Id, obj.ObjectId, bin.TypeId),
		"", http.StatusOK, &res)
	if res.ChangeId == "" {
		t.Fatalf("move minted no change: %+v", res)
	}
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, peers, func() bool {
		row, ok := binRowOn(t, joiner, sp.Id, obj.ObjectId)
		return ok && row.carriesBin() && row.Bin[bin.PropMovedBy] == ownerId && row.Bin[bin.PropMovedAt] != nil
	}) {
		row, _ := binRowOn(t, joiner, sp.Id, obj.ObjectId)
		t.Fatalf("joiner never saw the move with its stamps: %+v", row)
	}

	// The joiner restores it; the owner's row loses the type and the
	// whole namespace.
	mustJSON(t, http.MethodPost,
		fmt.Sprintf("%s/v1/spaces/%s/properties/%s/detach/%s", joiner.base, sp.Id, obj.ObjectId, bin.TypeId),
		"", http.StatusOK, &res)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, peers, func() bool {
		row, ok := binRowOn(t, owner, sp.Id, obj.ObjectId)
		return ok && !row.carriesBin() && row.Bin == nil
	}) {
		row, _ := binRowOn(t, owner, sp.Id, obj.ObjectId)
		t.Fatalf("owner never saw the restore: %+v", row)
	}
}
