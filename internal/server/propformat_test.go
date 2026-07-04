package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_PropertyFormat covers the format surface end-to-end:
// definition-time semantics on POST properties (vocabulary, kind
// defaulting, filter parseability), the format on the read-back, and
// value-shape enforcement on the property-write endpoints.
func TestServer_PropertyFormat(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"FmtDemo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/spaces: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Page","xKey":"page"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var tr api.TypesCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode type: %v", err)
	}
	propsURL := "/v1/spaces/" + sp.Id + "/types/" + tr.TypeId + "/properties"

	// --- definition-time semantics -------------------------------------

	// Links format, kind omitted: accepted, kind defaulted to array.
	rec = doJSON(t, e, http.MethodPost, propsURL,
		`{"name":"Related","format":{"type":"links","ui":"multiselect","filter":{"type":{"$in":["page"]}}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("links format: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var addResp api.AddPropertyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("decode propId: %v", err)
	}
	relatedProp := addResp.PropId

	// Datetime format.
	rec = doJSON(t, e, http.MethodPost, propsURL, `{"name":"Due","format":{"type":"datetime"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("datetime format: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("decode propId: %v", err)
	}
	dueProp := addResp.PropId

	for name, body := range map[string]string{
		"unknown format type":  `{"name":"A","format":{"type":"rainbow"}}`,
		"reserved tags format": `{"name":"B","format":{"type":"tags"}}`,
		"unknown ui":           `{"name":"C","format":{"type":"links","ui":"dropdown"}}`,
		"ui on datetime":       `{"name":"D","format":{"type":"datetime","ui":"select"}}`,
		"filter on date":       `{"name":"E","format":{"type":"date","filter":{"a":1}}}`,
		"unparseable filter":   `{"name":"F","format":{"type":"links","filter":{"type":{"$nope":1}}}}`,
		"kind mismatch":        `{"name":"G","kind":"string","format":{"type":"links"}}`,
	} {
		rec = doJSON(t, e, http.MethodPost, propsURL, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400; body=%s", name, rec.Code, rec.Body.String())
		}
	}

	// Read-back carries the format and the defaulted kind.
	rec = doJSON(t, e, http.MethodGet, propsURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list properties: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list api.PropertiesListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var related *api.PropertyDef
	for i := range list.Properties {
		if list.Properties[i].Id == relatedProp {
			related = &list.Properties[i]
		}
	}
	if related == nil || related.Format == nil {
		t.Fatalf("related prop or its format missing in read-back: %+v", list.Properties)
	}
	if related.Kind != api.PropertyKindArray {
		t.Errorf("kind = %q, want array (defaulted from links)", related.Kind)
	}
	if related.Format.Type != api.FormatTypeLinks || related.Format.UI != api.FormatUIMultiselect {
		t.Errorf("format read-back = %+v", related.Format)
	}
	if string(related.Format.Filter) != `{"type":{"$in":["page"]}}` {
		t.Errorf("filter read-back = %s", related.Format.Filter)
	}

	// --- value-shape enforcement ---------------------------------------

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", `{"types":["`+tr.TypeId+`"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var objResp api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &objResp); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	setURL := "/v1/spaces/" + sp.Id + "/properties/" + objResp.ObjectId + "/set/" + tr.TypeId

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"valid links":         {`{"patch":{"` + relatedProp + `":["any://abc","any://def"]}}`, http.StatusOK},
		"valid datetime":      {`{"patch":{"` + dueProp + `":"2026-07-03T12:00:00Z"}}`, http.StatusOK},
		"bad datetime":        {`{"patch":{"` + dueProp + `":"tomorrow"}}`, http.StatusBadRequest},
		"datetime non-string": {`{"patch":{"` + dueProp + `":12345}}`, http.StatusBadRequest},
		"links non-array":     {`{"patch":{"` + relatedProp + `":"any://abc"}}`, http.StatusBadRequest},
		"links bad uri":       {`{"patch":{"` + relatedProp + `":["not-a-uri"]}}`, http.StatusBadRequest},
		"links global form":   {`{"patch":{"` + relatedProp + `":["any://space/obj"]}}`, http.StatusBadRequest},
	} {
		rec = doJSON(t, e, http.MethodPost, setURL, tc.body)
		if rec.Code != tc.want {
			t.Errorf("%s: status=%d, want %d; body=%s", name, rec.Code, tc.want, rec.Body.String())
		}
		if tc.want == http.StatusBadRequest {
			if code := errCode(t, rec.Body.Bytes()); code != "property.format_violation" {
				t.Errorf("%s: code=%q, want property.format_violation", name, code)
			}
		}
	}

	// A null value passes the FORMAT gate (unsets are not its concern) —
	// the SDK's own kind check rejects it downstream, proving the
	// format validator didn't intercept it.
	rec = doJSON(t, e, http.MethodPost, setURL, `{"patch":{"`+dueProp+`":null}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("null value: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code == "property.format_violation" {
		t.Errorf("null value must not trip the format gate (got %q)", code)
	}

	// initialProperties on object create go through the same gate.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		`{"types":["`+tr.TypeId+`"],"initialProperties":{"`+tr.TypeId+`":{"`+dueProp+`":"not-a-date"}}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("initialProperties violation: status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		`{"types":["`+tr.TypeId+`"],"initialProperties":{"`+tr.TypeId+`":{"`+dueProp+`":"2026-01-01T00:00:00Z"}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("initialProperties valid: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
