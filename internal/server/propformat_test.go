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

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"Doc","xKey":"doc"}`)
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

	// The same format with an explicit string kind — the ISO-8601
	// convention these formats carried before instants existed. Kind is
	// pinned first-write, so this is how a property created by an older
	// client keeps behaving.
	rec = doJSON(t, e, http.MethodPost, propsURL, `{"name":"Legacy due","kind":"string","format":{"type":"datetime"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("legacy datetime format: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("decode propId: %v", err)
	}
	legacyDueProp := addResp.PropId

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
		"valid links":             {`{"patch":{"` + relatedProp + `":["any://objabc","any://objdef"]}}`, http.StatusOK},
		"valid datetime":          {`{"patch":{"` + dueProp + `":{"$date":"2026-07-03T12:00:00Z"}}}`, http.StatusOK},
		"valid datetime millis":   {`{"patch":{"` + dueProp + `":{"$date":1786014230123}}}`, http.StatusOK},
		"bad datetime":            {`{"patch":{"` + dueProp + `":{"$date":"tomorrow"}}}`, http.StatusBadRequest},
		"datetime bare string":    {`{"patch":{"` + dueProp + `":"2026-07-03T12:00:00Z"}}`, http.StatusBadRequest},
		"datetime bare number":    {`{"patch":{"` + dueProp + `":12345}}`, http.StatusBadRequest},
		"datetime extra key":      {`{"patch":{"` + dueProp + `":{"$date":"2026-07-03T12:00:00Z","tz":"UTC"}}}`, http.StatusBadRequest},
		"legacy string datetime":  {`{"patch":{"` + legacyDueProp + `":"2026-07-03T12:00:00Z"}}`, http.StatusOK},
		"legacy string bad value": {`{"patch":{"` + legacyDueProp + `":"tomorrow"}}`, http.StatusBadRequest},
		"links non-array":         {`{"patch":{"` + relatedProp + `":"any://objabc"}}`, http.StatusBadRequest},
		"links bad uri":           {`{"patch":{"` + relatedProp + `":["not-a-uri"]}}`, http.StatusBadRequest},
		"links global form":       {`{"patch":{"` + relatedProp + `":["any://space1/objabc"]}}`, http.StatusBadRequest},
		"links typed form":        {`{"patch":{"` + relatedProp + `":["any://o/space1/objabc"]}}`, http.StatusBadRequest},
		// INTENTIONAL delta vs the pre-anyuri validator: a 1-4 char
		// lowercase-alphanumeric first segment is the reserved kind-slug
		// namespace (docs/19-links.md), so pathological short ids like
		// "any://abc" — accepted before — now reject. Real object ids
		// are ≥40-char base58; no stored value has this shape.
		"links short id (kind namespace)": {`{"patch":{"` + relatedProp + `":["any://abc"]}}`, http.StatusBadRequest},
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
		`{"types":["`+tr.TypeId+`"],"initialProperties":{"`+tr.TypeId+`":{"`+dueProp+`":{"$date":"not-a-date"}}}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("initialProperties violation: status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		`{"types":["`+tr.TypeId+`"],"initialProperties":{"`+tr.TypeId+`":{"`+dueProp+`":{"$date":"2026-01-01T00:00:00Z"}}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("initialProperties valid: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestPatchPathToStorage covers the wire→storage path translation and
// the pinned/unknown-path rejections used by PATCH properties. Pure
// logic — no SDK or network.
func TestPatchPathToStorage(t *testing.T) {
	cases := []struct {
		path        string
		forSet      bool
		wantStorage string
		wantCode    string
	}{
		{"name", true, "name", ""},
		{"description", true, "description", ""},
		{"xKey", true, "x-key", ""},
		{"xKind", true, "x-kind", ""},
		{"meta.index", true, "meta.index", ""},
		{"format.ui", true, "format.ui", ""},
		{"format.filter", true, "format.filter", ""},
		{"format.meta.pattern", true, "format.meta.pattern", ""},
		{"format.options.high.name", true, "format.options.high.name", ""},
		{"format.options.high.color", true, "format.options.high.color", ""},
		{"format.options.high.meta.icon", true, "format.options.high.meta.icon", ""},
		// Container paths: rejected on set, allowed on unset (clear).
		{"format.options.high", true, "", "request.invalid_field"}, // whole option not settable
		{"format.options.high", false, "format.options.high", ""},  // unset deletes the option
		{"format.options", true, "", "request.invalid_field"},      // whole map not settable
		{"format.options", false, "format.options", ""},            // unset clears all
		{"meta", true, "", "request.invalid_field"},                // whole meta bag not settable
		{"meta", false, "meta", ""},                                // unset clears the bag
		{"format.meta", true, "", "request.invalid_field"},
		{"format.meta", false, "format.meta", ""},
		// Unknown option leaf.
		{"format.options.high.weight", true, "", "request.invalid_field"},
		// Pinned.
		{"kind", true, "", "property.immutable"},
		{"scope", true, "", "property.immutable"},
		{"items", true, "", "property.immutable"},
		{"properties", true, "", "property.immutable"},
		{"format", true, "", "property.immutable"},
		{"format.type", true, "", "property.immutable"},
		// Malformed / unknown.
		{"", true, "", "request.invalid_field"},
		{"format..ui", true, "", "request.invalid_field"},
		{"bogus", true, "", "request.invalid_field"},
		{"xKey.sub", true, "", "request.invalid_field"},
	}
	for _, tc := range cases {
		gotStorage, gotCode, reason := patchPathToStorage(tc.path, tc.forSet)
		if gotCode != tc.wantCode {
			t.Errorf("patchPathToStorage(%q, set=%v) code=%q want %q (reason=%q)", tc.path, tc.forSet, gotCode, tc.wantCode, reason)
		}
		if tc.wantCode == "" && gotStorage != tc.wantStorage {
			t.Errorf("patchPathToStorage(%q, set=%v) storage=%q want %q", tc.path, tc.forSet, gotStorage, tc.wantStorage)
		}
	}
}

// TestPatchSetValue covers value decoding: strings for all leaves,
// filter parse-check + JSON-text passthrough, ui vocabulary, and the
// error-code split (format-specific vs generic value error).
func TestPatchSetValue(t *testing.T) {
	// String leaf.
	if v, code, reason := patchSetValue("name", json.RawMessage(`"Priority"`)); code != "" || v != "Priority" {
		t.Errorf(`name: got (%v,%q,%q), want ("Priority","","")`, v, code, reason)
	}
	// Non-string value on a non-format field → generic code.
	if _, code, _ := patchSetValue("name", json.RawMessage(`42`)); code != "request.invalid_field" {
		t.Errorf("non-string name: code=%q want request.invalid_field", code)
	}
	// format.ui vocabulary → format-specific code.
	if _, code, _ := patchSetValue("format.ui", json.RawMessage(`"bogus"`)); code != "property.format_invalid" {
		t.Errorf("bad ui: code=%q want property.format_invalid", code)
	}
	if _, code, _ := patchSetValue("format.ui", json.RawMessage(`"select"`)); code != "" {
		t.Errorf("valid format ui rejected: code=%q", code)
	}
	// format.filter: valid condition passes through as JSON text.
	if v, code, _ := patchSetValue("format.filter", json.RawMessage(`{"type":"page"}`)); code != "" || v != `{"type":"page"}` {
		t.Errorf(`filter: got (%v,%q)`, v, code)
	}
}
