package server

import (
	"net/http"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"
)

// TestProjection_NonIntegerMarks pins the mark vocabulary against
// fastjson's best-effort integer parse: `1.0` (what a Python client's
// json.dumps writes for a float 1) and `1e0` must include, not silently
// invert to exclude.
func TestProjection_NonIntegerMarks(t *testing.T) {
	for _, body := range []string{`{"any":1.0}`, `{"any":1e0}`, `{"any":true}`} {
		got := shape(t, recordShaper{proj: parseProj(t, body)}, projTestRecord)
		if _, ok := got["any"]; !ok {
			t.Errorf("%s should INCLUDE any; got %v", body, keys(got))
		}
		if _, ok := got["spaceId"]; ok {
			t.Errorf("%s inverted into exclude mode; got %v", body, keys(got))
		}
	}
	for _, body := range []string{`{"any":-1.0}`, `{"any":0.0}`, `{"any":false}`} {
		got := shape(t, recordShaper{proj: parseProj(t, body)}, projTestRecord)
		if _, ok := got["any"]; ok {
			t.Errorf("%s should EXCLUDE any; got %v", body, keys(got))
		}
	}
	// A fractional value is still not a mark.
	c, rec := newEchoCtx()
	if _, _, done := parseProjection(c, mustBody(t, `{"any":0.5}`)); !done || rec.Code != http.StatusBadRequest {
		t.Errorf("0.5 should be rejected, got done=%v code=%d", done, rec.Code)
	}
}

// TestProjection_OpsFollowDeepestMark is the op-side half of
// deepest-mark-wins: with an ancestor excluded and a descendant
// included, the doc ships the descendant, so the ops must too.
func TestProjection_OpsFollowDeepestMark(t *testing.T) {
	s := recordShaper{proj: parseProj(t, `{"any":-1,"any.name":1}`)}

	got := shape(t, s, projTestRecord)
	anyGroup, _ := got["any"].(map[string]any)
	if anyGroup["name"] != "Doc" {
		t.Fatalf("doc should carry any.name: %v", got)
	}

	ops := opsOf(t, s, []space.EventOp{
		{Type: "$set", Path: []string{"any", "name"}, Payload: anyencVal(t, `"New"`)},
		{Type: "$set", Path: []string{"any", "types"}, Payload: anyencVal(t, `["page"]`)},
	})
	if len(ops) != 1 {
		t.Fatalf("expected exactly the any.name op, got %v", ops)
	}
	path := ops[0]["path"].([]any)
	if path[0] != "any" || path[1] != "name" {
		t.Errorf("wrong op survived: %v", ops[0])
	}
}

// TestProjection_EmptyNarrowBecomesUnset: a $set replacing a subtree
// with a value holding none of the projected leaves leaves the field
// absent from the doc, so the op must say $unset — a $set of {} would
// leave a mirror holding an empty object the doc denies.
func TestProjection_EmptyNarrowBecomesUnset(t *testing.T) {
	s := recordShaper{proj: parseProj(t, `{"nav.pos":1}`)}
	ops := opsOf(t, s, []space.EventOp{
		{Type: "$set", Path: []string{"nav"}, Payload: anyencVal(t, `{"type":2}`)},
	})
	if len(ops) != 1 {
		t.Fatalf("op should survive, got %v", ops)
	}
	if ops[0]["type"] != "$unset" {
		t.Errorf("op should have become $unset, got %v", ops[0])
	}
	if _, ok := ops[0]["payload"]; ok {
		t.Errorf("$unset should carry no payload: %v", ops[0])
	}
}

// TestProjection_Arrays: a projection into an array of objects narrows
// each element, mongo-style, instead of silently yielding nothing.
func TestProjection_Arrays(t *testing.T) {
	const record = `{
      "id": "r1",
      "_ver": {"id":"v1"},
      "tags": [{"name":"a","color":"red"},{"name":"b","color":"blue"}]
    }`
	got := shape(t, recordShaper{proj: parseProj(t, `{"tags.name":1}`)}, record)
	items, ok := got["tags"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("tags should be a 2-element array: %#v", got["tags"])
	}
	for i, item := range items {
		m, _ := item.(map[string]any)
		if m["name"] == nil {
			t.Errorf("element %d lost name: %v", i, m)
		}
		if _, leaked := m["color"]; leaked {
			t.Errorf("element %d kept an unprojected field: %v", i, m)
		}
	}
}

func TestProjection_ArrayCarve(t *testing.T) {
	const record = `{
      "id": "r1",
      "_ver": {"id":"v1"},
      "tags": [{"name":"a","color":"red"}]
    }`
	got := shape(t, recordShaper{proj: parseProj(t, `{"tags":1,"tags.color":-1}`)}, record)
	items, _ := got["tags"].([]any)
	if len(items) != 1 {
		t.Fatalf("tags should survive: %#v", got["tags"])
	}
	m, _ := items[0].(map[string]any)
	if _, leaked := m["color"]; leaked {
		t.Errorf("exclusion did not reach inside the array: %v", m)
	}
	if m["name"] != "a" {
		t.Errorf("name should have survived: %v", m)
	}
}

// TestProjection_TracesRideAlong: `_traces` is how an optimistic client
// recognises its own write echo, so an include-mode projection must not
// drop it silently.
func TestProjection_TracesRideAlong(t *testing.T) {
	const record = `{"id":"r1","_ver":{"id":"v1"},"any":{"name":"Doc"},"_traces":["t1"],"_addSeq":3}`
	got := shape(t, recordShaper{proj: parseProj(t, `{"any":1}`)}, record)
	if _, ok := got["_traces"]; !ok {
		t.Errorf("_traces should ride along: %v", keys(got))
	}
	if _, ok := got["_addSeq"]; ok {
		t.Errorf("_addSeq should still drop: %v", keys(got))
	}
	// ...and it is still droppable by name.
	got = shape(t, recordShaper{proj: parseProj(t, `{"any":1,"_traces":-1}`)}, record)
	if _, ok := got["_traces"]; ok {
		t.Errorf("_traces:-1 should drop it: %v", keys(got))
	}
}

// TestProjection_ExcludeCarvesVer: dropping a field drops its version
// with it, which is what docs/09-query.md promises for both modes.
func TestProjection_ExcludeCarvesVer(t *testing.T) {
	got := shape(t, recordShaper{proj: parseProj(t, `{"spaceId":-1}`)}, projTestRecord)
	if _, ok := got["spaceId"]; ok {
		t.Fatalf("spaceId should be excluded: %v", keys(got))
	}
	ver, _ := got["_ver"].(map[string]any)
	if _, ok := ver["spaceId"]; ok {
		t.Errorf("_ver.spaceId should be carved with the field: %v", keys(ver))
	}
	if _, ok := ver["any"]; !ok {
		t.Errorf("the rest of _ver should survive: %v", keys(ver))
	}
}

// TestProjection_EmptyObjectIsAbsent: a client that always emits the
// key must get the same bytes as one that omits it.
func TestProjection_EmptyObjectIsAbsent(t *testing.T) {
	c, _ := newEchoCtx()
	proj, _, done := parseProjection(c, mustBody(t, `{}`))
	if done || proj != nil {
		t.Fatalf(`{"projection":{}} should read as absent, got proj=%v done=%v`, proj, done)
	}
}
