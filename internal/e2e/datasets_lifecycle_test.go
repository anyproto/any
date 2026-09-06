// Runtime dataset schemas e2e: drives the type-scoped dataset CRUD
// (AddDataset / Datasets / PatchDataset / RemoveDataset[Field]), the
// discovery document, and the generic upsert end-to-end through the
// HTTP server against the real SDK. See docs/03-api.md § Types and
// § Upsert records.
package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestE2E_DatasetsLifecycle(t *testing.T) {
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
	spaceID := createSpace(t, base, "Datasets", "runtime dataset schemas e2e")
	articleType := createType(t, base, spaceID, "Article")
	objectID := createObject(t, base, spaceID, articleType)
	dsURL := base + "/v1/spaces/" + spaceID + "/types/" + articleType + "/datasets"
	upsertURL := base + "/v1/spaces/" + spaceID + "/upsert"

	// A dataset lives under a part; a records dataset is namespaced to
	// its type, so reads and writes name the collection <typeId>_<key>.
	var part map[string]any
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/types/"+articleType+"/parts",
		`{"key":"articles","name":"Articles"}`, http.StatusCreated, &part)
	partID, _ := part["partId"].(string)
	if partID == "" {
		t.Fatalf("no partId in %+v", part)
	}
	collection := articleType + "_articles"

	// Define: full behavioral draft — user ids, author-only delete,
	// stamps, one required field, search mapping.
	var added map[string]any
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/types/"+articleType+"/parts/"+partID+"/datasets", `{
		"key": "articles", "displayName": "Articles",
		"idRule": "user", "deleteBy": "author",
		"search": {"title": "title", "text": "body", "scope": "news"},
		"fields": [
			{"key": "title", "kind": "string", "required": true, "mutableBy": "author"},
			{"key": "body", "kind": "string", "mutableBy": "author"},
			{"key": "author", "stamp": "creator"},
			{"key": "createdAt", "stamp": "createTime"},
			{"key": "updatedAt", "stamp": "modifyTime"}
		]}`, http.StatusCreated, &added)
	defID, _ := added["datasetDefId"].(string)
	if defID == "" {
		t.Fatalf("no datasetDefId in %+v", added)
	}
	if got, _ := added["collection"].(string); got != collection {
		t.Fatalf("collection = %q, want %q", got, collection)
	}

	// Discovery: the space's dataset list carries the owning type and
	// the behavioral x-* keywords.
	var disco struct {
		Datasets []struct {
			Name   string          `json:"name"`
			Owners []string        `json:"owners"`
			Module string          `json:"module"`
			Schema json.RawMessage `json:"schema"`
		} `json:"datasets"`
	}
	mustJSON(t, http.MethodGet, base+"/v1/spaces/"+spaceID+"/datasets", "", http.StatusOK, &disco)
	found := false
	for _, ds := range disco.Datasets {
		if ds.Name != collection {
			continue
		}
		found = true
		if len(ds.Owners) != 1 || ds.Owners[0] != articleType || ds.Module != "records" {
			t.Errorf("owners/module = %v/%q, want [%s]/records", ds.Owners, ds.Module, articleType)
		}
		var doc struct {
			Id       string                        `json:"x-id"`
			DeleteBy string                        `json:"x-delete-by"`
			Search   *struct{ Title, Text string } `json:"x-search"`
		}
		if err := json.Unmarshal(ds.Schema, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Id != "user" || doc.DeleteBy != "author" || doc.Search == nil {
			t.Errorf("discovery keywords: %+v", doc)
		}
	}
	if !found {
		t.Fatal("articles missing from discovery")
	}

	// Upsert: create two records.
	upsertBody := `{"objectId":"` + objectID + `","dataset":"` + collection + `","records":[
		{"id":"a1","fields":{"title":"Hello","body":"first"}},
		{"id":"a2","fields":{"title":"World","body":"second"}}]}`
	var res struct {
		Created, Updated, Skipped int
		Rejections                []struct{ Id, Code string }
	}
	mustJSON(t, http.MethodPost, upsertURL, upsertBody, http.StatusOK, &res)
	if res.Created != 2 || len(res.Rejections) != 0 {
		t.Fatalf("upsert create = %+v", res)
	}

	// Idempotent rerun: everything skipped, nothing written.
	mustJSON(t, http.MethodPost, upsertURL, upsertBody, http.StatusOK, &res)
	if res.Skipped != 2 || res.Created != 0 || res.Updated != 0 {
		t.Fatalf("upsert rerun = %+v", res)
	}

	// Records carry the derived stamps; enforced write-once holds.
	var q struct {
		Records []map[string]any `json:"records"`
	}
	mustJSON(t, http.MethodPost, base+"/v1/spaces/"+spaceID+"/query",
		`{"objectId":"`+objectID+`","dataset":"`+collection+`","sort":["id"]}`, http.StatusOK, &q)
	if len(q.Records) != 2 {
		t.Fatalf("query records = %+v", q.Records)
	}
	if s, _ := q.Records[0]["author"].(string); s == "" {
		t.Error("creator stamp missing")
	}
	if ms := stampMillis(t, q.Records[0], "createdAt"); ms == 0 {
		t.Error("createTime stamp missing")
	}

	// A client payload naming a stamped field is rejected per-record.
	mustJSON(t, http.MethodPost, upsertURL,
		`{"objectId":"`+objectID+`","dataset":"`+collection+`","records":[
			{"id":"a3","fields":{"title":"Forged","author":"someone-else"}}]}`,
		http.StatusOK, &res)
	if len(res.Rejections) != 1 || res.Created != 0 {
		t.Fatalf("stamped-field write must reject: %+v", res)
	}

	// Patch display leaves; pinned paths refuse.
	mustStatus(t, http.MethodPatch, dsURL+"/"+defID,
		`{"set":{"displayName":"Posts","search.title":"headline","search.scope":"press"}}`, http.StatusNoContent)
	mustStatus(t, http.MethodPatch, dsURL+"/"+defID,
		`{"set":{"idRule":"auto"}}`, http.StatusBadRequest)
	mustStatus(t, http.MethodPatch, dsURL+"/"+defID,
		`{"set":{"search.scope":"Not A Slug"}}`, http.StatusBadRequest)

	// Additive evolution: add a field, then remove it.
	var fieldAdded map[string]any
	mustJSON(t, http.MethodPost, dsURL+"/"+defID+"/fields",
		`{"key":"rating","kind":"number","mutableBy":"any"}`, http.StatusCreated, &fieldAdded)
	fieldID, _ := fieldAdded["fieldDefId"].(string)
	if fieldID == "" {
		t.Fatalf("no fieldDefId in %+v", fieldAdded)
	}
	mustStatus(t, http.MethodDelete, dsURL+"/"+defID+"/fields/"+fieldID, "", http.StatusNoContent)

	// Read-back reflects the patch and the evolution.
	var list struct {
		Datasets []struct {
			DisplayName string `json:"displayName"`
			Search      struct{ Title, Scope string }
			Fields      []struct{ Key string }
			Invalid     bool
		} `json:"datasets"`
	}
	mustJSON(t, http.MethodGet, dsURL, "", http.StatusOK, &list)
	if len(list.Datasets) != 1 || list.Datasets[0].DisplayName != "Posts" ||
		list.Datasets[0].Search.Scope != "press" ||
		list.Datasets[0].Invalid || len(list.Datasets[0].Fields) != 5 {
		t.Fatalf("read-back = %+v", list.Datasets)
	}

	// Remove the definition; the list empties; upsert now refuses.
	mustStatus(t, http.MethodDelete, dsURL+"/"+defID, "", http.StatusNoContent)
	mustJSON(t, http.MethodGet, dsURL, "", http.StatusOK, &list)
	if len(list.Datasets) != 0 {
		t.Fatalf("post-remove list = %+v", list.Datasets)
	}
	mustStatus(t, http.MethodPost, upsertURL, upsertBody, http.StatusBadRequest)
}
