package index

import (
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
)

const memTypeId = "type123"

// memRecord builds an objects-row anyenc record with the given types,
// name, and property values keyed under memTypeId.
func memRecord(arena *anyenc.Arena, types []string, name string, propVals map[string]string) *anyenc.Value {
	rec := arena.NewObject()
	rec.Set("id", arena.NewString("obj1"))

	anyNs := arena.NewObject()
	if name != "" {
		anyNs.Set("name", arena.NewString(name))
	}
	if len(types) > 0 {
		arr := arena.NewArray()
		for i, ty := range types {
			arr.SetArrayItem(i, arena.NewString(ty))
		}
		anyNs.Set("types", arr)
	}
	rec.Set("any", anyNs)

	if len(propVals) > 0 {
		tns := arena.NewObject()
		for propId, v := range propVals {
			tns.Set(propId, arena.NewString(v))
		}
		rec.Set(memTypeId, tns)
	}
	return rec
}

func TestHasType(t *testing.T) {
	arena := &anyenc.Arena{}
	with := memRecord(arena, []string{"other", memTypeId}, "n", nil)
	if !hasType(with, memTypeId) {
		t.Errorf("hasType missed present type")
	}
	without := memRecord(arena, []string{"other"}, "n", nil)
	if hasType(without, memTypeId) {
		t.Errorf("hasType matched absent type")
	}
	if hasType(nil, memTypeId) {
		t.Errorf("hasType matched nil record")
	}
}

func TestMemoryData(t *testing.T) {
	props := map[string]string{
		"context":  "pCtx",
		"keywords": "pKw",
		"entities": "pEnt",
	}

	cases := []struct {
		name     string
		recName  string
		propVals map[string]string
		want     string
	}{
		{
			name:    "full join order: name, context, keywords, entities",
			recName: "Memory",
			propVals: map[string]string{
				"pCtx": "ctx text",
				"pKw":  "k1 k2",
				"pEnt": "Alice",
			},
			want: "Memory\nctx text\nk1 k2\nAlice",
		},
		{
			name:    "skip empties: keywords empty is omitted",
			recName: "Memory",
			propVals: map[string]string{
				"pCtx": "ctx",
				"pEnt": "Bob",
			},
			want: "Memory\nctx\nBob",
		},
		{
			name:     "name only",
			recName:  "JustName",
			propVals: nil,
			want:     "JustName",
		},
		{
			name:    "no name, props only",
			recName: "",
			propVals: map[string]string{
				"pCtx": "ctx",
			},
			want: "ctx",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arena := &anyenc.Arena{}
			rec := memRecord(arena, []string{memTypeId}, tc.recName, tc.propVals)
			got := memoryData(rec, memTypeId, props)
			if got != tc.want {
				t.Errorf("memoryData = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMemoryData_MissingPropDefinition(t *testing.T) {
	// props map resolved only "context" — keywords/entities undefined.
	props := map[string]string{"context": "pCtx"}
	arena := &anyenc.Arena{}
	rec := memRecord(arena, []string{memTypeId}, "Name", map[string]string{
		"pCtx": "ctx",
		"pKw":  "ignored — not in props map",
	})
	got := memoryData(rec, memTypeId, props)
	if got != "Name\nctx" {
		t.Errorf("memoryData = %q, want %q", got, "Name\nctx")
	}
}
