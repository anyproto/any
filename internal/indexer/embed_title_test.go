package indexer

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/index"
)

func TestDocPrompt(t *testing.T) {
	const tpl = "title: " + titlePlaceholder + " | text: "
	for _, c := range []struct{ prefix, title, want string }{
		{tpl, "Game  Consoles\n2024", "title: Game Consoles 2024 | text: "},
		{tpl, "", "title: none | text: "},
		{tpl, "  \n ", "title: none | text: "},
		{tpl, strings.Repeat("я", maxTitleRunes+50), "title: " + strings.Repeat("я", maxTitleRunes) + " | text: "},
		{"passage: ", "Game Consoles", "passage: "}, // no slot: the prefix as it is
		{"", "Game Consoles", ""},
	} {
		if got := docPrompt(c.prefix, c.title); got != c.want {
			t.Errorf("docPrompt(%q, %q) = %q, want %q", c.prefix, c.title, got, c.want)
		}
	}
}

// With documents embedding under their object's name, a rename puts the
// object's embedded docs back in the queue — keeping their vectors until
// the new ones land — and DocTitles pairs each doc with the name.
func TestStore_RenameRequeuesVectors(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 4)
	s.SetEmbedTitles(true)
	const sp = "space1"
	body := "a paragraph long enough to be embedded as a document of this object"
	vec := []float32{1, 0, 0, 0}
	apply := func(ups ...DocUpsert) {
		t.Helper()
		if err := s.Apply(ctx, sp, ups, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	pending := func() []string {
		t.Helper()
		ids, _, err := s.Pending(ctx, sp, 10)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(ids)
		return ids
	}
	apply(
		DocUpsert{Entry: entry("basic", "obj1", index.DatasetProp, index.NamePropRecordId, "Consoles", 1)},
		DocUpsert{Entry: entry("basic", "obj1", "editor_blocks", "win_a", body, 2), Vector: vec},
		DocUpsert{Entry: entry("basic", "obj1", "editor_blocks", "win_b", body+" two", 3), Vector: vec},
		DocUpsert{Entry: entry("basic", "obj2", index.DatasetProp, index.NamePropRecordId, "Other", 4)},
		DocUpsert{Entry: entry("basic", "obj2", "editor_blocks", "win_a", body, 5), Vector: vec},
		DocUpsert{Entry: entry("basic", "obj3", "editor_blocks", "win_a", body, 6), Vector: vec},
	)
	// Writing the name in the same page as the docs requeues them too:
	// their vectors may predate it.
	if err := s.SetVectors(ctx, sp, pending(), [][]float32{vec, vec, vec}[:len(pending())]); err != nil {
		t.Fatal(err)
	}
	if got := pending(); len(got) != 0 {
		t.Fatalf("pending after landing vectors: %v", got)
	}

	apply(DocUpsert{Entry: entry("basic", "obj1", index.DatasetProp, index.NamePropRecordId, "Game Consoles", 7)})
	want := []string{"obj1:editor_blocks:win_a", "obj1:editor_blocks:win_b"}
	got := pending()
	if !slices.Equal(got, want) {
		t.Fatalf("pending after the rename = %v, want %v", got, want)
	}
	coll, err := s.spaceColl(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range want {
		doc, err := coll.FindId(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if doc.Value().Get("vector") == nil {
			t.Fatalf("%s lost its vector while requeued", id)
		}
	}

	titles, err := s.DocTitles(ctx, sp, append(got, "obj2:editor_blocks:win_a", "obj3:editor_blocks:win_a"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(titles, []string{"Game Consoles", "Game Consoles", "Other", ""}) {
		t.Fatalf("titles = %q", titles)
	}

	// Clearing the name changes the prompt too.
	if err := s.SetVectors(ctx, sp, got, [][]float32{vec, vec}); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(ctx, sp, nil, []string{docId("obj1", index.DatasetProp, index.NamePropRecordId)}, nil); err != nil {
		t.Fatal(err)
	}
	if got := pending(); !slices.Equal(got, want) {
		t.Fatalf("pending after the name was cleared = %v, want %v", got, want)
	}

	// The name entry is re-streamed with every change to the object: an
	// unchanged name, and the delete an unnamed object's empty name
	// plans, requeue nothing.
	if err := s.SetVectors(ctx, sp, want, [][]float32{vec, vec}); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(ctx, sp, nil, []string{
		docId("obj1", index.DatasetProp, index.NamePropRecordId),
		docId("obj3", index.DatasetProp, index.NamePropRecordId),
	}, nil); err != nil {
		t.Fatal(err)
	}
	apply(DocUpsert{Entry: entry("basic", "obj2", index.DatasetProp, index.NamePropRecordId, "Other", 9)})
	if got := pending(); len(got) != 0 {
		t.Fatalf("pending after re-streamed unchanged names: %v", got)
	}
	// Naming it again is a change.
	apply(DocUpsert{Entry: entry("basic", "obj1", index.DatasetProp, index.NamePropRecordId, "Game Consoles", 10)})
	if got := pending(); !slices.Equal(got, want) {
		t.Fatalf("pending after naming the object again = %v, want %v", got, want)
	}
	got = want

	// An embedder without a title slot: no names, no requeue.
	s.SetEmbedTitles(false)
	if err := s.SetVectors(ctx, sp, got, [][]float32{vec, vec}); err != nil {
		t.Fatal(err)
	}
	apply(DocUpsert{Entry: entry("basic", "obj1", index.DatasetProp, index.NamePropRecordId, "Consoles again", 8)})
	if got := pending(); len(got) != 0 {
		t.Fatalf("pending after a rename without titles: %v", got)
	}
	if titles, err := s.DocTitles(ctx, sp, want); err != nil || titles != nil {
		t.Fatalf("titles without the switch = %q, %v", titles, err)
	}
}

// A doc whose object was renamed while its batch embedded is not landed.
func TestSameTitle(t *testing.T) {
	ids := []string{"a", "b", "c"}
	vecs := [][]float32{{1}, {2}, {3}}
	was := []string{"x", "y", ""}
	if gotIds, gotVecs := sameTitle(ids, vecs, was, []string{"x", "y", ""}); !slices.Equal(gotIds, ids) || len(gotVecs) != 3 {
		t.Fatalf("unchanged names dropped docs: %v", gotIds)
	}
	gotIds, gotVecs := sameTitle(ids, vecs, was, []string{"x", "renamed", "named"})
	if !slices.Equal(gotIds, []string{"a"}) || len(gotVecs) != 1 || gotVecs[0][0] != 1 {
		t.Fatalf("kept %v %v, want only the doc whose name stood", gotIds, gotVecs)
	}
}
