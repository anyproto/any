package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any/internal/api"
)

func newTestContext(body string) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(http.MethodPost, "/", nil)
	} else {
		req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

// A serialized nil in the :objectId path segment ("None", "null", …)
// is the recognizable signature of an unset caller variable — it must
// answer 400 object.id_required at the boundary, before any tree
// lookup, so the caller fixes the variable instead of investigating a
// "missing" object.
func TestResolveSpaceObject_SerializedNilId(t *testing.T) {
	for _, id := range []string{"None", "null", "nil", "undefined", "<nil>", "NONE", "[object Object]"} {
		c, rec := newTestContext("")
		c.SetParamNames("spaceId", "objectId")
		c.SetParamValues("sp1", id)
		_, _, _, done := (&deps{}).resolveSpaceObject(c)
		if !done {
			t.Fatalf("id %q passed the boundary gate", id)
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("id %q: status = %d, want 400", id, rec.Code)
		}
		if got := errEnvCode(t, rec.Body.Bytes()); got != "object.id_required" {
			t.Errorf("id %q: code = %q, want object.id_required", id, got)
		}
	}
	// A real-looking id must reach resolveSpace untouched — probe with
	// the msg variant too, which shares the gate.
	c, _ := newTestContext("")
	c.SetParamNames("spaceId", "objectId", "msgId")
	c.SetParamValues("", "None", "m1")
	_, _, _, _, done := (&deps{}).resolveSpaceObjectMsg(c)
	if !done {
		t.Fatal("resolveSpaceObjectMsg let a serialized-nil objectId through")
	}
}

func TestCheckUnknownFields(t *testing.T) {
	c, rec := newTestContext("")
	root := fastjson.MustParse(`{"filter": {}, "filters": {}, "sortt": []}`)
	_, done := checkUnknownFields(c, root, "", queryBodyFields...)
	if !done {
		t.Fatal("unknown fields not rejected")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "request.unknown_field" {
		t.Errorf("code = %q, want request.unknown_field", env.Error.Code)
	}
	for _, want := range []string{`"filters", "sortt"`, "filter, sort, limit"} {
		if !strings.Contains(env.Error.Message, want) {
			t.Errorf("message %q does not contain %q", env.Error.Message, want)
		}
	}

	// Accepted-only body passes.
	c, _ = newTestContext("")
	if _, done := checkUnknownFields(c, fastjson.MustParse(`{"filter": {}, "limit": 5}`), "", queryBodyFields...); done {
		t.Error("accepted fields rejected")
	}

	// nil root (empty body) passes.
	c, _ = newTestContext("")
	if _, done := checkUnknownFields(c, nil, "", queryBodyFields...); done {
		t.Error("nil root rejected")
	}

	// Non-object body is rejected — none of its "fields" would ever be
	// read, the same silent-drop class as an unknown key.
	c, rec = newTestContext("")
	if _, done := checkUnknownFields(c, fastjson.MustParse(`"hello"`), "", queryBodyFields...); !done {
		t.Fatal("non-object body not rejected")
	}
	if got := errEnvCode(t, rec.Body.Bytes()); got != "request.schema" {
		t.Errorf("non-object body: code = %q, want request.schema", got)
	}

	// The hint lands in the message.
	c, rec = newTestContext("")
	_, _ = checkUnknownFields(c, fastjson.MustParse(`{"name": "x"}`), objectCreateFieldsHint, "types", "initialProperties", "nav")
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !strings.Contains(env.Error.Message, "initialProperties") {
		t.Errorf("hint missing from message %q", env.Error.Message)
	}
}

// Inline `properties` on type create must reject loudly — a lax bind
// drops the key and reads as "properties never sync". The strict bind
// names the accepted fields and the per-field route.
func TestBindBodyStrict_TypeCreateUnknownField(t *testing.T) {
	c, rec := newTestContext(`{"name": "Movie", "xKey": "movie", "properties": [{"name": "year"}]}`)
	req, ok := bindBodyStrict[api.TypesCreateRequest](c, "add each property via POST …/types/{typeId}/properties")
	if ok || req != nil {
		t.Fatal("unknown field accepted")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "request.unknown_field" {
		t.Errorf("code = %q, want request.unknown_field", env.Error.Code)
	}
	for _, want := range []string{`"properties"`, "name, description, iconCid, xKey", "POST …/types/{typeId}/properties"} {
		if !strings.Contains(env.Error.Message, want) {
			t.Errorf("message %q does not contain %q", env.Error.Message, want)
		}
	}

	// The happy path still binds.
	c, _ = newTestContext(`{"name": "Movie", "xKey": "movie"}`)
	req, ok = bindBodyStrict[api.TypesCreateRequest](c, "")
	if !ok || req.XKey != "movie" {
		t.Fatalf("valid body rejected: ok=%v req=%+v", ok, req)
	}
}

// A bare-string body ("hello" instead of {"text": "hello"}) must name
// the expected shape, not answer a mute "invalid request body".
func TestBindBody_BadJSONNamesExpectedShape(t *testing.T) {
	c, rec := newTestContext(`"hello"`)
	_, ok := bindBody[api.ChatSendRequest](c)
	if ok {
		t.Fatal("bare string bound as ChatSendRequest")
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Error.Code != "request.bad_json" {
		t.Errorf("code = %q, want request.bad_json", env.Error.Code)
	}
	for _, want := range []string{"text", "got JSON string"} {
		if !strings.Contains(env.Error.Message, want) {
			t.Errorf("message %q does not contain %q", env.Error.Message, want)
		}
	}

	// Syntax errors report the failure position and the expected fields.
	c, rec = newTestContext(`{"text": `)
	if _, ok := bindBody[api.ChatSendRequest](c); ok {
		t.Fatal("truncated JSON bound")
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !strings.Contains(env.Error.Message, "not valid JSON") || !strings.Contains(env.Error.Message, "text") {
		t.Errorf("syntax-error message %q lacks position/shape", env.Error.Message)
	}
}

// Full-stack pins for the strict boundaries: unknown create/query keys,
// non-object initialProperties groups, inline type properties. Skips
// without the staging nodeconf, like every newTestDeps test.
func TestRequestSchemaBoundaries(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"SchemaTest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var sp api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &sp); err != nil {
		t.Fatalf("decode space: %v", err)
	}

	cases := []struct {
		name, method, path, body, wantCode string
	}{
		{"create unknown key", http.MethodPost, "/v1/spaces/" + sp.Id + "/objects",
			`{"any": {"name": "x"}, "types": ["nav"]}`, "request.unknown_field"},
		{"create bad group", http.MethodPost, "/v1/spaces/" + sp.Id + "/objects",
			`{"initialProperties": {"any": "Dune"}}`, "request.schema"},
		{"create types not array", http.MethodPost, "/v1/spaces/" + sp.Id + "/objects",
			`{"types": "nav"}`, "request.schema"},
		{"objects query filters typo", http.MethodPost, "/v1/spaces/" + sp.Id + "/objects/query",
			`{"filters": {"any.name": "x"}}`, "request.unknown_field"},
		{"per-object query unknown key", http.MethodPost, "/v1/spaces/" + sp.Id + "/query",
			`{"objectId": "o", "dataset": "chat_messages", "filtre": {}}`, "request.unknown_field"},
		{"per-object query nil objectId", http.MethodPost, "/v1/spaces/" + sp.Id + "/query",
			`{"objectId": "None", "dataset": "chat_messages"}`, "object.id_required"},
		{"space-list query unknown key", http.MethodPost, "/v1/spaces/query",
			`{"dataset": "spaces", "total": true}`, "request.unknown_field"},
		{"type create inline properties", http.MethodPost, "/v1/spaces/" + sp.Id + "/types",
			`{"name": "Movie", "xKey": "movie", "properties": [{"name": "year", "kind": "number"}]}`, "request.unknown_field"},
		{"markdown nil objectId", http.MethodPut, "/v1/spaces/" + sp.Id + "/objects/None/editor/markdown",
			`{"content": "hi"}`, "object.id_required"},
	}
	for _, tc := range cases {
		rec := doJSON(t, e, tc.method, tc.path, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", tc.name, rec.Code, rec.Body.String())
			continue
		}
		if got := errEnvCode(t, rec.Body.Bytes()); got != tc.wantCode {
			t.Errorf("%s: code = %q, want %q", tc.name, got, tc.wantCode)
		}
	}

	// The accepted vocabulary still works end-to-end: a create with all
	// three fields plus a query with every documented key answers 2xx.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects",
		`{"types": ["nav"], "initialProperties": {"any": {"name": "Dune"}}, "nav": {"type": 2}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("full create: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query",
		`{"filter": {"nav.type": 2}, "sort": ["nav.pos"], "limit": 10, "offset": 0, "includeTotal": true, "projection": {}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("full query: %d %s", rec.Code, rec.Body.String())
	}
}
