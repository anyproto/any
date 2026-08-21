package server

import (
	"encoding/json"
	"testing"
)

// TestServer_DataView_ReEnsureMerges backs the client recommendation in
// docs/08-clients.md § 12: a root `$set` MERGES its keys rather than
// replacing the record, so the idempotent "ensure the default view"
// call on every open must not wipe the device's localSettings or the
// server's creator / createdAt stamps.
func TestServer_DataView_ReEnsureMerges(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupViewFixture(t, e)
	createView(t, e, spaceId, objectId, "default", defaultViewPayload)
	writeLocalSettings(t, e, spaceId, objectId, "default", `{"widths": {"name": 480}}`)

	before := getView(t, e, spaceId, objectId, "default")
	if before.Creator == "" || before.CreatedAt.seconds() == 0 {
		t.Fatalf("fixture not stamped: %+v", before)
	}

	// The ensure a client repeats on every open.
	createView(t, e, spaceId, objectId, "default", defaultViewPayload)

	after := getView(t, e, spaceId, objectId, "default")
	if after.Creator != before.Creator {
		t.Errorf("creator clobbered: %q -> %q", before.Creator, after.Creator)
	}
	if after.CreatedAt.seconds() != before.CreatedAt.seconds() {
		t.Errorf("createdAt clobbered: %v -> %v", before.CreatedAt.seconds(), after.CreatedAt.seconds())
	}
	var local map[string]any
	if err := json.Unmarshal(after.LocalSettings, &local); err != nil {
		t.Fatalf("localSettings wiped by the re-ensure: %v (raw=%s)", err, after.LocalSettings)
	}
	widths, _ := local["widths"].(map[string]any)
	if widths["name"] != float64(480) {
		t.Errorf("localSettings.widths.name = %v, want 480 — the re-ensure must merge", widths["name"])
	}
}
