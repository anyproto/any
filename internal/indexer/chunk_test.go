package indexer

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/anyproto/any/internal/index"
)

func TestSplitText(t *testing.T) {
	short := "fits in one piece"
	if got := splitText(short, 100); len(got) != 1 || got[0] != short {
		t.Fatalf("short = %q", got)
	}

	// Paragraph boundary in the second half of the window wins over a
	// later sentence end.
	para := strings.Repeat("a ", 30) + "\n\n" + strings.Repeat("b ", 30) + "end. " + strings.Repeat("c ", 40)
	got := splitText(para, 100)
	if len(got) < 2 || !strings.HasSuffix(got[0], "a") || !strings.HasPrefix(got[1], "b ") {
		t.Fatalf("paragraph split = %q", got)
	}
	for _, p := range got {
		if n := utf8.RuneCountInString(p); n > 100 {
			t.Fatalf("piece of %d runes exceeds bound: %q", n, p)
		}
	}

	// No boundary at all → hard cut, every rune preserved.
	solid := strings.Repeat("x", 250)
	got = splitText(solid, 100)
	if len(got) != 3 || strings.Join(got, "") != solid {
		t.Fatalf("hard cut = %d pieces", len(got))
	}

	// Rune-aware: multibyte text counts runes, not bytes.
	cjk := strings.Repeat("日本語 ", 60) // 240 runes
	got = splitText(cjk, 100)
	if len(got) != 3 {
		t.Fatalf("cjk pieces = %d, want 3", len(got))
	}
	for _, p := range got {
		if !utf8.ValidString(p) {
			t.Fatalf("piece is not valid utf8: %q", p)
		}
	}
}

func TestExpandEntry(t *testing.T) {
	e := index.IndexEntry{ObjectId: "o", Dataset: "email_messages", RecordId: "r", Title: "Subject", ApplySeq: 7}
	e.Data = "Subject\n" + strings.Repeat("body text ", 50) // ~510 runes

	one := expandEntry(index.IndexEntry{ObjectId: "o", Dataset: "d", RecordId: "r", Data: "tiny"}, 100)
	if len(one) != 1 || one[0].Chunk != 0 || one[0].Entry.Data != "tiny" {
		t.Fatalf("short entry = %+v", one)
	}
	if expandEntry(index.IndexEntry{Data: ""}, 100) != nil {
		t.Fatal("empty data must expand to nothing")
	}

	ups := expandEntry(e, 200)
	if len(ups) < 3 {
		t.Fatalf("chunks = %d, want >= 3", len(ups))
	}
	if ups[0].Chunk != 0 || !strings.HasPrefix(ups[0].Entry.Data, "Subject\n") {
		t.Fatalf("chunk 0 = %+v", ups[0])
	}
	for i, up := range ups {
		if up.Chunk != i || up.Entry.RecordId != "r" || up.Entry.ApplySeq != 7 || up.Entry.Title != "Subject" {
			t.Fatalf("chunk %d identity = %+v", i, up)
		}
		if i > 0 && !strings.HasPrefix(up.Entry.Data, "Subject\n") {
			t.Fatalf("chunk %d lacks the title prefix: %q", i, up.Entry.Data)
		}
	}
}

func TestChunkDocIdRange(t *testing.T) {
	base := docId("obj", "ds", "rec")
	if chunkDocId(base, 0) != base {
		t.Fatal("chunk 0 must keep the base id")
	}
	c2 := chunkDocId(base, 2)
	up := recordUpper(base)
	if !(base >= base && base < up) || !(c2 >= base && c2 < up) {
		t.Fatalf("chunk ids must fall in [base, upper): %q %q %q", base, c2, up)
	}
	// Sibling records that merely extend the id stay outside the range.
	for _, other := range []string{docId("obj", "ds", "rec2"), docId("obj", "ds", "rec:x"), docId("obj", "ds", "rec.1"), docId("obj", "ds", "rec-1")} {
		if other >= base && other < up {
			t.Fatalf("%q must not fall in record %q's range", other, base)
		}
	}
}

// planDocs: unchanged chunks are skipped, changed ones upserted, a
// record that now yields fewer chunks drops its tail, an empty-Data
// entry deletes its base id.
func TestPlanDocs(t *testing.T) {
	long := strings.Repeat("word ", 100) // 500 runes → 3 chunks at 200
	e := index.IndexEntry{ObjectId: "o", Dataset: "d", RecordId: "r", Data: long}
	base := docId("o", "d", "r")

	var cold pageOps
	planDocs([]index.IndexEntry{e}, nil, 200, &cold)
	if len(cold.ups) != 3 || len(cold.dels) != 0 {
		t.Fatalf("cold = %d ups / %d dels, want 3 / 0", len(cold.ups), len(cold.dels))
	}
	stored := map[string]string{}
	for _, up := range cold.ups {
		stored[chunkDocId(base, up.Chunk)] = docHash(up.Entry.Data)
	}

	// Same text again → nothing to do.
	var same pageOps
	planDocs([]index.IndexEntry{e}, stored, 200, &same)
	if len(same.ups) != 0 || len(same.dels) != 0 {
		t.Fatalf("unchanged = %d ups / %d dels", len(same.ups), len(same.dels))
	}

	// Shrunk to one chunk → chunk 0 changes, chunks 1..2 vanish.
	small := e
	small.Data = "short now"
	var shrink pageOps
	planDocs([]index.IndexEntry{small}, stored, 200, &shrink)
	if len(shrink.ups) != 1 || shrink.ups[0].Chunk != 0 {
		t.Fatalf("shrink ups = %+v", shrink.ups)
	}
	if len(shrink.dels) != 2 {
		t.Fatalf("shrink dels = %v, want the two trailing chunk ids", shrink.dels)
	}

	// Tombstone → base id deleted once, even though stored lists chunks.
	gone := e
	gone.Data = ""
	var del pageOps
	planDocs([]index.IndexEntry{gone}, stored, 200, &del)
	if len(del.ups) != 0 || len(del.dels) != 3 || del.dels[0] != base {
		t.Fatalf("tombstone = ups %d dels %v", len(del.ups), del.dels)
	}
}
