package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-sync-sdk/space"
)

// newEchoCtx hands parseProjection a live echo.Context — the parse path
// writes its 400s through it.
func newEchoCtx() (echo.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	return echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec), rec
}

func mustBody(t *testing.T, body string) *fastjson.Value {
	t.Helper()
	var p fastjson.Parser
	v, err := p.Parse(`{"projection":` + body + `}`)
	if err != nil {
		t.Fatalf("parse body: %v", err)
	}
	return v
}

// parseProj is the test-local parse: valid bodies only.
func parseProj(t *testing.T, body string) *projection {
	t.Helper()
	return parseProjMode(t, body, false)
}

// parseProjFreeform parses a projection over plain any-store documents
// (the local store), where there is no protocol namespace.
func parseProjFreeform(t *testing.T, body string) *projection {
	t.Helper()
	return parseProjMode(t, body, true)
}

func parseProjMode(t *testing.T, body string, freeform bool) *projection {
	t.Helper()
	c, _ := newEchoCtx()
	proj, errResp, done := parseProjection(c, mustBody(t, body), freeform)
	if done {
		t.Fatalf("projection %s rejected: %v", body, errResp)
	}
	return proj
}

// shape runs one record through a shaper and returns the wire JSON as a
// generic map, which is what assertions read.
func shape(t *testing.T, s recordShaper, recordJSON string) map[string]any {
	t.Helper()
	doc, err := anyenc.ParseJson(recordJSON)
	if err != nil {
		t.Fatalf("parse record: %v", err)
	}
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	var out map[string]any
	if err := json.Unmarshal(s.record(doc, fa).MarshalTo(nil), &out); err != nil {
		t.Fatalf("unmarshal shaped record: %v", err)
	}
	return out
}

const projTestRecord = `{
  "id": "rec1",
  "_ver": {"id":"v1","any":{"types":"v1","name":"v3"},"nav":{"pos":"v1","type":"v1"},"spaceId":"v1"},
  "any": {"types":["page"],"name":"Doc"},
  "nav": {"pos":"aa","type":1},
  "spaceId": "space1",
  "author": "acct1",
  "_addSeq": 7,
  "_applySeq": 9
}`

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestProjection_IncludeMode(t *testing.T) {
	got := shape(t, recordShaper{proj: parseProj(t, `{"any":1,"nav":1}`)}, projTestRecord)

	// id rides along unlisted; the delivery counters do not.
	for _, want := range []string{"id", "any", "nav", "_ver"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q; got %v", want, keys(got))
		}
	}
	for _, gone := range []string{"spaceId", "author", "_addSeq", "_applySeq"} {
		if _, ok := got[gone]; ok {
			t.Errorf("%q should not be projected; got %v", gone, keys(got))
		}
	}

	// _ver follows the projection without the client naming a _ver path.
	ver, _ := got["_ver"].(map[string]any)
	for _, want := range []string{"id", "any", "nav"} {
		if _, ok := ver[want]; !ok {
			t.Errorf("_ver missing %q; got %v", want, keys(ver))
		}
	}
	if _, ok := ver["spaceId"]; ok {
		t.Errorf("_ver.spaceId should have been narrowed away; got %v", keys(ver))
	}
	// Subtrees are copied verbatim, not collapsed to their max — a
	// collapsed "v3" here would tell the client any.types is at v3 when
	// it is really at v1, which is the direction that makes a client
	// discard a live local edit.
	anyVer, _ := ver["any"].(map[string]any)
	if anyVer["types"] != "v1" || anyVer["name"] != "v3" {
		t.Errorf("_ver.any should be verbatim per-leaf, got %v", anyVer)
	}
}

func TestProjection_ExcludeMode(t *testing.T) {
	got := shape(t, recordShaper{proj: parseProj(t, `{"_ver":-1,"spaceId":-1}`)}, projTestRecord)

	// No user-field inclusion → every user field survives.
	for _, want := range []string{"id", "any", "nav", "author"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q; got %v", want, keys(got))
		}
	}
	for _, gone := range []string{"_ver", "spaceId", "_addSeq", "_applySeq"} {
		if _, ok := got[gone]; ok {
			t.Errorf("%q should have been dropped; got %v", gone, keys(got))
		}
	}
}

func TestProjection_DeliveryCountersOptIn(t *testing.T) {
	got := shape(t, recordShaper{proj: parseProj(t, `{"any":1,"_addSeq":1}`)}, projTestRecord)
	if _, ok := got["_addSeq"]; !ok {
		t.Errorf("_addSeq was named explicitly and should be present; got %v", keys(got))
	}
	if _, ok := got["_applySeq"]; ok {
		t.Errorf("_applySeq was not named and should be absent; got %v", keys(got))
	}
	// Naming a protocol field is not a user-field inclusion, so this is
	// still include mode driven by `any` alone.
	if _, ok := got["author"]; ok {
		t.Errorf("author should not be projected; got %v", keys(got))
	}
}

func TestProjection_AbsentStaysAbsent(t *testing.T) {
	got := shape(t, recordShaper{proj: parseProj(t, `{"any":1,"missing":1}`)}, projTestRecord)
	if _, ok := got["missing"]; ok {
		t.Errorf("a projected field the record lacks must stay absent, not null-filled; got %v", keys(got))
	}
}

func TestProjection_Nested(t *testing.T) {
	got := shape(t, recordShaper{proj: parseProj(t, `{"any.name":1,"nav.pos":1}`)}, projTestRecord)
	anyGroup, _ := got["any"].(map[string]any)
	if anyGroup["name"] != "Doc" {
		t.Errorf("any.name missing: %v", anyGroup)
	}
	if _, ok := anyGroup["types"]; ok {
		t.Errorf("any.types was not projected: %v", anyGroup)
	}
	navGroup, _ := got["nav"].(map[string]any)
	if _, ok := navGroup["type"]; ok {
		t.Errorf("nav.type was not projected: %v", navGroup)
	}
}

// TestProjection_DeepestMarkWins pins both polarities of the rule: a
// deeper INCLUDE narrows an included ancestor (rather than the broader
// mark silently winning and over-returning), and a deeper EXCLUDE
// carves it.
func TestProjection_DeepestMarkWins(t *testing.T) {
	got := shape(t, recordShaper{proj: parseProj(t, `{"nav":1,"nav.pos":1}`)}, projTestRecord)
	navGroup, _ := got["nav"].(map[string]any)
	if navGroup["pos"] != "aa" {
		t.Errorf("nav.pos should be present: %v", navGroup)
	}
	if _, ok := navGroup["type"]; ok {
		t.Errorf("a deeper include must narrow the ancestor, not be subsumed by it: %v", navGroup)
	}
}

func TestProjection_NestedCarve(t *testing.T) {
	// Mongo rejects mixing include and exclude; this is the shape that
	// mixing buys — a whole subtree minus one leaf.
	got := shape(t, recordShaper{proj: parseProj(t, `{"nav":1,"nav.pos":-1}`)}, projTestRecord)
	navGroup, _ := got["nav"].(map[string]any)
	if _, ok := navGroup["pos"]; ok {
		t.Errorf("nav.pos was excluded: %v", navGroup)
	}
	if navGroup["type"] == nil {
		t.Errorf("nav.type should have survived: %v", navGroup)
	}
	// Exclusions carve _ver too, so a field you dropped does not leave
	// its version behind.
	ver, _ := got["_ver"].(map[string]any)
	navVer, _ := ver["nav"].(map[string]any)
	if _, ok := navVer["pos"]; ok {
		t.Errorf("_ver.nav.pos should be carved with the field: %v", navVer)
	}
	if navVer["type"] != "v1" {
		t.Errorf("_ver.nav.type should have survived: %v", navVer)
	}
}

// TestProjection_VerResolvesIdentically is the contract the narrowing
// rests on: for every path the projection includes, looking the version
// up in the narrowed map answers exactly what the full map answers.
// Covers the two shapes that make `_ver` non-trivial — a `*` default
// standing in for unenumerated siblings, and a subtree collapsed to a
// single version string.
func TestProjection_VerResolvesIdentically(t *testing.T) {
	const record = `{
      "id": "rec1",
      "_ver": {"*":"v2","id":"v1","any":{"*":"v5","name":"v7"},"nav":"v4","spaceId":"v9"},
      "any": {"types":["page"],"name":"Doc"},
      "nav": {"pos":"aa","type":1},
      "spaceId": "space1"
    }`
	full, err := anyenc.ParseJson(record)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := shape(t, recordShaper{proj: parseProj(t, `{"any":1,"nav":1}`)}, record)
	narrowed, err := json.Marshal(got["_ver"])
	if err != nil {
		t.Fatalf("marshal narrowed _ver: %v", err)
	}
	var narrowedVer any
	if err := json.Unmarshal(narrowed, &narrowedVer); err != nil {
		t.Fatalf("unmarshal narrowed _ver: %v", err)
	}
	var fullVer any
	if err := json.Unmarshal(full.Get("_ver").FastJson(&fastjson.Arena{}).MarshalTo(nil), &fullVer); err != nil {
		t.Fatalf("unmarshal full _ver: %v", err)
	}

	for _, path := range [][]string{
		{"id"},
		{"any"}, {"any", "name"}, {"any", "types"}, {"any", "unenumerated"},
		{"nav"}, {"nav", "pos"}, {"nav", "type"},
	} {
		want := getVersionRef(fullVer, path)
		gotVer := getVersionRef(narrowedVer, path)
		if want != gotVer {
			t.Errorf("_ver at %v: narrowed=%q full=%q", path, gotVer, want)
		}
	}
	// `nav` was collapsed to a bare string upstream; narrowing keeps it
	// that way rather than expanding it.
	if got, ok := narrowedVer.(map[string]any)["nav"].(string); !ok || got != "v4" {
		t.Errorf("_ver.nav should stay the collapsed string, got %#v",
			narrowedVer.(map[string]any)["nav"])
	}
}

// getVersionRef reimplements the SDK's crdt.getVersion walk (its
// package is internal) so the test asserts against the real lookup
// rule rather than against this server's idea of it: descend, and on a
// missing key fall back to the level's `*`.
func getVersionRef(node any, path []string) string {
	cur := node
	for _, key := range path {
		switch n := cur.(type) {
		case string:
			return n // collapsed ancestor covers everything below
		case map[string]any:
			if next, ok := n[key]; ok {
				cur = next
				continue
			}
			def, ok := n[verDefaultKey].(string)
			if !ok {
				return ""
			}
			return def
		default:
			return ""
		}
	}
	return maxVersionRef(cur)
}

func maxVersionRef(node any) string {
	switch n := node.(type) {
	case string:
		return n
	case map[string]any:
		best := ""
		for _, v := range n {
			if c := maxVersionRef(v); c > best {
				best = c
			}
		}
		return best
	}
	return ""
}

func TestProjection_BlocklistWinsOverProjection(t *testing.T) {
	const row = `{"id":"s1","name":"Space","guestKey":"SECRET","_ver":{"id":"v1","guestKey":"v1"}}`
	s := recordShaper{proj: parseProj(t, `{"name":1,"guestKey":1}`), strip: spaceListStrippedFields}
	got := shape(t, s, row)
	if _, ok := got["guestKey"]; ok {
		t.Errorf("a projection naming a withheld field must not surface it; got %v", keys(got))
	}
	if got["name"] != "Space" {
		t.Errorf("name should have been projected; got %v", keys(got))
	}
}

func TestProjection_Validation(t *testing.T) {
	for _, tc := range []struct{ name, body, wantMsg string }{
		{"id excluded", `{"id":-1}`, "id cannot be excluded"},
		{"empty path", `{"":1}`, "projection path is empty"},
		{"empty segment", `{"nav..pos":1}`, "empty segment"},
		{"reserved star", `{"nav.*":1}`, "reserved segment"},
		{"bad value", `{"nav":"yes"}`, "must be 1 (include) or -1 (exclude)"},
		{"bad value 2", `{"nav":2}`, "must be 1 (include) or -1 (exclude)"},
		{"not an object", `["nav"]`, "must be a JSON object"},
		{"too deep", `{"a.b.c.d.e.f.g.h.i":1}`, "nested too deeply"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newEchoCtx()
			_, _, done := parseProjection(c, mustBody(t, tc.body), false)
			if !done {
				t.Fatalf("projection %s should have been rejected", tc.body)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Errorf("message %q should mention %q", rec.Body.String(), tc.wantMsg)
			}
		})
	}
}

func TestProjection_Absent(t *testing.T) {
	c, _ := newEchoCtx()
	for _, body := range []string{`{}`, `{"limit":10}`, `{"projection":null}`} {
		var p fastjson.Parser
		v, err := p.Parse(body)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		proj, _, done := parseProjection(c, v, false)
		if done || proj != nil {
			t.Errorf("body %s should leave the wire untouched, got proj=%v done=%v", body, proj, done)
		}
	}
	// A nil root (empty body) is the full-snapshot path.
	if proj, _, done := parseProjection(c, nil, false); done || proj != nil {
		t.Errorf("nil body should leave the wire untouched")
	}
}

// --- ops -------------------------------------------------------------

func opsOf(t *testing.T, s recordShaper, ops []space.EventOp) []map[string]any {
	t.Helper()
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	var out []map[string]any
	for _, op := range ops {
		for _, shaped := range s.op(op, fa) {
			blob, err := json.Marshal(shaped)
			if err != nil {
				t.Fatalf("marshal op: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(blob, &m); err != nil {
				t.Fatalf("unmarshal op: %v", err)
			}
			out = append(out, m)
		}
	}
	return out
}

func anyencVal(t *testing.T, jsonStr string) *anyenc.Value {
	t.Helper()
	v, err := anyenc.ParseJson(jsonStr)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return v
}

func TestProjection_OpsFiltered(t *testing.T) {
	s := recordShaper{proj: parseProj(t, `{"any":1,"nav":1}`)}
	got := opsOf(t, s, []space.EventOp{
		{Type: "$set", Path: []string{"any", "name"}, Payload: anyencVal(t, `"New"`)},
		{Type: "$set", Path: []string{"spaceId"}, Payload: anyencVal(t, `"s2"`)},
		{Type: "$unset", Path: []string{"nav", "pos"}},
	})
	if len(got) != 2 {
		t.Fatalf("expected the spaceId op to be dropped, got %v", got)
	}
	if got[0]["path"].([]any)[0] != "any" || got[1]["type"] != "$unset" {
		t.Errorf("unexpected ops: %v", got)
	}
}

func TestProjection_OpNarrowedAtBoundary(t *testing.T) {
	// A $set that replaces `nav` wholesale, under a projection that only
	// holds nav.pos: the payload must be narrowed, not shipped whole.
	s := recordShaper{proj: parseProj(t, `{"nav.pos":1}`)}
	got := opsOf(t, s, []space.EventOp{
		{Type: "$set", Path: []string{"nav"}, Payload: anyencVal(t, `{"pos":"bb","type":2}`)},
	})
	if len(got) != 1 {
		t.Fatalf("op should survive narrowed, got %v", got)
	}
	payload, _ := got[0]["payload"].(map[string]any)
	if payload["pos"] != "bb" {
		t.Errorf("narrowed payload should keep pos: %v", payload)
	}
	if _, ok := payload["type"]; ok {
		t.Errorf("narrowed payload should drop type: %v", payload)
	}
}

func TestProjection_MultiFieldOpNarrowed(t *testing.T) {
	// The record-creation shape: empty path, payload keys are dotted
	// paths applied in parallel.
	s := recordShaper{proj: parseProj(t, `{"any":1}`)}
	got := opsOf(t, s, []space.EventOp{
		{Type: "$set", Payload: anyencVal(t, `{"any.name":"Doc","spaceId":"s1","nav.pos":"aa"}`)},
	})
	if len(got) != 1 {
		t.Fatalf("multi-field op should survive, got %v", got)
	}
	payload, _ := got[0]["payload"].(map[string]any)
	if _, ok := payload["any.name"]; !ok {
		t.Errorf("any.name should survive: %v", payload)
	}
	for _, gone := range []string{"spaceId", "nav.pos"} {
		if _, ok := payload[gone]; ok {
			t.Errorf("%q should have been dropped: %v", gone, payload)
		}
	}
}

func TestProjection_MultiFieldOpDroppedWhenEmpty(t *testing.T) {
	s := recordShaper{proj: parseProj(t, `{"any":1}`)}
	got := opsOf(t, s, []space.EventOp{
		{Type: "$set", Payload: anyencVal(t, `{"spaceId":"s1"}`)},
	})
	if len(got) != 0 {
		t.Errorf("a multi-field op with nothing projected says nothing and should be dropped: %v", got)
	}
}

// TestProjection_MultiFieldStripDotted: a multi-field payload's keys
// are PATHS, so the blocklist has to match on the first segment —
// "guestKey.x" is withheld along with "guestKey".
func TestProjection_MultiFieldStripDotted(t *testing.T) {
	s := recordShaper{strip: spaceListStrippedFields}
	got := opsOf(t, s, []space.EventOp{
		{Type: "$set", Payload: anyencVal(t, `{"guestKey.priv":"SECRET","name":"Space"}`)},
	})
	if len(got) != 1 {
		t.Fatalf("op should survive, got %v", got)
	}
	if strings.Contains(string(mustJSON(t, got[0])), "SECRET") {
		t.Errorf("withheld key material reached the wire: %s", mustJSON(t, got[0]))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return blob
}

func TestProjection_NoProjectionIsUnchanged(t *testing.T) {
	// The no-projection path must stay byte-identical to what it always
	// was — a projection is opt-in, including for the delivery counters.
	got := shape(t, recordShaper{}, projTestRecord)
	for _, want := range []string{"id", "_ver", "any", "nav", "spaceId", "author", "_addSeq", "_applySeq"} {
		if _, ok := got[want]; !ok {
			t.Errorf("without a projection %q must still ship; got %v", want, keys(got))
		}
	}
}
