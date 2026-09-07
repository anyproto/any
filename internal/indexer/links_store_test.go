package indexer

import (
	"context"
	"testing"

	"github.com/anyproto/any/anyuri"
	"github.com/anyproto/any/internal/index"
)

// The link sink needs no full-text or vector capability: these tests
// carry no build tag on purpose.

func linkStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStoreInMemory(context.Background(), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func edge(objectId, dataset, recordId, kind, target string) index.LinkEntry {
	u, err := anyuri.Parse(target)
	if err != nil {
		panic(err)
	}
	c, ok := u.Canonical("sp1")
	if !ok {
		panic("not a target: " + target)
	}
	return index.LinkEntry{ObjectId: objectId, Dataset: dataset, RecordId: recordId, Kind: kind, Target: c}
}

func keys(docs []LinkDoc) []string {
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.ObjectId+"/"+d.Dataset+"/"+d.RecordId+"→"+d.Target.String())
	}
	return out
}

func TestStore_LinksApplyAndRead(t *testing.T) {
	ctx := context.Background()
	s := linkStore(t)
	const sp = "sp1"

	ops := &LinkOps{
		Ups: []index.LinkEntry{
			edge("P", "editor_blocks", "b1", "link", "any://o/sp1/X"),
			edge("P", "editor_blocks", "b1", "link", "any://o/sp1/X/editor_blocks/z"),
			edge("P", "editor_blocks", "b2", "mention", "any://m/sp1/ident1"),
			edge("C", "chat_messages", "m1", "link", "any://o/sp1/X"),
			edge("C", "chat_messages", "m1", "link", "any://o/sp1/X"), // duplicate from one place
			edge("Q", "prop", "p1", "relation", "any://o/sp1/Y"),
		},
		Seqs: []uint64{1, 1, 2, 3, 3, 4},
	}
	touched, err := s.ApplyPage(ctx, sp, nil, nil, nil, ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 3 { // X, ident1, Y
		t.Errorf("touched = %v, want 3 targets", touched)
	}

	// Backlinks of X by object: the whole-object edges AND the block edge.
	docs, err := s.Backlinks(ctx, sp, "any://o/sp1/X", true, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("backlinks of X = %v, want 3", keys(docs))
	}
	// Exactly the block target.
	docs, err = s.Backlinks(ctx, sp, "any://o/sp1/X/editor_blocks/z", false, nil, 0)
	if err != nil || len(docs) != 1 || docs[0].RecordId != "b1" {
		t.Fatalf("backlinks of block z = %v (%v)", keys(docs), err)
	}
	// Kind filter.
	docs, err = s.Backlinks(ctx, sp, "any://o/sp1/X", true, []string{"mention"}, 0)
	if err != nil || len(docs) != 0 {
		t.Fatalf("mention backlinks of X = %v (%v), want none", keys(docs), err)
	}
	docs, err = s.Backlinks(ctx, sp, "any://m/sp1/ident1", false, nil, 0)
	if err != nil || len(docs) != 1 || docs[0].Kind != "mention" {
		t.Fatalf("backlinks of the identity = %v (%v)", keys(docs), err)
	}

	// Forward links: object, dataset, record prefixes.
	docs, err = s.Links(ctx, sp, "P:", nil, 0)
	if err != nil || len(docs) != 3 {
		t.Fatalf("links of P = %v (%v), want 3", keys(docs), err)
	}
	docs, err = s.Links(ctx, sp, "P:editor_blocks:b2:", nil, 0)
	if err != nil || len(docs) != 1 || docs[0].Target.Identity != "ident1" {
		t.Fatalf("links of P/b2 = %v (%v)", keys(docs), err)
	}

	// Record replace: b1 now links only Y; the X edges of b1 go.
	touched, err = s.ApplyPage(ctx, sp, nil, nil, nil, &LinkOps{
		Ups:  []index.LinkEntry{edge("P", "editor_blocks", "b1", "link", "any://o/sp1/Y")},
		Seqs: []uint64{5},
		Dels: []string{"P:editor_blocks:b1:"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 2 { // X (removed) and Y (added)
		t.Errorf("touched after replace = %v, want X and Y", touched)
	}
	docs, _ = s.Backlinks(ctx, sp, "any://o/sp1/X", true, nil, 0)
	if len(docs) != 1 || docs[0].ObjectId != "C" {
		t.Fatalf("backlinks of X after replace = %v, want only the chat edge", keys(docs))
	}

	// Collection rewrite (reconciled shape): P's editor edges replaced whole.
	if _, err := s.ApplyPage(ctx, sp, nil, nil, nil, &LinkOps{
		Prefixes: []string{"P:editor_blocks:"},
		Ups:      []index.LinkEntry{edge("P", "editor_blocks", "b3", "card", "any://f/sp1/file1")},
		Seqs:     []uint64{6},
	}); err != nil {
		t.Fatal(err)
	}
	docs, _ = s.Links(ctx, sp, "P:", nil, 0)
	if len(docs) != 1 || docs[0].Kind != "card" {
		t.Fatalf("links of P after rewrite = %v, want one card", keys(docs))
	}

	// Structural eviction rides the shared prefix: the chat object is
	// deleted, its edges go with the text docs.
	if _, err := s.ApplyPage(ctx, sp, nil, nil, []string{"C:"}, nil); err != nil {
		t.Fatal(err)
	}
	docs, _ = s.Backlinks(ctx, sp, "any://o/sp1/X", true, nil, 0)
	if len(docs) != 0 {
		t.Fatalf("backlinks of X after object eviction = %v, want none", keys(docs))
	}
	// An upsert of an evicted object in the same page is dropped.
	if _, err := s.ApplyPage(ctx, sp, nil, nil, []string{"Q:"}, &LinkOps{
		Ups: []index.LinkEntry{edge("Q", "prop", "p1", "relation", "any://o/sp1/Y")}, Seqs: []uint64{7},
	}); err != nil {
		t.Fatal(err)
	}
	docs, _ = s.Backlinks(ctx, sp, "any://o/sp1/Y", true, nil, 0)
	if len(docs) != 0 {
		t.Fatalf("backlinks of Y = %v, want none (Q evicted, P's b1 rewritten away)", keys(docs))
	}
	if docs, _ = s.Links(ctx, sp, "Q:", nil, 0); len(docs) != 0 {
		t.Fatalf("links of the evicted Q = %v", keys(docs))
	}

	spaces, err := s.LinkSpaces(ctx)
	if err != nil || len(spaces) != 1 || spaces[0] != sp {
		t.Fatalf("link spaces = %v (%v)", spaces, err)
	}
	if err := s.DropSpace(ctx, sp); err != nil {
		t.Fatal(err)
	}
	docs, _ = s.Backlinks(ctx, sp, "any://f/sp1/file1", false, nil, 0)
	if len(docs) != 0 {
		t.Fatalf("backlinks after DropSpace = %v", keys(docs))
	}
}

func TestStore_LinksVersionStamp(t *testing.T) {
	ctx := context.Background()
	s := linkStore(t)
	const sp = "sp1"

	// A never-indexed space needs no backfill; an indexed one stamped
	// on the current layout neither.
	if need, _ := s.LinksBackfillNeeded(ctx, sp); need {
		t.Error("fresh space needs a backfill")
	}
	if err := s.SetCursor(ctx, sp, 10, "gen"); err != nil {
		t.Fatal(err)
	}
	if need, _ := s.LinksBackfillNeeded(ctx, sp); need {
		t.Error("stamped space needs a backfill")
	}
	// A cursor written before the link sink existed (no stamp) does.
	if err := s.stampLinksVersionAs(ctx, sp, 0); err != nil {
		t.Fatal(err)
	}
	if need, _ := s.LinksBackfillNeeded(ctx, sp); !need {
		t.Error("unstamped indexed space needs no backfill")
	}
	if err := s.ReplaceLinks(ctx, sp, &LinkOps{
		Ups: []index.LinkEntry{edge("P", "prop", "p1", "relation", "any://o/sp1/Y")}, Seqs: []uint64{1},
	}); err != nil {
		t.Fatal(err)
	}
	if need, _ := s.LinksBackfillNeeded(ctx, sp); need {
		t.Error("backfilled space still needs a backfill")
	}
	docs, _ := s.Backlinks(ctx, sp, "any://o/sp1/Y", true, nil, 0)
	if len(docs) != 1 {
		t.Fatalf("backlinks after backfill = %v", keys(docs))
	}
}

func TestPlanLinks(t *testing.T) {
	e := func(rec string, links ...index.LinkEntry) index.IndexEntry {
		return index.IndexEntry{ObjectId: "P", Dataset: "editor_blocks", RecordId: rec, ApplySeq: 9, Links: links}
	}
	l := edge("P", "editor_blocks", "b1", "link", "any://o/sp1/X")

	// Reconciled shape: the collection prefix is cleared even for an
	// empty set (the last linked block was deleted).
	var full LinkOps
	planLinks(nil, "P", "editor_blocks", 5, true, &full)
	if len(full.Prefixes) != 1 || full.Prefixes[0] != "P:editor_blocks:" || len(full.Ups) != 0 {
		t.Errorf("empty reconciled set: %+v", full)
	}
	full = LinkOps{}
	planLinks([]index.IndexEntry{e("win_b1", l)}, "P", "editor_blocks", 5, true, &full)
	if len(full.Prefixes) != 1 || len(full.Ups) != 1 || len(full.Dels) != 0 || full.Seqs[0] != 9 {
		t.Errorf("reconciled set: %+v", full)
	}

	// Streaming shape: per-record clears past the cold cursor, none on it.
	var stream LinkOps
	planLinks([]index.IndexEntry{e("b1", l), e("b2")}, "P", "editor_blocks", 5, false, &stream)
	if len(stream.Prefixes) != 0 || len(stream.Dels) != 2 || len(stream.Ups) != 1 {
		t.Errorf("stream past cursor: %+v", stream)
	}
	stream = LinkOps{}
	planLinks([]index.IndexEntry{e("b1", l)}, "P", "editor_blocks", 0, false, &stream)
	if len(stream.Dels) != 0 || len(stream.Ups) != 1 {
		t.Errorf("stream on cold cursor: %+v", stream)
	}
	// A control byte in the record id is skipped, like its text doc.
	stream = LinkOps{}
	planLinks([]index.IndexEntry{e("b\x1f1", l)}, "P", "editor_blocks", 5, false, &stream)
	if len(stream.Dels) != 0 || len(stream.Ups) != 0 {
		t.Errorf("control byte: %+v", stream)
	}
}
