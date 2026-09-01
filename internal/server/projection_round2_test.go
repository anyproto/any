package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"
)

// TestProjection_MultiFieldUnsetKeysOnly: the multi-field $unset form
// carries placeholder VALUES the CRDT ignores (the SDK's crdt.Op doc),
// so only its keys may be classified. Narrowing a placeholder would
// silently drop the removal and leave a mirror holding a stale field.
func TestProjection_MultiFieldUnsetKeysOnly(t *testing.T) {
	s := recordShaper{proj: parseProj(t, `{"nav.pos":1,"any":1}`)}
	got := opsOf(t, s, []space.EventOp{
		{Type: "$unset", Payload: anyencVal(t, `{"nav":true,"any.name":true,"spaceId":true}`)},
	})
	if len(got) != 1 {
		t.Fatalf("the unset should survive: %v", got)
	}
	payload, _ := got[0]["payload"].(map[string]any)
	for _, want := range []string{"nav", "any.name"} {
		if _, ok := payload[want]; !ok {
			t.Errorf("%q must stay in the unset list: %v", want, payload)
		}
	}
	if _, ok := payload["spaceId"]; ok {
		t.Errorf("an unprojected key should still be dropped: %v", payload)
	}
}

// TestProjection_MultiFieldSetSplitsVanished: a $set key whose new
// value narrows to nothing means the field is gone for this client,
// which no $set can say — it moves to a companion $unset.
func TestProjection_MultiFieldSetSplitsVanished(t *testing.T) {
	s := recordShaper{proj: parseProj(t, `{"nav.pos":1,"any":1}`)}
	got := opsOf(t, s, []space.EventOp{
		{Type: "$set", Payload: anyencVal(t, `{"nav":{"type":2},"any.name":"X"}`)},
	})
	if len(got) != 2 {
		t.Fatalf("expected a $set and a companion $unset, got %v", got)
	}
	set, unset := got[0], got[1]
	if set["type"] != "$set" || unset["type"] != "$unset" {
		t.Fatalf("wrong op types: %v", got)
	}
	setPayload, _ := set["payload"].(map[string]any)
	if setPayload["any.name"] != "X" {
		t.Errorf("$set lost the surviving key: %v", setPayload)
	}
	if _, ok := setPayload["nav"]; ok {
		t.Errorf("$set should not carry the vanished key: %v", setPayload)
	}
	unsetPayload, _ := unset["payload"].(map[string]any)
	if _, ok := unsetPayload["nav"]; !ok {
		t.Errorf("$unset should name the vanished key: %v", unsetPayload)
	}
}

// TestProjection_ArrayKeepsSlots: an element holding none of the
// projected paths comes back as {} rather than vanishing, so a
// projection never changes an array's length or shifts its indices.
func TestProjection_ArrayKeepsSlots(t *testing.T) {
	const record = `{
      "id":"r1","_ver":{"id":"v1"},
      "tags":[{"name":"a"},{"other":"b"},{"name":"c"}]
    }`
	got := shape(t, recordShaper{proj: parseProj(t, `{"tags.name":1}`)}, record)
	items, _ := got["tags"].([]any)
	if len(items) != 3 {
		t.Fatalf("array length must survive the projection: %#v", got["tags"])
	}
	mid, _ := items[1].(map[string]any)
	if len(mid) != 0 {
		t.Errorf("an element with nothing projected should be {}: %v", mid)
	}
	last, _ := items[2].(map[string]any)
	if last["name"] != "c" {
		t.Errorf("indices shifted: %v", items)
	}
}

// TestProjection_VerFollowsDeepestInclude: a deeper include narrows the
// record body, so it must narrow the version map too — otherwise _ver
// carries an entry for a field that is not on the wire.
func TestProjection_VerFollowsDeepestInclude(t *testing.T) {
	got := shape(t, recordShaper{proj: parseProj(t, `{"nav":1,"nav.pos":1}`)}, projTestRecord)
	navGroup, _ := got["nav"].(map[string]any)
	if _, ok := navGroup["type"]; ok {
		t.Fatalf("doc should be narrowed to nav.pos: %v", navGroup)
	}
	ver, _ := got["_ver"].(map[string]any)
	navVer, _ := ver["nav"].(map[string]any)
	if _, ok := navVer["type"]; ok {
		t.Errorf("_ver should narrow with the body: %v", navVer)
	}
	if navVer["pos"] != "v1" {
		t.Errorf("_ver.nav.pos should have survived: %v", navVer)
	}
}

// TestProjection_VerIdIsRootOnly: `id` rides along because it is the
// record's creation marker. A nested object's own `id` is an ordinary
// field and follows the projection like any other.
func TestProjection_VerIdIsRootOnly(t *testing.T) {
	const record = `{
      "id":"r1",
      "_ver":{"id":"v1","grp":{"id":"vX","name":"v2"}},
      "grp":{"id":"inner","name":"N"}
    }`
	got := shape(t, recordShaper{proj: parseProj(t, `{"grp.name":1}`)}, record)
	grp, _ := got["grp"].(map[string]any)
	if _, ok := grp["id"]; ok {
		t.Fatalf("a nested id is not projected: %v", grp)
	}
	ver, _ := got["_ver"].(map[string]any)
	if ver["id"] != "v1" {
		t.Errorf("the record's own _ver.id must ride along: %v", ver)
	}
	grpVer, _ := ver["grp"].(map[string]any)
	if _, ok := grpVer["id"]; ok {
		t.Errorf("a nested id's version should not leak: %v", grpVer)
	}
}

// TestProjection_IdSubpathExclusionRejected: `id` ships whole and
// unconditionally, so an exclusion anywhere under it would be accepted
// and then do nothing.
func TestProjection_IdSubpathExclusionRejected(t *testing.T) {
	c, rec := newEchoCtx()
	_, _, done := parseProjection(c, mustBody(t, `{"nav":1,"id.x":-1}`), false)
	if !done || rec.Code != http.StatusBadRequest {
		t.Fatalf("id.x exclusion should be rejected, got done=%v code=%d", done, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "id cannot be excluded") {
		t.Errorf("unexpected message: %s", rec.Body.String())
	}
}

// TestProjection_NoShapingLeavesOpsAlone: without a projection or a
// blocklist nothing is inspected, so even a degenerate empty
// multi-field payload reaches the wire exactly as it always did.
func TestProjection_NoShapingLeavesOpsAlone(t *testing.T) {
	got := opsOf(t, recordShaper{}, []space.EventOp{
		{Type: "$set", Payload: anyencVal(t, `{}`)},
	})
	if len(got) != 1 {
		t.Fatalf("the op must survive untouched: %v", got)
	}
	payload, ok := got[0]["payload"].(map[string]any)
	if !ok || len(payload) != 0 {
		t.Errorf("payload should still be the empty object: %v", got[0])
	}
}

// TestProjection_OpWalkersAgree drives both walkers over the same
// table. The deepest-mark-wins rule lives in two implementations — one
// over []string paths, one over dotted []byte keys, so a multi-field
// payload is classified without splitting — and this is what makes a
// one-sided edit fail.
func TestProjection_OpWalkersAgree(t *testing.T) {
	projections := []string{
		`{"any":1,"nav":1}`,
		`{"_ver":-1}`,
		`{"nav":1,"nav.pos":-1}`,
		`{"nav":1,"nav.pos":1}`,
		`{"any":-1,"any.name":1}`,
		`{"any.name":1}`,
		`{"spaceId":-1,"author":-1}`,
	}
	paths := [][]string{
		{"any"}, {"any", "name"}, {"any", "types"},
		{"nav"}, {"nav", "pos"}, {"nav", "type"}, {"nav", "pos", "deep"},
		{"spaceId"}, {"author"}, {"missing"}, {"missing", "deeper"},
	}
	for _, body := range projections {
		p := parseProj(t, body)
		for _, path := range paths {
			wantVerdict, wantNode := p.opVerdict(path)
			gotVerdict, gotNode := p.opVerdictBytes([]byte(strings.Join(path, ".")))
			if wantVerdict != gotVerdict || wantNode != gotNode {
				t.Errorf("%s @ %v: []string walk = (%d,%p), dotted walk = (%d,%p)",
					body, path, wantVerdict, wantNode, gotVerdict, gotNode)
			}
		}
	}
}

// TestProjection_LocalFreeformKeepsUnderscoreFields: a local record is
// a plain any-store document, so an `_`-prefixed name there is the
// caller's own field and the protocol-field defaults must not touch it.
func TestProjection_LocalFreeformKeepsUnderscoreFields(t *testing.T) {
	const record = `{"id":"x","_addSeq":42,"title":"t","body":"b"}`
	got := shape(t, recordShaper{proj: parseProjFreeform(t, `{"body":-1}`)}, record)
	if got["_addSeq"] == nil {
		t.Errorf("a caller's own _addSeq must survive on the local store: %v", keys(got))
	}
	if _, ok := got["body"]; ok {
		t.Errorf("body should be excluded: %v", keys(got))
	}
	// Include mode reaches them by name like any other field.
	got = shape(t, recordShaper{proj: parseProjFreeform(t, `{"_addSeq":1}`)}, record)
	if got["_addSeq"] == nil {
		t.Errorf("_addSeq should be selectable: %v", keys(got))
	}
	if _, ok := got["title"]; ok {
		t.Errorf("title was not projected: %v", keys(got))
	}
}
