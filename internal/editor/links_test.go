package editor

import (
	"testing"

	"github.com/anyproto/any/internal/index"
)

func TestBlockLinks(t *testing.T) {
	const sp, obj, coll = "spc1", "obj42", "editor_blocks"
	cases := []struct {
		name  string
		block Block
		want  map[string]string // target → kind
	}{
		{"inline link in a sentence", blk("b1", TypeParagraph, "See [Roadmap](any://o/spc1/obj9) for detail."),
			map[string]string{"any://o/spc1/obj9": index.LinkKindLink}},
		{"whole-line object link is a card", blk("b2", TypeParagraph, "[Roadmap](any://o/spc1/obj9)"),
			map[string]string{"any://o/spc1/obj9": index.LinkKindCard}},
		{"whole-line file link is a card", blk("b3", TypeParagraph, "  [report.pdf](any://f/spc1/file7)  "),
			map[string]string{"any://f/spc1/file7": index.LinkKindCard}},
		{"whole-line mention is not a card", blk("b4", TypeParagraph, "[Zarko](any://m/spc1/ident1)"),
			map[string]string{"any://m/spc1/ident1": index.LinkKindMention}},
		{"whole-line link in a list item is a link", blk("b5", TypeListItem, "[Roadmap](any://o/spc1/obj9)"),
			map[string]string{"any://o/spc1/obj9": index.LinkKindLink}},
		{"escaped label still a card", blk("b6", TypeParagraph, `[a \[b\] c](any://o/spc1/obj9)`),
			map[string]string{"any://o/spc1/obj9": index.LinkKindCard}},
		{"image line is a link", blk("b7", TypeParagraph, "![Diagram](any://f/spc1/file3)<!-- any:image-width=320 -->"),
			map[string]string{"any://f/spc1/file3": index.LinkKindLink}},
		{"synced-block reference is an embed", blk("b8", TypeHTML,
			`<!-- any:block {"v":1,"kind":"synced-block","schema":1,"instance":"01ARZ","data":{"ref":"any://o/spc1/obj9/editor_blocks/blk7","role":"reference"}} -->`),
			map[string]string{"any://o/spc1/obj9/editor_blocks/blk7": index.LinkKindEmbed}},
		{"synced-block source is nothing", blk("b9", TypeHTML,
			`<!-- any:block {"v":1,"kind":"synced-block","schema":1,"instance":"01ARZ","data":{"role":"source"}} -->`),
			map[string]string{}},
		{"external embed is nothing", blk("b10", TypeHTML,
			`<!-- any:block {"v":1,"kind":"embed","schema":1,"instance":"01ARZ","data":{"provider":"youtube","url":"https://youtu.be/x"}} -->`),
			map[string]string{}},
		{"self link dropped", blk("b11", TypeParagraph, "[me](any://o/spc1/obj42)"), map[string]string{}},
		{"legacy bare form is a link, not a card", blk("b13", TypeParagraph, "[Roadmap](any://obj9abcdef)"),
			map[string]string{"any://o/spc1/obj9abcdef": index.LinkKindLink}},
		{"legacy global form is a link, not a card", blk("b14", TypeParagraph, "[Roadmap](any://spacespace1/obj9abcdef)"),
			map[string]string{"any://o/spacespace1/obj9abcdef": index.LinkKindLink}},
		{"record reference is a link, not a card", blk("b15", TypeParagraph, "[blk](any://o/spc1/obj9/editor_blocks/blk7)"),
			map[string]string{"any://o/spc1/obj9/editor_blocks/blk7": index.LinkKindLink}},
		{"empty label is a link, not a card", blk("b16", TypeParagraph, "[](any://o/spc1/obj9)"),
			map[string]string{"any://o/spc1/obj9": index.LinkKindLink}},
		{"empty", blk("b12", TypeParagraph, ""), map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]string{}
			for _, e := range BlockLinks(sp, obj, coll, tc.block) {
				got[e.Target.String()] = e.Kind
				if e.RecordId != tc.block.Id || e.Dataset != coll || e.ObjectId != obj {
					t.Errorf("source place wrong: %+v", e)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("links = %v, want %v", got, tc.want)
			}
			for k, kind := range tc.want {
				if got[k] != kind {
					t.Errorf("%s: kind %q, want %q", k, got[k], kind)
				}
			}
		})
	}
}
