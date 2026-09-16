package index

import (
	"testing"
	"time"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"
)

func TestRenderPropValue(t *testing.T) {
	arena := &anyenc.Arena{}

	if got := renderPropValue(arena.NewString("plain text"), space.PropertyKindString); got != "plain text" {
		t.Errorf("string render = %q", got)
	}
	if got := renderPropValue(nil, space.PropertyKindString); got != "" {
		t.Errorf("absent render = %q", got)
	}
	// Array: string and number elements newline-joined, others skipped.
	arr := arena.NewArray()
	arr.SetArrayItem(0, arena.NewString("alpha"))
	arr.SetArrayItem(1, arena.NewNumberInt(7))
	arr.SetArrayItem(2, arena.NewString("bravo"))
	arr.SetArrayItem(3, arena.NewTrue())
	if got := renderPropValue(arr, space.PropertyKindArray); got != "alpha\n7\nbravo" {
		t.Errorf("array render = %q", got)
	}
	// Numbers: canonical JSON — integers without a decimal point,
	// floats shortest round-trip.
	if got := renderPropValue(arena.NewNumberInt(85600), space.PropertyKindNumber); got != "85600" {
		t.Errorf("int render = %q", got)
	}
	if got := renderPropValue(arena.NewNumberFloat64(9.5), space.PropertyKindNumber); got != "9.5" {
		t.Errorf("float render = %q", got)
	}
	// Kind mismatch (a number where a string is declared) renders empty.
	if got := renderPropValue(arena.NewNumberInt(5), space.PropertyKindString); got != "" {
		t.Errorf("mismatched kind render = %q", got)
	}
}

func TestResolveIndexedProp(t *testing.T) {
	str := func(meta map[string]string) space.PropertyDef {
		return space.PropertyDef{Id: "p1", Name: "Author", Kind: space.PropertyKindString, Meta: meta}
	}

	// Absent meta.index → default scope props.
	p, ok := resolveIndexedProp("t1", str(nil))
	if !ok || p.scope != ScopeProps || p.name != "Author" || p.propId != "p1" || p.typeId != "t1" {
		t.Fatalf("default-on: ok=%v row=%+v", ok, p)
	}
	// Explicit scope override.
	p, ok = resolveIndexedProp("t1", str(map[string]string{MetaIndexKey: "agent"}))
	if !ok || p.scope != "agent" {
		t.Fatalf("override: ok=%v scope=%q", ok, p.scope)
	}
	// "none" opts out.
	if _, ok = resolveIndexedProp("t1", str(map[string]string{MetaIndexKey: MetaIndexNone})); ok {
		t.Fatal("none should exclude")
	}
	// Invalid scope slug: excluded, not silently defaulted.
	if _, ok = resolveIndexedProp("t1", str(map[string]string{MetaIndexKey: "Bad Scope"})); ok {
		t.Fatal("invalid scope should exclude")
	}
	// Numbers index; booleans don't.
	if _, ok = resolveIndexedProp("t1", space.PropertyDef{Id: "p2", Name: "Score", Kind: space.PropertyKindNumber}); !ok {
		t.Fatal("number kind should index")
	}
	if _, ok = resolveIndexedProp("t1", space.PropertyDef{Id: "p3", Name: "Done", Kind: space.PropertyKindBoolean}); ok {
		t.Fatal("boolean kind should not index")
	}
	// Name falls back to XKey when the display label is empty.
	p, ok = resolveIndexedProp("t1", space.PropertyDef{Id: "p4", XKey: "book.author", Kind: space.PropertyKindString})
	if !ok || p.name != "book.author" {
		t.Fatalf("xKey fallback: ok=%v name=%q", ok, p.name)
	}
}

func TestValidScope(t *testing.T) {
	for _, ok := range []string{"basic", "chat", "agent", "my-scope_2"} {
		if !ValidScope(ok) {
			t.Errorf("ValidScope(%q) = false, want true", ok)
		}
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	for _, bad := range []string{"", "Bad", "has space", "семантика", string(long)} {
		if ValidScope(bad) {
			t.Errorf("ValidScope(%q) = true, want false", bad)
		}
	}
}

func TestPropChunker_AlwaysExcludesMetaType(t *testing.T) {
	// Type-definition rows are schema, not knowledge — excluded even
	// when no caller-supplied exclusions are wired in.
	if !NewPropChunker().excludeTypes[MetaTypeLabel] {
		t.Error("NewPropChunker() must exclude MetaTypeLabel")
	}
	c := NewPropChunker("customType")
	if !c.excludeTypes[MetaTypeLabel] || !c.excludeTypes["customType"] {
		t.Errorf("exclusions = %v, want MetaTypeLabel + customType", c.excludeTypes)
	}
}

func TestPropChunker_CatalogTTLAndInvalidate(t *testing.T) {
	c := NewPropChunker()
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }

	// Install a snapshot by hand and verify TTL semantics without an SDK.
	c.cache["sp"] = &propCatalog{resolvedAt: now, props: []indexedProp{{typeId: "t", propId: "p", scope: "agent", kind: space.PropertyKindString}}}

	fresh := c.cache["sp"]
	if got := c.now().Sub(fresh.resolvedAt); got >= c.ttl {
		t.Fatalf("snapshot should be fresh, age %v", got)
	}
	now = now.Add(c.ttl + time.Second)
	if got := c.now().Sub(fresh.resolvedAt); got < c.ttl {
		t.Fatalf("snapshot should have expired, age %v", got)
	}

	c.Invalidate("sp")
	if c.cache["sp"] != nil {
		t.Fatal("Invalidate should drop the snapshot")
	}
}

// A date property is searchable text, not an opaque envelope: the value
// indexes as RFC 3339, so "2026-08" matches the month. It also has to
// reach the catalog at all — under the previous string default these
// properties WERE indexed, so excluding the new kind would be a silent
// search regression.
func TestPropDatetimeIsIndexed(t *testing.T) {
	arena := &anyenc.Arena{}
	ts := time.Date(2026, 8, 5, 17, 0, 0, 0, time.UTC)

	if got := renderPropValue(arena.NewDateTime(ts), space.PropertyKindDatetime); got != "2026-08-05T17:00:00Z" {
		t.Errorf("datetime render = %q", got)
	}
	// A stale numeric value (a row the SDK's re-index hasn't reached)
	// renders empty rather than as a bogus date.
	if got := renderPropValue(arena.NewNumberInt(1786014230), space.PropertyKindDatetime); got != "" {
		t.Errorf("numeric under a datetime kind render = %q", got)
	}

	def := space.PropertyDef{
		Id: "p1", Name: "Due", Kind: space.PropertyKindDatetime,
		XFormat: map[string]any{"type": "datetime"},
	}
	got, ok := resolveIndexedProp("t1", def)
	if !ok {
		t.Fatal("a datetime property must enter the index catalog")
	}
	if got.scope != ScopeProps || got.kind != space.PropertyKindDatetime {
		t.Errorf("indexed prop = %+v", got)
	}
}

// The schema chunker's renderer sees actual anyenc types (x-search may
// map undeclared fields), and stamped createTime/modifyTime fields are
// instants.
func TestRenderSearchValueDatetime(t *testing.T) {
	arena := &anyenc.Arena{}
	ts := time.Date(2026, 8, 5, 17, 0, 0, 0, time.UTC)

	if got := renderSearchValue(arena.NewDateTime(ts)); got != "2026-08-05T17:00:00Z" {
		t.Errorf("datetime render = %q", got)
	}
	arr := arena.NewArray()
	arr.SetArrayItem(0, arena.NewString("alpha"))
	arr.SetArrayItem(1, arena.NewDateTime(ts))
	if got := renderSearchValue(arr); got != "alpha\n2026-08-05T17:00:00Z" {
		t.Errorf("array render = %q", got)
	}
}

// TestMembers pins the membership set the chunkers gate on: the one
// type, the collections, and — a definition implements itself — the
// row's own id when it carries a marker.
func TestMembers(t *testing.T) {
	row := func(js string) *anyenc.Value {
		v, err := anyenc.ParseJson(js)
		if err != nil {
			t.Fatalf("parse %s: %v", js, err)
		}
		return v
	}
	cases := map[string]struct {
		json string
		want []string
	}{
		"type only": {`{"id":"o1","any":{"type":"page"}}`, []string{"page"}},
		"type + collections": {`{"id":"o1","any":{"type":"page","collections":["wiki","bin"]}}`,
			[]string{"page", "wiki", "bin"}},
		"collections only": {`{"id":"o1","any":{"collections":["miniapp"]}}`, []string{"miniapp"}},
		"type definition":  {`{"id":"t1","any":{"type":"__type__"}}`, []string{"__type__", "t1"}},
		"collection def":   {`{"id":"c1","any":{"type":"__collection__"}}`, []string{"__collection__", "c1"}},
		"bare object":      {`{"id":"o1","any":{"name":"x"}}`, nil},
	}
	for name, tc := range cases {
		got := Members(row(tc.json))
		if len(got) != len(tc.want) {
			t.Errorf("%s: Members = %v, want %v", name, got, tc.want)
			continue
		}
		for _, w := range tc.want {
			if !got[w] {
				t.Errorf("%s: Members = %v, missing %q", name, got, w)
			}
		}
	}
	if Members(nil) != nil {
		t.Error("Members(nil) must be nil")
	}
}
