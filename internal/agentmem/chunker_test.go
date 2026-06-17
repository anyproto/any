package agentmem

import (
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
)

func TestMemoryData(t *testing.T) {
	arena := &anyenc.Arena{}
	strArr := func(vals ...string) *anyenc.Value {
		a := arena.NewArray()
		for i, v := range vals {
			a.SetArrayItem(i, arena.NewString(v))
		}
		return a
	}

	rec := arena.NewObject()
	rec.Set(FieldContext, arena.NewString("user prefers dark mode"))
	rec.Set(FieldBody, arena.NewString("set in the editor settings"))
	rec.Set(FieldCategory, arena.NewString("preference"))
	rec.Set(FieldKeywords, strArr("dark", "mode"))
	rec.Set(FieldEntities, strArr("editor"))
	rec.Set(FieldTags, strArr("ui"))
	// Numeric/structural fields must NOT appear in the indexed text.
	rec.Set(FieldAccessCount, arena.NewNumberInt(7))
	rec.Set(FieldConfidence, arena.NewNumberInt(90))

	got := memoryData(rec)
	want := "user prefers dark mode\nset in the editor settings\npreference\ndark mode\neditor\nui"
	if got != want {
		t.Fatalf("memoryData =\n%q\nwant\n%q", got, want)
	}

	// A bump to a numeric field leaves the indexed text (and thus its
	// hash) unchanged — the basis for skipping re-embedding.
	rec.Set(FieldAccessCount, arena.NewNumberInt(8))
	if memoryData(rec) != want {
		t.Errorf("accessCount bump changed indexed text: %q", memoryData(rec))
	}

	// Minimal item: context only.
	min := arena.NewObject()
	min.Set(FieldContext, arena.NewString("just a context"))
	if got := memoryData(min); got != "just a context" {
		t.Errorf("minimal memoryData = %q", got)
	}
}
