package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_AgentSecrets_DeviceLocalValue exercises the agent_secrets
// built-in — the secrets counterpart of agent_config. The dataset
// declares `value` local-scope, so the secret payload persists as a
// never-synced field. This mirrors the two-step write the harness
// performs — a synced record materializes the ref, a local $set writes
// the secret — then proves the field reads back, never mints a DAG
// change, and enforces its scope both directions. It also pins the
// derive contract: the id is deterministic (same on repeated GETs) and
// distinct from the agent_config object.
func TestServer_AgentSecrets_DeviceLocalValue(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SecTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	// The single-space GET derives (materializes) + reports the secrets object.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get space: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space GET: %v", err)
	}
	obj := sp.AgentSecretsObjectId
	if obj == "" {
		t.Fatal("space GET reported no agentSecretsObjectId")
	}
	if sp.AgentConfigObjectId == "" {
		t.Fatal("space GET reported no agentConfigObjectId")
	}
	if obj == sp.AgentConfigObjectId {
		t.Fatalf("agentSecretsObjectId == agentConfigObjectId (%s), want distinct objects", obj)
	}

	// Derive idempotency: a second GET reports the same id.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("second get space: %d %s", rec.Code, rec.Body.String())
	}
	var sp2 api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp2); err != nil {
		t.Fatalf("decode second space GET: %v", err)
	}
	if sp2.AgentSecretsObjectId != obj {
		t.Fatalf("agentSecretsObjectId not stable: %s then %s", obj, sp2.AgentSecretsObjectId)
	}

	const key = "connector.key.linear"
	modifyURL := "/v1/spaces/" + sp.Id + "/modify"

	// Step 1 (synced): materialize the record carrying only the ref +
	// the secret marker — no secret ever lands in synced data.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "agent_secrets",
		"records": [{"id": %q, "upsert": true, "ops": [{"type": "$set", "path": "",
			"value": {"key": %q, "secret": true}}]}]
	}`, obj, key, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("synced upsert: %d %s", rec.Code, rec.Body.String())
	}
	if res := decodeModifyResult(t, rec.Body.Bytes()); len(res.Rejections) > 0 {
		t.Fatalf("synced upsert rejected: %+v", res.Rejections)
	}

	// Step 2 (local): write the secret into the never-synced value.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "agent_secrets", "scope": "local",
		"records": [{"id": %q, "ops": [{"type": "$set", "path": "value", "value": "lin_api_secret"}]}]
	}`, obj, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("local set: %d %s", rec.Code, rec.Body.String())
	}
	res := decodeModifyResult(t, rec.Body.Bytes())
	if len(res.Rejections) > 0 {
		t.Fatalf("local set rejected: %+v", res.Rejections)
	}
	if res.VersionId == "" {
		t.Errorf("local set: empty versionId")
	}
	if res.ChangeId != "" {
		t.Errorf("local set: got DAG changeId %q, device-local writes must not mint one", res.ChangeId)
	}

	// Read back: the record carries value on the query path.
	qBody, _ := json.Marshal(map[string]any{"objectId": obj, "dataset": "agent_secrets"})
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/query", string(qBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("query: %d %s", rec.Code, rec.Body.String())
	}
	var qr struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &qr); err != nil {
		t.Fatalf("decode query: %v", err)
	}
	if len(qr.Records) != 1 {
		t.Fatalf("query: got %d records, want 1: %s", len(qr.Records), rec.Body.String())
	}
	if got := qr.Records[0]["value"]; got != "lin_api_secret" {
		t.Errorf("value read back = %v, want lin_api_secret", got)
	}

	// Scope enforcement, both directions.
	// (a) A SYNCED write to value is refused (400) — it is device-local.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "agent_secrets",
		"records": [{"id": %q, "ops": [{"type": "$set", "path": "value", "value": "leaked"}]}]
	}`, obj, key))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("synced write to value: status=%d, want 400: %s", rec.Code, rec.Body.String())
	}
	// (b) A LOCAL write to the synced `key` field is op-rejected, value intact.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "agent_secrets", "scope": "local",
		"records": [{"id": %q, "ops": [{"type": "$set", "path": "key", "value": "smuggled"}]}]
	}`, obj, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("local-to-synced: %d %s", rec.Code, rec.Body.String())
	}
	if res := decodeModifyResult(t, rec.Body.Bytes()); len(res.Rejections) == 0 {
		t.Errorf("local write to synced key: expected op rejection, got %+v", res)
	}

	// Discovery: the datasets doc reports value as x-scope local.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/datasets", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("datasets: %d %s", rec.Code, rec.Body.String())
	}
	var ds struct {
		Datasets []struct {
			Name   string `json:"name"`
			Schema struct {
				Properties map[string]struct {
					XScope string `json:"x-scope"`
				} `json:"properties"`
			} `json:"schema"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ds); err != nil {
		t.Fatalf("decode datasets: %v", err)
	}
	found := false
	for _, dd := range ds.Datasets {
		if dd.Name != "agent_secrets" {
			continue
		}
		found = true
		if sc := dd.Schema.Properties["value"].XScope; sc != "local" {
			t.Errorf("agent_secrets.value x-scope = %q, want local", sc)
		}
	}
	if !found {
		t.Error("agent_secrets dataset missing from discovery")
	}
}
