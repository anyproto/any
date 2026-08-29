package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_ModifyUnknownDataset pins the write-side contract for a
// dataset name the object does not carry: POST /modify (upsert and
// plain ops) and POST /delete-records answer 400 dataset.unknown with
// the name in details — a caller fault, never a 500 internal
// (docs/06-errors.md).
func TestServer_ModifyUnknownDataset(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	modifyURL := "/v1/spaces/" + spaceId + "/modify"
	deleteURL := "/v1/spaces/" + spaceId + "/delete-records"

	for name, tc := range map[string]struct{ url, body string }{
		"modify upsert": {modifyURL, fmt.Sprintf(`{"objectId": %q, "dataset": "trace_records",
			"records": [{"id": "x", "upsert": true, "ops": [{"type": "$set", "path": "", "value": {"fake": true}}]}]}`, objectId)},
		"modify set": {modifyURL, fmt.Sprintf(`{"objectId": %q, "dataset": "trace_records",
			"records": [{"id": "x", "ops": [{"type": "$set", "path": "fake", "value": true}]}]}`, objectId)},
		"delete-records": {deleteURL, fmt.Sprintf(`{"objectId": %q, "dataset": "trace_records", "recordIds": ["x"]}`, objectId)},
	} {
		rec := doJSON(t, e, http.MethodPost, tc.url, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400: %s", name, rec.Code, rec.Body.String())
			continue
		}
		assertErrorCode(t, rec, "dataset.unknown")
		var env api.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s: decode envelope: %v", name, err)
		}
		if got, _ := env.Error.Details["dataset"].(string); got != "trace_records" {
			t.Errorf("%s: details.dataset = %q, want trace_records (%s)", name, got, rec.Body.String())
		}
	}
}
