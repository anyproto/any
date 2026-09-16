// TestE2E_ObjectsModifiedAt pins the derived `modifiedAt` /
// `modifiedBy` pair on objects-collection rows end to end: seeded at
// create, bumped by a property write, and usable as the `-modifiedAt`
// recency sort — the "recently modified first" ordering clients build
// object lists on. `modifiedBy` names the account that signed the
// change `modifiedAt` points at; on one peer that is always this
// account, so the cross-peer half of the contract lives in
// TestE2E_MultipeerModifiedBy. The stamps are SDK-owned
// (SystemPropertiesHandler); this test covers the HTTP passthrough
// contract documented in docs/03-api.md § Data plane and
// docs/08-clients.md § 3.
package e2e

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestE2E_ObjectsModifiedAt(t *testing.T) {
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
	spaceID := createSpace(t, base, "ModifiedAt", "modifiedAt e2e")
	movieType := createType(t, base, spaceID, "Movie")
	titleProp := addProperty(t, base, spaceID, movieType, "Title", "string")

	// The identity every stamp below must name: this server's own
	// account. /v1/account reports it in the same StrKey encoding the
	// row stamps carry (`author`, `modifiedBy`) — clients read the two
	// together to answer "did I write this last?".
	account := accountId(t, base)

	// rowIdentity reads a row-root identity stamp, failing when it is
	// absent or empty — every row carries both.
	rowIdentity := func(t *testing.T, row map[string]any, field string) string {
		t.Helper()
		s, _ := row[field].(string)
		if s == "" {
			t.Fatalf("%s = %#v, want a non-empty identity: %+v", field, row[field], row)
		}
		return s
	}

	queryRow := func(t *testing.T, objectID string) map[string]any {
		t.Helper()
		var resp map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/objects/query",
			fmt.Sprintf(`{"filter":{"id":%q}}`, objectID), http.StatusOK, &resp)
		records, _ := resp["records"].([]any)
		if len(records) != 1 {
			t.Fatalf("query for %s returned %d records, want 1: %+v", objectID, len(records), resp)
		}
		row, _ := records[0].(map[string]any)
		return row
	}

	objA := createObject(t, base, spaceID, movieType)

	t.Run("create seeds modifiedAt", func(t *testing.T) {
		row := queryRow(t, objA)
		created := stampMillis(t, row, "createdAt")
		modified := stampMillis(t, row, "modifiedAt")
		if created <= 0 {
			t.Errorf("createdAt = %v, want a positive instant: %+v", row["createdAt"], row)
		}
		if modified <= 0 {
			t.Errorf("modifiedAt = %v, want a positive instant: %+v", row["modifiedAt"], row)
		}
		if modified < created {
			t.Errorf("modifiedAt %v < createdAt %v on a fresh row", modified, created)
		}
		// The create is the object's latest change and its signer is
		// the object's author, so both stamps name this account. (Who
		// the stamp names once another account writes is pinned by
		// TestE2E_MultipeerModifiedBy.)
		if by, author := rowIdentity(t, row, "modifiedBy"), rowIdentity(t, row, "author"); by != account || author != account {
			t.Errorf("modifiedBy = %q, author = %q on a fresh row, want this account %q", by, author, account)
		}
	})

	objB := createObject(t, base, spaceID, movieType)

	// modifiedAt has unix-second resolution — tick the clock past B's
	// create so A's upcoming write lands on a strictly later second.
	time.Sleep(1100 * time.Millisecond)

	var modify map[string]any
	mustJSON(t, http.MethodPost,
		base+"/v1/spaces/"+spaceID+"/properties/"+objA+"/set/"+movieType,
		fmt.Sprintf(`{"patch":{%q:"Casablanca"}}`, titleProp),
		http.StatusOK, &modify)

	t.Run("property write bumps modifiedAt", func(t *testing.T) {
		rowA := queryRow(t, objA)
		rowB := queryRow(t, objB)
		modA := stampMillis(t, rowA, "modifiedAt")
		modB := stampMillis(t, rowB, "modifiedAt")
		createdA := stampMillis(t, rowA, "createdAt")
		if modA <= modB {
			t.Errorf("A.modifiedAt = %v not past B.modifiedAt = %v after writing A", modA, modB)
		}
		if modA <= createdA {
			t.Errorf("A.modifiedAt = %v not past A.createdAt = %v after a later write", modA, createdA)
		}
		// The write re-stamps modifiedBy with the same account.
		if by := rowIdentity(t, rowA, "modifiedBy"); by != account {
			t.Errorf("A.modifiedBy = %q after the write, want this account %q", by, account)
		}
	})

	t.Run("sort -modifiedAt is recency order", func(t *testing.T) {
		// Creation order is A then B, so newest-first *creation* order
		// would be [B, A]; the later write to A must flip recency to
		// [A, B].
		var resp map[string]any
		mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/objects/query",
			fmt.Sprintf(`{"filter":{"any.type":%q},"sort":["-modifiedAt"]}`, movieType),
			http.StatusOK, &resp)
		records, _ := resp["records"].([]any)
		if len(records) != 2 {
			t.Fatalf("query returned %d records, want 2: %+v", len(records), resp)
		}
		var ids []string
		for _, r := range records {
			row, _ := r.(map[string]any)
			id, _ := row["id"].(string)
			ids = append(ids, id)
		}
		if ids[0] != objA || ids[1] != objB {
			t.Errorf("-modifiedAt order = %v, want [%s %s]", ids, objA, objB)
		}
	})
}
