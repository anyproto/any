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

	ups := []index.LinkEntry{
		edge("P", "editor_blocks", "b1", "link", "any://o/sp1/X"),
		edge("P", "editor_blocks", "b1", "link", "any://o/sp1/X/editor_blocks/z"),
		edge("P", "editor_blocks", "b2", "mention", "any://m/sp1/ident1"),
		edge("C", "chat_messages", "m1", "link", "any://o/sp1/X"),
		edge("C", "chat_messages", "m1", "link", "any://o/sp1/X"), // duplicate from one place
		edge("Q", "prop", "p1", "relation", "any://o/sp1/Y"),
		edge("R", "mail", "thread:1", "relation", "any://o/sp1/Y"),       // record ids may carry ':'
		edge("R", "mail", "thread:1:reply", "relation", "any://o/sp1/Y"), // … and be prefixes of each other
	}
	touched, err := s.ApplyPage(ctx, sp, nil, nil, nil, nil, &LinkOps{
		Ups: ups, Seqs: []uint64{1, 1, 2, 3, 3, 4, 5, 5},
		Touched: []string{"any://o/sp1/X", "any://m/sp1/ident1", "any://o/sp1/Y"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 3 {
		t.Errorf("touched = %v, want 3 targets", touched)
	}

	// Backlinks of X by object: the whole-object edges AND the block edge.
	docs, more, err := s.Backlinks(ctx, sp, "any://o/sp1/X", true, nil, 0)
	if err != nil || more {
		t.Fatal(err, more)
	}
	if len(docs) != 3 {
		t.Fatalf("backlinks of X = %v, want 3", keys(docs))
	}
	// Exactly the block target.
	docs, _, err = s.Backlinks(ctx, sp, "any://o/sp1/X/editor_blocks/z", false, nil, 0)
	if err != nil || len(docs) != 1 || docs[0].RecordId != "b1" {
		t.Fatalf("backlinks of block z = %v (%v)", keys(docs), err)
	}
	// Kind filter, identity target, and the cap.
	docs, _, err = s.Backlinks(ctx, sp, "any://o/sp1/X", true, []string{"mention"}, 0)
	if err != nil || len(docs) != 0 {
		t.Fatalf("mention backlinks of X = %v (%v), want none", keys(docs), err)
	}
	docs, _, err = s.Backlinks(ctx, sp, "any://m/sp1/ident1", false, nil, 0)
	if err != nil || len(docs) != 1 || docs[0].Kind != "mention" {
		t.Fatalf("backlinks of the identity = %v (%v)", keys(docs), err)
	}
	docs, more, err = s.Backlinks(ctx, sp, "any://o/sp1/Y", true, nil, 2)
	if err != nil || len(docs) != 2 || !more {
		t.Fatalf("capped backlinks of Y = %v more=%v (%v), want 2 + more", keys(docs), more, err)
	}

	// Forward links: object, dataset, record prefixes — a record whose
	// id is a prefix of a sibling's reads only its own edges.
	docs, _, err = s.Links(ctx, sp, "P:", nil, 0)
	if err != nil || len(docs) != 3 {
		t.Fatalf("links of P = %v (%v), want 3", keys(docs), err)
	}
	docs, _, err = s.Links(ctx, sp, linkRecordPrefix("P", "editor_blocks", "b2"), nil, 0)
	if err != nil || len(docs) != 1 || docs[0].Target.Identity != "ident1" {
		t.Fatalf("links of P/b2 = %v (%v)", keys(docs), err)
	}
	docs, _, err = s.Links(ctx, sp, linkRecordPrefix("R", "mail", "thread:1"), nil, 0)
	if err != nil || len(docs) != 1 || docs[0].RecordId != "thread:1" {
		t.Fatalf("links of R/thread:1 = %v (%v), want only its own", keys(docs), err)
	}

	// LinkIds: the diff's read, keyed by doc id with the liveness key.
	ids, err := s.LinkIds(ctx, sp, "P:")
	if err != nil || len(ids) != 3 || ids[linkDocId(ups[2])].Key != "any://m/sp1/ident1" || ids[linkDocId(ups[0])].Key != "any://o/sp1/X" {
		t.Fatalf("LinkIds(P) = %v (%v)", ids, err)
	}
	if ids, _ := s.LinkIds(ctx, "nowhere", "P:"); ids != nil {
		t.Errorf("LinkIds on an unindexed space = %v", ids)
	}

	// Exact deletes: b1 drops its X edges, keeps nothing else.
	touched, err = s.ApplyPage(ctx, sp, nil, nil, nil, nil, &LinkOps{
		Dels:    []string{linkDocId(ups[0]), linkDocId(ups[1])},
		Touched: []string{"any://o/sp1/X"},
	})
	if err != nil || len(touched) != 1 {
		t.Fatal(err, touched)
	}
	docs, _, _ = s.Backlinks(ctx, sp, "any://o/sp1/X", true, nil, 0)
	if len(docs) != 1 || docs[0].ObjectId != "C" {
		t.Fatalf("backlinks of X after delete = %v, want only the chat edge", keys(docs))
	}

	// Structural eviction rides the shared prefix: the chat object is
	// deleted, its edges go with the text docs, and its targets are
	// reported.
	touched, err = s.ApplyPage(ctx, sp, nil, nil, []string{"C:"}, nil, nil)
	if err != nil || len(touched) != 1 || touched[0] != "any://o/sp1/X" {
		t.Fatal(err, touched)
	}
	docs, _, _ = s.Backlinks(ctx, sp, "any://o/sp1/X", true, nil, 0)
	if len(docs) != 0 {
		t.Fatalf("backlinks of X after object eviction = %v, want none", keys(docs))
	}
	// A dataset prefix evicts one collection; an upsert of an evicted
	// object in the same page is dropped (removal wins).
	if _, err := s.ApplyPage(ctx, sp, nil, nil, []string{"R:mail:", "Q:"}, nil, &LinkOps{
		Ups: []index.LinkEntry{edge("Q", "prop", "p1", "relation", "any://o/sp1/Y")}, Seqs: []uint64{7},
	}); err != nil {
		t.Fatal(err)
	}
	docs, _, _ = s.Backlinks(ctx, sp, "any://o/sp1/Y", true, nil, 0)
	if len(docs) != 0 {
		t.Fatalf("backlinks of Y after evictions = %v, want none", keys(docs))
	}
	// A text-only prefix leaves edges alone.
	if _, err := s.ApplyPage(ctx, sp, nil, nil, nil, []string{"P:editor_blocks:"}, nil); err != nil {
		t.Fatal(err)
	}
	if docs, _, _ = s.Links(ctx, sp, "P:", nil, 0); len(docs) != 1 {
		t.Fatalf("text-only eviction touched edges: %v", keys(docs))
	}

	spaces, err := s.LinkSpaces(ctx)
	if err != nil || len(spaces) != 1 || spaces[0] != sp {
		t.Fatalf("link spaces = %v (%v)", spaces, err)
	}
	if err := s.DropSpace(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if docs, _, _ = s.Links(ctx, sp, "P:", nil, 0); len(docs) != 0 {
		t.Fatalf("links after DropSpace = %v", keys(docs))
	}
	if spaces, _ := s.LinkSpaces(ctx); len(spaces) != 0 {
		t.Fatalf("link spaces after DropSpace = %v", spaces)
	}
}

func TestStore_LinksVersionStamp(t *testing.T) {
	ctx := context.Background()
	s := linkStore(t)
	const sp = "sp1"

	// A never-indexed space needs no backfill; an indexed one stamped
	// on the current layout neither; an indexed one without a stamp (a
	// db from before the sink) does.
	if need, _ := s.LinksBackfillNeeded(ctx, sp); need {
		t.Error("fresh space needs a backfill")
	}
	if err := s.SetCursor(ctx, sp, 10, "gen"); err != nil {
		t.Fatal(err)
	}
	if need, _ := s.LinksBackfillNeeded(ctx, sp); !need {
		t.Error("indexed, unstamped space needs no backfill")
	}
	if err := s.StampLinksVersion(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if need, _ := s.LinksBackfillNeeded(ctx, sp); need {
		t.Error("stamped space needs a backfill")
	}
	if err := s.stampLinksVersionAs(ctx, sp, 0); err != nil {
		t.Fatal(err)
	}
	if need, _ := s.LinksBackfillNeeded(ctx, sp); !need {
		t.Error("space stamped on an old layout needs no backfill")
	}
	// The backfill's writes: reset, per-page apply, stamp.
	if err := s.ResetLinks(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyLinks(ctx, sp, &LinkOps{
		Ups: []index.LinkEntry{edge("P", "prop", "p1", "relation", "any://o/sp1/Y")}, Seqs: []uint64{1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.StampLinksVersion(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if need, _ := s.LinksBackfillNeeded(ctx, sp); need {
		t.Error("backfilled space still needs a backfill")
	}
	if docs, _, _ := s.Backlinks(ctx, sp, "any://o/sp1/Y", true, nil, 0); len(docs) != 1 {
		t.Fatalf("backlinks after backfill = %v", keys(docs))
	}
	// The cursor row keeps the stamp across cursor writes; DropSpace
	// takes it away with the row.
	if err := s.SetCursor(ctx, sp, 20, "gen"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.LinksVersion(ctx, sp); v != linksSchemaVersion {
		t.Errorf("stamp after SetCursor = %d", v)
	}
	if err := s.DropSpace(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if need, _ := s.LinksBackfillNeeded(ctx, sp); need {
		t.Error("dropped space needs a backfill")
	}
}

func TestPlanLinks(t *testing.T) {
	e := func(rec string, links ...index.LinkEntry) index.IndexEntry {
		return index.IndexEntry{ObjectId: "P", Dataset: "editor_blocks", RecordId: rec, ApplySeq: 9, Links: links}
	}
	l := edge("P", "editor_blocks", "b1", "link", "any://o/sp1/X")

	// Reconciled shape: the collection is the scope, even for an empty
	// set (the last linked block was deleted).
	var full objectLinks
	planLinks(nil, "P", "editor_blocks", true, &full)
	if len(full.scopes) != 1 || full.scopes[0] != "P:editor_blocks:" || len(full.edges) != 0 {
		t.Errorf("empty reconciled set: %+v", full)
	}
	full = objectLinks{}
	planLinks([]index.IndexEntry{e("win_b1", l)}, "P", "editor_blocks", true, &full)
	if len(full.scopes) != 1 || len(full.edges) != 1 || full.edges[linkDocId(l)].seq != 9 {
		t.Errorf("reconciled set: %+v", full)
	}

	// Streaming shape: each record is a scope; a record with no links
	// is a scope with nothing in it (its stored edges go).
	var stream objectLinks
	planLinks([]index.IndexEntry{e("b1", l), e("b2")}, "P", "editor_blocks", false, &stream)
	if len(stream.scopes) != 2 || stream.scopes[1] != linkRecordPrefix("P", "editor_blocks", "b2") || len(stream.edges) != 1 {
		t.Errorf("stream: %+v", stream)
	}
	// A control byte in the record id is skipped, like its text doc.
	stream = objectLinks{}
	planLinks([]index.IndexEntry{e("b\x1f1", l)}, "P", "editor_blocks", false, &stream)
	if len(stream.scopes) != 0 || len(stream.edges) != 0 {
		t.Errorf("control byte: %+v", stream)
	}
}
