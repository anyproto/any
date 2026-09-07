package index

import (
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
)

func targets(entries []LinkEntry) map[string]string {
	out := map[string]string{}
	for _, e := range entries {
		out[e.Target.String()] = e.Kind
	}
	return out
}

func TestLinkMode(t *testing.T) {
	cases := []struct {
		name string
		xf   map[string]any
		want string
	}{
		{"nil", nil, ""},
		{"plain text", map[string]any{"type": "text"}, ""},
		{"relation implies links", map[string]any{"type": "relation"}, LinkModeMany},
		{"markdown implies markdown", map[string]any{"type": "markdown"}, LinkModeMarkdown},
		{"explicit one", map[string]any{"type": "text", "links": "link"}, LinkModeOne},
		{"explicit overrides slug", map[string]any{"type": "relation", "links": "markdown"}, LinkModeMarkdown},
		{"bogus explicit disables", map[string]any{"type": "relation", "links": "nope"}, ""},
		{"non-string explicit disables", map[string]any{"type": "relation", "links": 1}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LinkMode(tc.xf); got != tc.want {
				t.Errorf("LinkMode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTextLinks(t *testing.T) {
	const sp, obj = "space1", "obj1abc"
	text := "see [spec](any://o/space1/obj2abc) and [blk](any://o/space1/obj2abc/editor_blocks/blk9), " +
		"ping [Z](any://m/space1/ident1), file ![x](any://f/space1/file1x?variant=thumb), " +
		"self any://obj1abc, own block any://o/space1/obj1abc/editor_blocks/blk1, " +
		"space any://s/space1, bare any://obj3abc, dup any://o/space1/obj2abc"
	got := targets(TextLinks(sp, obj, "editor_blocks", "b1", text))
	want := map[string]string{
		"any://o/space1/obj2abc":                    LinkKindLink,
		"any://o/space1/obj2abc/editor_blocks/blk9": LinkKindLink,
		"any://m/space1/ident1":                     LinkKindMention,
		"any://f/space1/file1x":                     LinkKindLink,
		"any://o/space1/obj1abc/editor_blocks/blk1": LinkKindLink, // a link to one's own block is a real link
		"any://o/space1/obj3abc":                    LinkKindLink, // bare form resolved to the source space
	}
	if len(got) != len(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	for k, kind := range want {
		if got[k] != kind {
			t.Errorf("%s: kind %q, want %q", k, got[k], kind)
		}
	}
	entries := TextLinks(sp, obj, "editor_blocks", "b1", text)
	for _, e := range entries {
		if e.ObjectId != obj || e.Dataset != "editor_blocks" || e.RecordId != "b1" {
			t.Errorf("source place wrong on %+v", e)
		}
	}
}

func TestValueLinks(t *testing.T) {
	const sp, obj = "space1", "obj1abc"
	a := &anyenc.Arena{}

	arr := a.NewArray()
	arr.SetArrayItem(0, a.NewString("any://obj2abc"))
	arr.SetArrayItem(1, a.NewString("any://obj1abc")) // self
	arr.SetArrayItem(2, a.NewString("not a uri"))
	arr.SetArrayItem(3, a.NewString("any://obj2abc")) // dup
	arr.SetArrayItem(4, a.NewNumberInt(7))
	got := targets(ValueLinks(sp, obj, DatasetProp, "p1", LinkModeMany, arr))
	if len(got) != 1 || got["any://o/space1/obj2abc"] != LinkKindRelation {
		t.Errorf("many: %v", got)
	}

	one := ValueLinks(sp, obj, DatasetProp, "p2", LinkModeOne, a.NewString("any://o/space2/obj9abc"))
	if len(one) != 1 || one[0].Target.String() != "any://o/space2/obj9abc" || one[0].Kind != LinkKindRelation {
		t.Errorf("one: %+v", one)
	}
	if got := ValueLinks(sp, obj, DatasetProp, "p2", LinkModeOne, arr); got != nil {
		t.Errorf("one on an array: %+v", got)
	}

	md := ValueLinks(sp, obj, "notes", "r1", LinkModeMarkdown, a.NewString("cc [Z](any://m/space1/ident1)"))
	if len(md) != 1 || md[0].Kind != LinkKindMention {
		t.Errorf("markdown: %+v", md)
	}
	if got := ValueLinks(sp, obj, "notes", "r1", "", a.NewString("any://obj2abc")); got != nil {
		t.Errorf("no mode: %+v", got)
	}
	if got := ValueLinks(sp, obj, "notes", "r1", LinkModeMany, nil); got != nil {
		t.Errorf("nil value: %+v", got)
	}
}
