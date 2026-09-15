package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_PropertyDescriptor covers the descriptor surface end to
// end: definition-time validation on POST properties (interpreted keys
// typed, slug against the pinned kind, reserved keys, vendor keys
// verbatim, the xKey guard, meta narrowed to index), the read-back,
// value validation against the current slug on the property-write
// endpoints, and the PATCH rules.
func TestServer_PropertyDescriptor(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"DescriptorDemo"}`)
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

	add := func(body string) string {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, propsURL, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("add %s: status=%d body=%s", body, rec.Code, rec.Body.String())
		}
		var resp api.AddPropertyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode propId: %v", err)
		}
		return resp.PropId
	}

	// --- definition time -------------------------------------------------

	stage := add(`{"name":"Stage","xKey":"stage","kind":"array","xFormat":{"type":"choice","pos":"a0",
		"config":{"multiple":false},
		"options":{"lead":{"name":"Lead","color":"grey","pos":"a0","meta":{"icon":"dot"}},"won":{"name":"Won","color":"green","pos":"a1"}}}}`)
	related := add(`{"name":"Related","xKey":"related","kind":"array","xFormat":{"type":"relation","icon":"link",
		"config":{"multiple":true},"relation":{"targetTypes":["doc"],"filter":"{\"any.type\":\"page\"}"}}}`)
	due := add(`{"name":"Due","xKey":"due","kind":"datetime","xFormat":{"type":"date"}}`)
	when := add(`{"name":"When","xKey":"when","kind":"datetime","xFormat":{"type":"datetime"}}`)
	site := add(`{"name":"Site","xKey":"site","kind":"string","xFormat":{"type":"url"}}`)
	mail := add(`{"name":"Mail","xKey":"mail","kind":"string","xFormat":{"type":"email"}}`)
	trip := add(`{"name":"Trip","xKey":"trip","kind":"object","xFormat":{"type":"period","icon":"calendar"}}`)
	price := add(`{"name":"Price","xKey":"price","kind":"object","xFormat":{"type":"money"}}`)
	where := add(`{"name":"Where","xKey":"where","kind":"object","xFormat":{"type":"geo"}}`)
	stars := add(`{"name":"Stars","xKey":"stars","kind":"number","xFormat":{"type":"rating","config":{"max":5}}}`)
	done := add(`{"name":"Done","xKey":"done","kind":"boolean","xFormat":{"type":"checkbox"}}`)
	custom := add(`{"name":"Custom","xKey":"custom","kind":"string","xFormat":{"type":"acme.widget","acme":{"anything":[1,2,{"deep":true}]}}}`)
	bare := add(`{"name":"Title","xKey":"title","kind":"string","meta":{"index":"basic"}}`)
	nulled := add(`{"name":"Nulled","xKey":"nulled","kind":"string","xFormat":null}`)
	notes := add(`{"name":"Notes","xKey":"notes","kind":"string","xFormat":{"type":"markdown"}}`)
	ref := add(`{"name":"Ref","xKey":"ref","kind":"string","xFormat":{"type":"text","links":"link"}}`)

	for name, tc := range map[string]struct {
		body string
		want int
		code string
	}{
		"kind missing":            {`{"name":"A","xKey":"a","xFormat":{"type":"text"}}`, 400, "request.schema"},
		"slug/kind mismatch":      {`{"name":"B","xKey":"b","kind":"string","xFormat":{"type":"choice"}}`, 400, "property.format_invalid"},
		"reserved tags":           {`{"name":"C","xKey":"c","kind":"array","xFormat":{"type":"tags"}}`, 400, "property.format_invalid"},
		"reserved validate":       {`{"name":"D","xKey":"d","kind":"string","xFormat":{"type":"text","validate":{}}}`, 400, "property.format_invalid"},
		"xFormat not object":      {`{"name":"E","xKey":"e","kind":"string","xFormat":"email"}`, 400, "request.invalid_field"},
		"icon not string":         {`{"name":"F","xKey":"f","kind":"string","xFormat":{"type":"text","icon":3}}`, 400, "request.invalid_field"},
		"option entry not object": {`{"name":"G","xKey":"g","kind":"array","xFormat":{"type":"choice","options":{"x":"nope"}}}`, 400, "request.invalid_field"},
		"unknown option leaf":     {`{"name":"H","xKey":"h","kind":"array","xFormat":{"type":"choice","options":{"x":{"weight":1}}}}`, 400, "request.invalid_field"},
		"unknown relation member": {`{"name":"I","xKey":"i","kind":"array","xFormat":{"type":"relation","relation":{"foo":1}}}`, 400, "request.invalid_field"},
		"unparseable filter":      {`{"name":"J","xKey":"j","kind":"array","xFormat":{"type":"relation","relation":{"filter":"{\"a\":{\"$nope\":1}}"}}}`, 400, "property.format_invalid"},
		"config object value":     {`{"name":"K","xKey":"k","kind":"number","xFormat":{"type":"number","config":{"a":{"b":1}}}}`, 400, "request.invalid_field"},
		"meta unknown key":        {`{"name":"L","xKey":"l","kind":"string","meta":{"pos":"a0"}}`, 400, "request.invalid_field"},
		"xKey conflict":           {`{"name":"Stage 2","xKey":"stage","kind":"string"}`, 409, "property.xkey_conflict"},
		"legacy format key":       {`{"name":"M","xKey":"m","kind":"string","format":{"type":"links"}}`, 400, "request.unknown_field"},
		"dotted option key":       {`{"name":"N","xKey":"n","kind":"array","xFormat":{"type":"choice","options":{"a.b":{"name":"X"}}}}`, 400, "request.invalid_field"},
		"dollar top-level key":    {`{"name":"O","xKey":"o","kind":"string","xFormat":{"$date":"2026-01-01T00:00:00Z"}}`, 400, "request.invalid_field"},
		"dollar key in vendor":    {`{"name":"P","xKey":"p","kind":"string","xFormat":{"type":"text","acme":{"list":[{"$date":"x"}]}}}`, 400, "request.invalid_field"},
		"empty option meta key":   {`{"name":"Q","xKey":"q","kind":"array","xFormat":{"type":"choice","options":{"a":{"meta":{"":"x"}}}}}`, 400, "request.invalid_field"},
		"links marker unknown":    {`{"name":"R","xKey":"r","kind":"string","xFormat":{"type":"text","links":"refs"}}`, 400, "property.format_invalid"},
		"links marker not string": {`{"name":"S","xKey":"s","kind":"string","xFormat":{"type":"text","links":true}}`, 400, "request.invalid_field"},
		"links marker kind":       {`{"name":"T","xKey":"t","kind":"string","xFormat":{"type":"text","links":"links"}}`, 400, "property.format_invalid"},
		"markdown on array":       {`{"name":"U","xKey":"u","kind":"array","xFormat":{"type":"markdown"}}`, 400, "property.format_invalid"},
	} {
		rec = doJSON(t, e, http.MethodPost, propsURL, tc.body)
		if rec.Code != tc.want {
			t.Errorf("%s: status=%d, want %d; body=%s", name, rec.Code, tc.want, rec.Body.String())
			continue
		}
		if code := errCode(t, rec.Body.Bytes()); code != tc.code {
			t.Errorf("%s: code=%q, want %q", name, code, tc.code)
		}
	}

	// --- read-back --------------------------------------------------------

	byId := func() map[string]api.PropertyDef {
		t.Helper()
		rec := doJSON(t, e, http.MethodGet, propsURL, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list properties: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var list api.PropertiesListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		out := map[string]api.PropertyDef{}
		for _, p := range list.Properties {
			out[p.Id] = p
		}
		return out
	}
	xf := func(p api.PropertyDef) map[string]any {
		t.Helper()
		if p.XFormat == nil {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal(p.XFormat, &m); err != nil {
			t.Fatalf("decode xFormat %s: %v", p.XFormat, err)
		}
		return m
	}
	props := byId()
	if s := xf(props[stage]); s["type"] != "choice" || s["pos"] != "a0" ||
		s["options"].(map[string]any)["lead"].(map[string]any)["color"] != "grey" ||
		s["options"].(map[string]any)["lead"].(map[string]any)["meta"].(map[string]any)["icon"] != "dot" ||
		s["config"].(map[string]any)["multiple"] != false {
		t.Errorf("stage descriptor read-back = %s", props[stage].XFormat)
	}
	if props[stage].Kind != api.PropertyKindArray || props[stage].XKey != "stage" {
		t.Errorf("stage def = %+v", props[stage])
	}
	if r := xf(props[related]); r["relation"].(map[string]any)["filter"] != `{"any.type":"page"}` ||
		r["relation"].(map[string]any)["targetTypes"].([]any)[0] != "doc" {
		t.Errorf("related descriptor read-back = %s", props[related].XFormat)
	}
	if c := xf(props[custom]); c["type"] != "acme.widget" || c["acme"] == nil {
		t.Errorf("vendor descriptor must read back verbatim: %s", props[custom].XFormat)
	}
	if props[bare].XFormat != nil || props[bare].Meta["index"] != "basic" {
		t.Errorf("bare def = %+v", props[bare])
	}
	if props[nulled].XFormat != nil {
		t.Errorf("xFormat: null must read as absent: %s", props[nulled].XFormat)
	}

	// --- value validation --------------------------------------------------

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", `{"type":"`+tr.TypeId+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var objResp api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &objResp); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	setURL := "/v1/spaces/" + sp.Id + "/properties/" + objResp.ObjectId + "/set/" + tr.TypeId
	set := func(propId, value string) int {
		t.Helper()
		rec := doJSON(t, e, http.MethodPost, setURL, `{"patch":{"`+propId+`":`+value+`}}`)
		if rec.Code == http.StatusBadRequest {
			if code := errCode(t, rec.Body.Bytes()); code != "property.format_violation" {
				t.Errorf("set %s=%s: code=%q, want property.format_violation", propId, value, code)
			}
		}
		return rec.Code
	}
	for name, tc := range []struct {
		name  string
		prop  string
		value string
		want  int
	}{
		{"choice one", stage, `["lead"]`, 200},
		{"choice two with multiple off", stage, `["lead","won"]`, 400},
		{"choice scalar", stage, `"lead"`, 400},
		{"choice empty key", stage, `[""]`, 400},
		{"choice unknown key is dangling-tolerant", stage, `["retired"]`, 200},
		{"relation two", related, `["any://objabc","any://objdef"]`, 200},
		{"relation bad uri", related, `["not-a-uri"]`, 400},
		{"relation global form", related, `["any://space1/objabc"]`, 400},
		{"relation typed form", related, `["any://o/space1/objabc"]`, 400},
		{"relation short id (kind namespace)", related, `["any://abc"]`, 400},
		{"markdown string", notes, `"see [x](any://abc123def)"`, 200},
		{"markdown not string", notes, `7`, 400},
		{"link marker keeps text checks", ref, `"any://abc123def"`, 200},
		{"date midnight", due, `{"$date":"2026-07-03T00:00:00Z"}`, 200},
		{"date not midnight", due, `{"$date":"2026-07-03T12:00:00Z"}`, 400},
		{"date bare string", due, `"2026-07-03"`, 400},
		{"datetime", when, `{"$date":"2026-07-03T12:00:00Z"}`, 200},
		{"datetime millis", when, `{"$date":1786014230123}`, 200},
		{"datetime bad", when, `{"$date":"tomorrow"}`, 400},
		{"datetime extra key", when, `{"$date":"2026-07-03T12:00:00Z","tz":"UTC"}`, 400},
		{"url", site, `"https://example.com/x"`, 200},
		{"url no scheme", site, `"example.com"`, 400},
		{"url number", site, `42`, 400},
		{"email", mail, `"a@example.com"`, 200},
		{"email no at", mail, `"a.example.com"`, 400},
		{"email two ats", mail, `"a@b@c"`, 400},
		{"period", trip, `{"from":{"$date":"2026-09-01T00:00:00Z"},"to":{"$date":"2026-09-14T00:00:00Z"}}`, 200},
		{"period open end", trip, `{"from":{"$date":"2026-09-01T00:00:00Z"}}`, 200},
		{"period reversed", trip, `{"from":{"$date":"2026-09-14T00:00:00Z"},"to":{"$date":"2026-09-01T00:00:00Z"}}`, 400},
		{"period bad part", trip, `{"from":"banana"}`, 400},
		{"period unknown key", trip, `{"from":{"$date":"2026-09-01T00:00:00Z"},"until":1}`, 400},
		{"period empty", trip, `{}`, 400},
		{"money", price, `{"amount":10.5,"currency":"USD"}`, 200},
		{"money amount string", price, `{"amount":"10","currency":"USD"}`, 400},
		{"money missing currency", price, `{"amount":10}`, 400},
		{"geo", where, `{"lat":52.52,"lng":13.4}`, 200},
		{"geo out of range", where, `{"lat":91,"lng":13.4}`, 400},
		{"geo missing lng", where, `{"lat":52.52}`, 400},
		{"rating", stars, `3`, 200},
		{"rating over max", stars, `6`, 400},
		{"rating string", stars, `"3"`, 400},
		{"checkbox", done, `true`, 200},
		{"checkbox number", done, `1`, 400},
		{"unknown slug takes anything the kind allows", custom, `"whatever"`, 200},
	} {
		if got := set(tc.prop, tc.value); got != tc.want {
			t.Errorf("%d %s: status=%d, want %d", name, tc.name, got, tc.want)
		}
	}

	// A null value passes the descriptor gate (unsets are not its
	// concern) — the SDK's own kind check rejects it downstream, proving
	// the gate didn't intercept it.
	rec = doJSON(t, e, http.MethodPost, setURL, `{"patch":{"`+due+`":null}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("null value: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if code := errCode(t, rec.Body.Bytes()); code == "property.format_violation" {
		t.Errorf("null value must not trip the descriptor gate (got %q)", code)
	}

	// initialProperties on object create go through the same gate.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		`{"type":"`+tr.TypeId+`","initialProperties":{"`+tr.TypeId+`":{"`+due+`":{"$date":"not-a-date"}}}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("initialProperties violation: status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		`{"type":"`+tr.TypeId+`","initialProperties":{"`+tr.TypeId+`":{"`+due+`":{"$date":"2026-01-01T00:00:00Z"}}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("initialProperties valid: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// --- PATCH --------------------------------------------------------------

	patch := func(propId, body string) *httptest.ResponseRecorder {
		t.Helper()
		return doJSON(t, e, http.MethodPatch, propsURL+"/"+propId, body)
	}
	if rec := patch(stage, `{"set":{"xFormat.options.qualified.name":"Qualified","xFormat.options.qualified.color":"blue","xFormat.options.qualified.pos":"a05"},"unset":["xFormat.options.won"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("option add+delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if opts := xf(byId()[stage])["options"].(map[string]any); opts["won"] != nil || opts["qualified"].(map[string]any)["color"] != "blue" || opts["lead"] == nil {
		t.Errorf("options after patch = %v", opts)
	}
	// Flipping arity is a leaf write; the value gate reads the new flag.
	if rec := patch(stage, `{"set":{"xFormat.config.multiple":true}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("set multiple: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := set(stage, `["lead","qualified"]`); got != http.StatusOK {
		t.Errorf("two values after multiple=true: status=%d", got)
	}
	// The slug moves within the kind, never across it.
	if rec := patch(stage, `{"set":{"xFormat.type":"relation"}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("slug within kind: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := patch(stage, `{"set":{"xFormat.type":"choice"}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("slug back: status=%d body=%s", rec.Code, rec.Body.String())
	}
	for name, tc := range map[string]struct {
		prop, body string
		want       int
		code       string
	}{
		"cross-kind slug":       {stage, `{"set":{"xFormat.type":"text"}}`, 400, "property.format_invalid"},
		"cross-kind links mark": {stage, `{"set":{"xFormat.links":"link"}}`, 400, "property.format_invalid"},
		"bad links mark":        {stage, `{"set":{"xFormat.links":"refs"}}`, 400, "property.format_invalid"},
		"object over option":    {stage, `{"set":{"xFormat.options.lead":{"name":"X"}}}`, 400, "request.invalid_field"},
		"object over bag":       {stage, `{"set":{"xFormat":{"type":"choice"}}}`, 400, "request.invalid_field"},
		"reserved key":          {stage, `{"set":{"xFormat.validate.min":1}}`, 400, "property.format_invalid"},
		"xKey conflict":         {stage, `{"set":{"xKey":"related"}}`, 409, "property.xkey_conflict"},
		"pinned kind":           {stage, `{"set":{"kind":"string"}}`, 400, "property.immutable"},
		"meta beyond index":     {stage, `{"set":{"meta.pos":"a0"}}`, 400, "request.invalid_field"},
		"legacy format path":    {stage, `{"set":{"format.ui":"select"}}`, 400, "request.invalid_field"},
		"legacy xKind path":     {stage, `{"set":{"xKind":"tags"}}`, 400, "request.invalid_field"},
		"bad filter text":       {related, `{"set":{"xFormat.relation.filter":"{\"a\":{\"$nope\":1}}"}}`, 400, "property.format_invalid"},
		"filter as object":      {related, `{"set":{"xFormat.relation.filter":{"a":1}}}`, 400, "request.invalid_field"},
		"targetTypes scalar":    {related, `{"set":{"xFormat.relation.targetTypes":"doc"}}`, 400, "request.invalid_field"},
		"config nested":         {related, `{"set":{"xFormat.config.a.b":1}}`, 400, "request.invalid_field"},
		"unknown option leaf":   {stage, `{"set":{"xFormat.options.lead.weight":1}}`, 400, "request.invalid_field"},
		"null on a string leaf": {stage, `{"set":{"name":null}}`, 400, "request.invalid_field"},
		"null on xKey":          {stage, `{"set":{"xKey":null}}`, 400, "request.invalid_field"},
		"dollar segment":        {stage, `{"set":{"xFormat.acme.$date":"x"}}`, 400, "request.invalid_field"},
		"dotted key in value":   {stage, `{"set":{"xFormat.acme.list":[{"a.b":1}]}}`, 400, "request.invalid_field"},
	} {
		rec := patch(tc.prop, tc.body)
		if rec.Code != tc.want {
			t.Errorf("%s: status=%d, want %d; body=%s", name, rec.Code, tc.want, rec.Body.String())
			continue
		}
		if code := errCode(t, rec.Body.Bytes()); code != tc.code {
			t.Errorf("%s: code=%q, want %q", name, code, tc.code)
		}
	}
	// Accepted: the handle, the index flag, a vendor array leaf, a
	// descriptor grown onto a bare property, a whole-bag unset.
	if rec := patch(stage, `{"set":{"xKey":"pipeline_stage","meta.index":"none","xFormat.acme.weights":[1,2]}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("accepted patch: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := patch(bare, `{"set":{"xFormat.type":"email","xFormat.icon":"envelope"}}`); rec.Code != http.StatusNoContent {
		t.Fatalf("grow descriptor: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := patch(custom, `{"unset":["xFormat"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("unset bag: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// A reserved key refuses a set but takes an unset — the repair path.
	if rec := patch(stage, `{"unset":["xFormat.validate","xFormat.compute.x"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("unset reserved: status=%d body=%s", rec.Code, rec.Body.String())
	}
	props = byId()
	if p := props[stage]; p.XKey != "pipeline_stage" || p.Meta["index"] != "none" || xf(p)["acme"] == nil {
		t.Errorf("stage after accepted patch = %+v", p)
	}
	if got := set(mail, `"x@y.z"`); got != http.StatusOK {
		t.Errorf("email still valid: %d", got)
	}
	if b := xf(props[bare]); b["type"] != "email" || b["icon"] != "envelope" {
		t.Errorf("grown descriptor = %s", props[bare].XFormat)
	}
	if got := set(bare, `"not an email"`); got != http.StatusBadRequest {
		t.Errorf("the grown slug must now gate values: %d", got)
	}
	if props[custom].XFormat != nil {
		t.Errorf("unset bag must read back absent: %s", props[custom].XFormat)
	}
}

// TestPatchPathToStorage covers the wire→storage path translation and
// the pinned/unknown-path rejections used by PATCH properties. Pure
// logic — no SDK or network.
func TestPatchPathToStorage(t *testing.T) {
	const inv = "request.invalid_field"
	cases := []struct {
		path        string
		forSet      bool
		wantStorage string
		wantCode    string
	}{
		{"name", true, "name", ""},
		{"description", true, "description", ""},
		{"xKey", true, "x-key", ""},
		{"meta.index", true, "meta.index", ""},
		{"meta", true, "", inv},
		{"meta", false, "meta", ""},
		{"meta.pos", true, "", inv},
		{"xFormat.type", true, "x-format.type", ""},
		{"xFormat.icon", true, "x-format.icon", ""},
		{"xFormat.pos", true, "x-format.pos", ""},
		{"xFormat.type.sub", true, "", inv},
		// Containers: rejected on set, allowed on unset (clear).
		{"xFormat", true, "", inv},
		{"xFormat", false, "x-format", ""},
		{"xFormat.options", true, "", inv},
		{"xFormat.options", false, "x-format.options", ""},
		{"xFormat.options.high", true, "", inv},
		{"xFormat.options.high", false, "x-format.options.high", ""},
		{"xFormat.options.high.name", true, "x-format.options.high.name", ""},
		{"xFormat.options.high.color", true, "x-format.options.high.color", ""},
		{"xFormat.options.high.pos", true, "x-format.options.high.pos", ""},
		{"xFormat.options.high.meta", true, "", inv},
		{"xFormat.options.high.meta", false, "x-format.options.high.meta", ""},
		{"xFormat.options.high.meta.icon", true, "x-format.options.high.meta.icon", ""},
		{"xFormat.options.high.meta.icon.x", true, "", inv},
		{"xFormat.options.high.weight", true, "", inv},
		{"xFormat.relation", true, "", inv},
		{"xFormat.relation", false, "x-format.relation", ""},
		{"xFormat.relation.targetTypes", true, "x-format.relation.targetTypes", ""},
		{"xFormat.relation.filter", true, "x-format.relation.filter", ""},
		{"xFormat.relation.foo", true, "", inv},
		{"xFormat.config", true, "", inv},
		{"xFormat.config", false, "x-format.config", ""},
		{"xFormat.config.multiple", true, "x-format.config.multiple", ""},
		{"xFormat.config.a.b", true, "", inv},
		{"xFormat.validate", true, "", "property.format_invalid"},
		{"xFormat.compute.x", true, "", "property.format_invalid"},
		{"xFormat.validate", false, "x-format.validate", ""},
		{"xFormat.compute.x", false, "x-format.compute.x", ""},
		{"xFormat.acme.$date", true, "", inv},
		// Vendor namespace: any depth.
		{"xFormat.acme", true, "x-format.acme", ""},
		{"xFormat.acme.deep.leaf", true, "x-format.acme.deep.leaf", ""},
		// Pinned.
		{"kind", true, "", "property.immutable"},
		{"scope", true, "", "property.immutable"},
		{"items", true, "", "property.immutable"},
		{"properties", true, "", "property.immutable"},
		{"key", false, "", "property.immutable"},
		// Malformed / unknown / retired.
		{"", true, "", inv},
		{"xFormat..type", true, "", inv},
		{"bogus", true, "", inv},
		{"xKey.sub", true, "", inv},
		{"xKind", true, "", inv},
		{"format.ui", true, "", inv},
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

// TestFieldPatchPathToStorage: a dataset field record mutates its
// display pair and descriptor; everything else is pinned.
func TestFieldPatchPathToStorage(t *testing.T) {
	cases := []struct {
		path        string
		forSet      bool
		wantStorage string
		wantCode    string
	}{
		{"name", true, "name", ""},
		{"description", true, "description", ""},
		{"xFormat.type", true, "x-format.type", ""},
		{"xFormat.options.high.name", true, "x-format.options.high.name", ""},
		{"xFormat", true, "", "request.invalid_field"},
		{"xFormat", false, "x-format", ""},
		{"key", true, "", "dataset.immutable"},
		{"kind", true, "", "dataset.immutable"},
		{"required", true, "", "dataset.immutable"},
		{"mutableBy", false, "", "dataset.immutable"},
		{"displayName", true, "", "dataset.immutable"},
		{"xKey", true, "", "dataset.immutable"},
		{"", true, "", "request.invalid_field"},
	}
	for _, tc := range cases {
		gotStorage, gotCode, reason := fieldPatchPathToStorage(tc.path, tc.forSet)
		if gotCode != tc.wantCode {
			t.Errorf("fieldPatchPathToStorage(%q, set=%v) code=%q want %q (reason=%q)", tc.path, tc.forSet, gotCode, tc.wantCode, reason)
		}
		if tc.wantCode == "" && gotStorage != tc.wantStorage {
			t.Errorf("fieldPatchPathToStorage(%q, set=%v) storage=%q want %q", tc.path, tc.forSet, gotStorage, tc.wantStorage)
		}
	}
}

// TestPatchSetValue covers value decoding: strings outside x-format,
// the object ban under it, typed interpreted leaves, free vendor
// leaves, and the error-code split.
func TestPatchSetValue(t *testing.T) {
	cases := []struct {
		path     string
		raw      string
		wantCode string
		want     any
	}{
		{"name", `"Priority"`, "", "Priority"},
		{"name", `42`, "request.invalid_field", nil},
		{"name", `null`, "request.invalid_field", nil},
		{"x-key", `null`, "request.invalid_field", nil},
		{"x-format.type", `"email"`, "", "email"},
		{"x-format.type", `3`, "request.invalid_field", nil},
		{"x-format.type", `""`, "property.format_invalid", nil},
		{"x-format.type", `"tags"`, "property.format_invalid", nil},
		{"x-format.type", `"acme.custom"`, "", "acme.custom"},
		{"x-format.icon", `"tag"`, "", "tag"},
		{"x-format.relation.filter", `"{\"type\":\"page\"}"`, "", `{"type":"page"}`},
		{"x-format.relation.filter", `{"type":"page"}`, "request.invalid_field", nil},
		{"x-format.relation.filter", `"{\"$nope\":1}"`, "property.format_invalid", nil},
		{"x-format.relation.targetTypes", `"doc"`, "request.invalid_field", nil},
		{"x-format.config.multiple", `true`, "", true},
		{"x-format.config.multiple", `{"a":1}`, "request.invalid_field", nil},
		{"x-format.config.multiple", `[1]`, "request.invalid_field", nil},
		{"x-format.options.high.name", `"High"`, "", "High"},
		{"x-format.options.high.name", `1`, "request.invalid_field", nil},
		{"x-format.acme.widget", `"compact"`, "", "compact"},
		{"x-format.acme.widget", `{"a":1}`, "request.invalid_field", nil},
		{"x-format.acme.widget", `not json`, "request.invalid_field", nil},
		{"x-format.acme.list", `[{"$date":"2026"}]`, "request.invalid_field", nil},
		{"x-format.acme.list", `[{"a.b":1}]`, "request.invalid_field", nil},
		{"x-format.acme.list", `[{"ok":1}]`, "", nil},
	}
	for _, tc := range cases {
		got, code, reason := patchSetValue(tc.path, json.RawMessage(tc.raw))
		if code != tc.wantCode {
			t.Errorf("patchSetValue(%q, %s) code=%q want %q (reason=%q)", tc.path, tc.raw, code, tc.wantCode, reason)
			continue
		}
		if tc.wantCode == "" && tc.want != nil && got != tc.want {
			t.Errorf("patchSetValue(%q, %s) = %v (%T), want %v", tc.path, tc.raw, got, got, tc.want)
		}
	}
	// Array leaves decode to []any.
	got, code, _ := patchSetValue("x-format.relation.targetTypes", json.RawMessage(`["a","b"]`))
	if code != "" || len(got.([]any)) != 2 {
		t.Errorf("targetTypes decode = %v (%q)", got, code)
	}
	got, code, _ = patchSetValue("x-format.acme.weights", json.RawMessage(`[1,2]`))
	if code != "" || len(got.([]any)) != 2 {
		t.Errorf("vendor array decode = %v (%q)", got, code)
	}
}
