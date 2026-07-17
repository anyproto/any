package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_AgentConfig_DeviceLocalSecret exercises the device-local
// secret path on the agent_config built-in (anybao docs/adr 006 §3). The
// dataset declares `localValue` local-scope, so a config secret (the
// Anthropic API key) persists as a never-synced field. This mirrors the
// two-step write the harness performs — a synced record materializes the
// id, a local $set writes the secret — then proves the field reads back,
// never mints a DAG change, and enforces its scope both directions.
func TestServer_AgentConfig_DeviceLocalSecret(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"CfgTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	// The single-space GET derives (materializes) + reports the config object.
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get space: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space GET: %v", err)
	}
	obj := sp.AgentConfigObjectId
	if obj == "" {
		t.Fatal("space GET reported no agentConfigObjectId")
	}

	const key = "llm.key.anthropic"
	modifyURL := "/v1/spaces/" + sp.Id + "/modify"

	// Step 1 (synced): materialize the record carrying only the key name +
	// a secret marker — no secret ever lands in synced data.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "agent_config",
		"records": [{"id": %q, "upsert": true, "ops": [{"type": "$set", "path": "",
			"value": {"key": %q, "secret": true}}]}]
	}`, obj, key, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("synced upsert: %d %s", rec.Code, rec.Body.String())
	}
	if res := decodeModifyResult(t, rec.Body.Bytes()); len(res.Rejections) > 0 {
		t.Fatalf("synced upsert rejected: %+v", res.Rejections)
	}

	// Step 2 (local): write the secret into the never-synced localValue.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "agent_config", "scope": "local",
		"records": [{"id": %q, "ops": [{"type": "$set", "path": "localValue", "value": "sk-secret"}]}]
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

	// Read back: the record carries localValue on the query path.
	qBody, _ := json.Marshal(map[string]any{"objectId": obj, "dataset": "agent_config"})
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
	if got := qr.Records[0]["localValue"]; got != "sk-secret" {
		t.Errorf("localValue read back = %v, want sk-secret", got)
	}

	// Scope enforcement, both directions.
	// (a) A SYNCED write to localValue is refused (400) — it is device-local.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "agent_config",
		"records": [{"id": %q, "ops": [{"type": "$set", "path": "localValue", "value": "sk-synced"}]}]
	}`, obj, key))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("synced write to localValue: status=%d, want 400: %s", rec.Code, rec.Body.String())
	}
	// (b) A LOCAL write to the synced `value` field is op-rejected, value intact.
	rec = doJSON(t, e, http.MethodPost, modifyURL, fmt.Sprintf(`{
		"objectId": %q, "dataset": "agent_config", "scope": "local",
		"records": [{"id": %q, "ops": [{"type": "$set", "path": "value", "value": "smuggled"}]}]
	}`, obj, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("local-to-synced: %d %s", rec.Code, rec.Body.String())
	}
	if res := decodeModifyResult(t, rec.Body.Bytes()); len(res.Rejections) == 0 {
		t.Errorf("local write to synced value: expected op rejection, got %+v", res)
	}

	// Discovery: the datasets doc reports localValue as x-scope local.
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
		if dd.Name != "agent_config" {
			continue
		}
		found = true
		if sc := dd.Schema.Properties["localValue"].XScope; sc != "local" {
			t.Errorf("agent_config.localValue x-scope = %q, want local", sc)
		}
	}
	if !found {
		t.Error("agent_config dataset missing from discovery")
	}
}
