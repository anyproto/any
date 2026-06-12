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
	// Array: string elements newline-joined, non-strings skipped.
	arr := arena.NewArray()
	arr.SetArrayItem(0, arena.NewString("alpha"))
	arr.SetArrayItem(1, arena.NewNumberInt(7))
	arr.SetArrayItem(2, arena.NewString("bravo"))
	if got := renderPropValue(arr, space.PropertyKindArray); got != "alpha\nbravo" {
		t.Errorf("array render = %q", got)
	}
	// Kind mismatch (a number where a string is declared) renders empty.
	if got := renderPropValue(arena.NewNumberInt(5), space.PropertyKindString); got != "" {
		t.Errorf("mismatched kind render = %q", got)
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
