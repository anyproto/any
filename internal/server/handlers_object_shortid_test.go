package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

func TestIsShortId(t *testing.T) {
	cases := map[string]bool{
		"abcd":                  false, // below the minimum
		"abcde":                 true,
		"ab3xqd":                true,
		"z7z7z7":                true,
		"ab3xq1":                false, // 1 is outside base32
		"AB3XQD":                false,
		"ab-xqd":                false,
		"":                      false,
		strings.Repeat("a", 52): true,
		strings.Repeat("a", 53): false,
	}
	for id, want := range cases {
		if got := isShortId(id); got != want {
			t.Errorf("isShortId(%q) = %v, want %v", id, got, want)
		}
	}
}

// An ambiguous short id lists at most shortIdCandidatesMax matches.
func TestAmbiguousIdError_Cap(t *testing.T) {
	matches := make([]api.ObjectCandidate, 0, shortIdCandidatesMax+1)
	for i := 0; i <= shortIdCandidatesMax; i++ {
		matches = append(matches, api.ObjectCandidate{
			Id: fmt.Sprintf("bafyreiab3xqd%02d", i), Name: fmt.Sprintf("n%d", i),
		})
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)
	if err := ambiguousIdError(c, "ab3xqd", matches); err != nil {
		t.Fatal(err)
	}
	env := decodeAmbiguous(t, rec)
	got := env.Error.Details.Candidates
	if len(got) != shortIdCandidatesMax {
		t.Fatalf("candidates = %d, want %d", len(got), shortIdCandidatesMax)
	}
	for i, cand := range got {
		if cand != matches[i] {
			t.Errorf("candidate %d = %+v, want %+v", i, cand, matches[i])
		}
	}
	if !strings.Contains(env.Error.Message, "more than 10") {
		t.Errorf("message = %q", env.Error.Message)
	}
}

type ambiguousEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			ShortId    string                `json:"shortId"`
			Candidates []api.ObjectCandidate `json:"candidates"`
		} `json:"details"`
	} `json:"error"`
}

func decodeAmbiguous(t *testing.T, rec *httptest.ResponseRecorder) ambiguousEnvelope {
	t.Helper()
	var env ambiguousEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if rec.Code != http.StatusConflict || env.Error.Code != api.ErrObjectAmbiguousId {
		t.Fatalf("got %d %s, want 409 %s (%s)", rec.Code, env.Error.Code, api.ErrObjectAmbiguousId, rec.Body.String())
	}
	return env
}

// absentObjectId is a well-formed full id no object has.
var absentObjectId = objectIdPrefix + strings.Repeat("z", shortIdMaxLen)

// shortOf is the six-character short id of a full object id.
func shortOf(id string) string {
	return id[len(objectIdPrefix) : len(objectIdPrefix)+6]
}

// doJSONDeadline is doJSON with a deadline: a stream that should have
// been refused ends the test instead of hanging it.
func doJSONDeadline(t *testing.T, e http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r.WithContext(ctx))
	return rec
}

// shortIdRoute is one object-addressing request, built for a given id.
// names, when set, checks that the 200 reply is the object's own.
type shortIdRoute struct {
	name   string
	method string
	path   func(id string) string
	body   func(id string) string
	names  func(t *testing.T, reply []byte)
}

// TestShortObjectIds drives the three places an object id arrives (the
// path param, the body objectId, the files query string) with a short
// id: a unique one acts on the object, an unknown one is 404, a
// malformed one is 400.
func TestShortObjectIds(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, typeId, objectId := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId
	short := shortOf(objectId)

	partId := mustAddPart(t, e, spaceId, typeId, `{"key":"notes","name":"Notes","ui":{"type":"table"}}`)
	rec := doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts/"+partId+"/datasets",
		`{"key":"notes","idRule":"user","fields":[{"key":"title","kind":"string","mutableBy":"any"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add dataset: %d %s", rec.Code, rec.Body.String())
	}
	var ds api.AddDatasetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ds); err != nil {
		t.Fatal(err)
	}
	dataset := ds.Collection

	chatSpace, chatId := setupChatFixture(t, e)
	chatBase := "/v1/spaces/" + chatSpace

	noBody := func(string) string { return "" }
	bodyWith := func(rest string) func(string) string {
		return func(id string) string { return `{"objectId":"` + id + `","dataset":"` + dataset + `"` + rest + `}` }
	}
	// These routes answer 200 for an id of no object too, so the reply
	// has to name the object.
	echoesId := func(t *testing.T, reply []byte) {
		var got struct {
			ObjectId string `json:"objectId"`
		}
		if err := json.Unmarshal(reply, &got); err != nil || got.ObjectId != objectId {
			t.Errorf("reply names %q, want %q (%s)", got.ObjectId, objectId, reply)
		}
	}
	hasRecord := func(t *testing.T, reply []byte) {
		var got struct {
			Record json.RawMessage `json:"record"`
		}
		if err := json.Unmarshal(reply, &got); err != nil || len(got.Record) == 0 || string(got.Record) == "null" {
			t.Errorf("no record in %s", reply)
		}
	}
	routes := []shortIdRoute{
		{"path get", http.MethodGet, func(id string) string { return base + "/objects/" + id }, noBody, echoesId},
		{"path properties", http.MethodGet, func(id string) string { return base + "/properties/" + id }, noBody, hasRecord},
		{"path history", http.MethodGet, func(id string) string { return base + "/objects/" + id + "/history" }, noBody, nil},
		{"path sync-status", http.MethodGet, func(id string) string { return base + "/sync-status/objects/" + id }, noBody, echoesId},
		{"body upsert", http.MethodPost, func(string) string { return base + "/upsert" },
			bodyWith(`,"records":[{"id":"n1","fields":{"title":"one"}}]`), nil},
		{"body modify", http.MethodPost, func(string) string { return base + "/modify" },
			bodyWith(`,"records":[{"id":"n1","ops":[{"type":"$set","path":"title","value":"two"}]}]`), nil},
		{"body query", http.MethodPost, func(string) string { return base + "/query" }, bodyWith(""), nil},
		{"body aggregate", http.MethodPost, func(string) string { return base + "/aggregate" },
			bodyWith(`,"pipeline":[{"$count":"n"}]`), nil},
		{"body delete-records", http.MethodPost, func(string) string { return base + "/delete-records" },
			bodyWith(`,"recordIds":["n1"]`), nil},
		{"query files", http.MethodGet, func(id string) string { return base + "/files?objectId=" + id }, noBody, nil},
	}

	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			rec := doJSON(t, e, r.method, r.path(short), r.body(short))
			if rec.Code != http.StatusOK {
				t.Errorf("unique: %d %s", rec.Code, rec.Body.String())
			} else if r.names != nil {
				r.names(t, rec.Body.Bytes())
			}
			// The first character after the prefix is one of a–h, so no
			// object matches this one.
			rec = doJSON(t, e, r.method, r.path("zzzzzz"), r.body("zzzzzz"))
			if rec.Code != http.StatusNotFound {
				t.Errorf("unknown: %d %s", rec.Code, rec.Body.String())
			}
			assertErrorCode(t, rec, "object.not_found")
			for _, bad := range []string{"abcd", "ab-xqd", "bafyreiabcd", objectId + "a"} {
				rec := doJSON(t, e, r.method, r.path(bad), r.body(bad))
				if rec.Code != http.StatusBadRequest {
					t.Errorf("malformed %q: %d %s", bad, rec.Code, rec.Body.String())
				}
				assertErrorCode(t, rec, "request.invalid_field")
			}
			// A serialized nil with the shape of a short id.
			rec = doJSON(t, e, r.method, r.path("undefined"), r.body("undefined"))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("serialized nil: %d %s", rec.Code, rec.Body.String())
			}
			assertErrorCode(t, rec, "object.id_required")
		})
	}

	t.Run("responses carry the full id", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodGet, base+"/objects/"+short, "")
		var got api.ObjectGetResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.ObjectId != objectId {
			t.Errorf("objectId = %q, want %q", got.ObjectId, objectId)
		}
	})

	t.Run("a write by short id lands on the object", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, base+"/upsert",
			`{"objectId":"`+short+`","dataset":"`+dataset+`","records":[{"id":"n2","fields":{"title":"kept"}}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("upsert: %d %s", rec.Code, rec.Body.String())
		}
		rec = doJSON(t, e, http.MethodPost, base+"/query",
			`{"objectId":"`+objectId+`","dataset":"`+dataset+`","filter":{"id":"n2"}}`)
		var got api.QueryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Records) != 1 {
			t.Errorf("records = %d, want 1 (%s)", len(got.Records), rec.Body.String())
		}
	})

	t.Run("files by short id", func(t *testing.T) {
		rec := doRaw(t, e, http.MethodPost, base+"/objects/"+short+"/files?name=a.txt", "text/plain", []byte("body"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("attach: %d %s", rec.Code, rec.Body.String())
		}
		var attached api.FileInfo
		if err := json.Unmarshal(rec.Body.Bytes(), &attached); err != nil {
			t.Fatal(err)
		}
		if attached.ObjectId != objectId {
			t.Errorf("attached to %q, want %q", attached.ObjectId, objectId)
		}
		// The list is filtered by the resolved id: the object's file is
		// there, and another object's short id lists none.
		var list api.FileListResponse
		decodeGet(t, e, base+"/files?objectId="+short, &list)
		if len(list.Files) != 1 || list.Files[0].FileId != attached.FileId {
			t.Errorf("files of %s = %+v, want %s", short, list.Files, attached.FileId)
		}
		other := mustCreateObject(t, e, spaceId, `{"type":"`+typeId+`"}`)
		decodeGet(t, e, base+"/files?objectId="+shortOf(other), &list)
		if len(list.Files) != 0 {
			t.Errorf("files of another object = %+v, want none", list.Files)
		}
		rec = doJSON(t, e, http.MethodPost, base+"/objects/"+short+"/files/query", `{}`)
		var rows api.QueryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows.Records) != 1 {
			t.Errorf("files/query: %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("chat message routes", func(t *testing.T) {
		chatShort := shortOf(chatId)
		rec := doJSON(t, e, http.MethodPost, chatBase+"/objects/"+chatShort+"/chat/messages", `{"text":"hi"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
		}
		msgId := decodeModifyResult(t, rec.Body.Bytes()).RecordIds[0]
		rec = doJSON(t, e, http.MethodPatch, chatBase+"/objects/"+chatShort+"/chat/messages/"+msgId, `{"text":"edited"}`)
		if rec.Code != http.StatusOK {
			t.Errorf("edit: %d %s", rec.Code, rec.Body.String())
		}
		if got := getChatMsg(t, e, chatBase+"/objects/"+chatId, msgId); got.Text != "edited" {
			t.Errorf("text = %q, want edited", got.Text)
		}
	})

	t.Run("a full id cut short", func(t *testing.T) {
		for _, cut := range []string{objectId[:12], objectId[:15], objectId[:58]} {
			var got api.ObjectGetResponse
			decodeGet(t, e, base+"/objects/"+cut, &got)
			if got.ObjectId != objectId {
				t.Errorf("%s names %q, want %q", cut, got.ObjectId, objectId)
			}
		}
		cut := objectId[:15]
		rec := doJSON(t, e, http.MethodPost, base+"/query",
			`{"objectId":"`+cut+`","dataset":"`+dataset+`","filter":{"id":"n2"}}`)
		var rows api.QueryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows.Records) != 1 {
			t.Errorf("body: %d %s", rec.Code, rec.Body.String())
		}
		var list api.FileListResponse
		decodeGet(t, e, base+"/files?objectId="+cut, &list)
		if len(list.Files) != 1 {
			t.Errorf("query string: files = %+v, want one", list.Files)
		}
	})

	t.Run("a 59-character id is used as is", func(t *testing.T) {
		for _, id := range []string{absentObjectId, strings.Repeat("X", objectIdLen)} {
			rec := doJSON(t, e, http.MethodGet, base+"/objects/"+id, "")
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: %d %s", id, rec.Code, rec.Body.String())
			}
			assertErrorCode(t, rec, "object.not_found")
		}
	})

	t.Run("delete", func(t *testing.T) {
		if rec := doJSON(t, e, http.MethodDelete, base+"/objects/"+short, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
		}
		if rec := doJSON(t, e, http.MethodGet, base+"/objects/"+objectId, ""); rec.Code != http.StatusGone {
			t.Errorf("get after delete: %d %s", rec.Code, rec.Body.String())
		}
		// A deleted object has no row, and nothing else shares this
		// short id.
		if rec := doJSON(t, e, http.MethodGet, base+"/objects/"+short, ""); rec.Code != http.StatusNotFound {
			t.Errorf("short get after delete: %d %s", rec.Code, rec.Body.String())
		}
	})
}

// TestShortObjectIds_EveryObjectRoute: every registered route with an
// :objectId segment resolves it: a malformed id is 400 and a short id of
// no object is 404.
func TestShortObjectIds_EveryObjectRoute(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	spaceId := mustCreateSpace(t, e, "ShortIdRoutes")

	// Routes that vet their body before the object need a valid one.
	bodies := map[string]string{
		"PATCH /v1/spaces/:spaceId/objects/:objectId/editor/:collection/markdown": `{"edits":[{"oldText":"a","newText":"b"}]}`,
	}
	seen := 0
	for _, route := range e.Routes() {
		if !strings.Contains(route.Path, ":objectId") {
			continue
		}
		seen++
		body := bodies[route.Method+" "+route.Path]
		if body == "" && (route.Method == http.MethodPost || route.Method == http.MethodPut || route.Method == http.MethodPatch) {
			body = "{}"
		}
		for id, want := range map[string]struct {
			status int
			code   string
		}{
			"ab-xqd":        {http.StatusBadRequest, "request.invalid_field"},
			"abcd":          {http.StatusBadRequest, "request.invalid_field"},
			"bafyreiabcd":   {http.StatusBadRequest, "request.invalid_field"},
			"zzzzzz":        {http.StatusNotFound, "object.not_found"},
			"bafyreizzzzzz": {http.StatusNotFound, "object.not_found"},
		} {
			var segs []string
			for _, seg := range strings.Split(route.Path, "/") {
				switch {
				case seg == ":spaceId":
					seg = spaceId
				case seg == ":objectId":
					seg = id
				case strings.HasPrefix(seg, ":"):
					seg = "x"
				}
				segs = append(segs, seg)
			}
			rec := doJSONDeadline(t, e, route.Method, strings.Join(segs, "/"), body)
			if rec.Code != want.status || errEnvCode(t, rec.Body.Bytes()) != want.code {
				t.Errorf("%s %s with %q: %d %s, want %d %s", route.Method, route.Path, id,
					rec.Code, rec.Body.String(), want.status, want.code)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no :objectId route registered")
	}
}

// TestShortObjectIds_Ambiguous: a short id several objects share is
// 409 object.ambiguous_id listing them, and nothing is executed.
func TestShortObjectIds_Ambiguous(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	// The first character after the prefix has eight values, so nine
	// objects always hold a one-character collision.
	defer func(n int) { shortIdMinLen = n }(shortIdMinLen)
	shortIdMinLen = 1

	spaceId, typeId, _ := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId
	names := map[string]string{}
	byHead := map[string][]string{}
	var shared string
	for i := 0; i < 9; i++ {
		name := fmt.Sprintf("object %d", i)
		id := mustCreateObject(t, e, spaceId,
			`{"type":"`+typeId+`","initialProperties":{"any":{"name":"`+name+`"}}}`)
		names[id] = name
		head := id[len(objectIdPrefix) : len(objectIdPrefix)+1]
		byHead[head] = append(byHead[head], id)
		if len(byHead[head]) > 1 {
			shared = head
		}
	}
	if shared == "" {
		t.Fatal("no two objects share a first character")
	}

	env := decodeAmbiguous(t, doJSON(t, e, http.MethodGet, base+"/objects/"+shared, ""))
	if env.Error.Details.ShortId != shared {
		t.Errorf("shortId = %q, want %q", env.Error.Details.ShortId, shared)
	}
	listed := map[string]bool{}
	for _, cand := range env.Error.Details.Candidates {
		if !strings.HasPrefix(cand.Id, objectIdPrefix+shared) || len(cand.Id) != objectIdLen {
			t.Errorf("candidate %q does not carry the short id", cand.Id)
		}
		if want, ok := names[cand.Id]; ok && cand.Name != want {
			t.Errorf("candidate %s name = %q, want %q", cand.Id, cand.Name, want)
		}
		listed[cand.Id] = true
	}
	if len(env.Error.Details.Candidates) < shortIdCandidatesMax {
		for _, id := range byHead[shared] {
			if !listed[id] {
				t.Errorf("candidates miss %s", id)
			}
		}
	}

	// So does the same id sent as a full id cut short.
	cut := objectIdPrefix + shared
	if env := decodeAmbiguous(t, doJSON(t, e, http.MethodGet, base+"/objects/"+cut, "")); env.Error.Details.ShortId != cut {
		t.Errorf("shortId = %q, want %q", env.Error.Details.ShortId, cut)
	}

	// Every kind of site refuses the same way.
	body := func(rest string) string { return `{"objectId":"` + shared + `","dataset":"x"` + rest + `}` }
	for _, r := range []struct{ method, path, body string }{
		{http.MethodPost, base + "/query", body("")},
		{http.MethodPost, base + "/query/subscribe", body("")},
		{http.MethodPost, base + "/aggregate", body(`,"pipeline":[{"$count":"n"}]`)},
		{http.MethodPost, base + "/modify", body(`,"records":[{"id":"a","ops":[{"type":"$set","path":"t","value":1}]}]`)},
		{http.MethodPost, base + "/upsert", body(`,"records":[{"id":"a","fields":{"t":1}}]`)},
		{http.MethodPost, base + "/delete-records", body(`,"recordIds":["a"]`)},
		{http.MethodGet, base + "/files?objectId=" + shared, ""},
		{http.MethodPatch, base + "/objects/" + shared + "/chat/messages/m1", `{"text":"x"}`},
		{http.MethodDelete, base + "/objects/" + shared, ""},
	} {
		decodeAmbiguous(t, doJSONDeadline(t, e, r.method, r.path, r.body))
	}

	// The refused delete removed nothing.
	for _, id := range byHead[shared] {
		if rec := doJSON(t, e, http.MethodGet, base+"/objects/"+id, ""); rec.Code != http.StatusOK {
			t.Errorf("object %s after a refused delete: %d", id, rec.Code)
		}
	}
}
