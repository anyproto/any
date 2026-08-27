//go:build fts

package indexer

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/anyproto/any/internal/index"
)

// A long Title is re-prefixed onto every later chunk, so it must not
// push those chunks past the bound (nor, being prefixed first, crowd the
// record's own text out of the embedder's window).
func TestExpandEntryTitleStaysInBound(t *testing.T) {
	for _, titleRunes := range []int{10, 60, 300, 5000} {
		e := index.IndexEntry{
			ObjectId: "o", Dataset: "d", RecordId: "r",
			Title: strings.Repeat("T", titleRunes),
			Data:  strings.Repeat("word ", 400),
		}
		const bound = 100
		ups := expandEntry(e, bound)
		if len(ups) < 2 {
			t.Fatalf("title=%d: want several chunks, got %d", titleRunes, len(ups))
		}
		for i, up := range ups {
			if n := utf8.RuneCountInString(up.Entry.Data); n > bound {
				t.Errorf("title=%d chunk %d is %d runes, over the %d bound", titleRunes, i, n, bound)
			}
		}
	}
}

// The data window must centre on the passage, not the head: stop words
// occur in the first line of nearly every record, and an unanchored
// substring matches inside unrelated words.
func TestSnippetCentersOnPassage(t *testing.T) {
	for _, tc := range []struct{ name, data, query, want string }{
		{
			name:  "stop word does not anchor",
			data:  "The team met on Tuesday. " + strings.Repeat("routine notes. ", 60) + "The Hindenburg burned in 1937.",
			query: "what is the hindenburg disaster",
			want:  "Hindenburg",
		},
		{
			name:  "term must match at a token boundary",
			data:  "Money was the start of it. " + strings.Repeat("filler filler. ", 60) + "The one true art of shipping.",
			query: "one art",
			want:  "one true art",
		},
	} {
		got, off, total := snippet(tc.data, foldTerms(snippetTerms(tc.query, nil)), 200)
		if !strings.Contains(strings.ToLower(got), strings.ToLower(tc.want)) {
			t.Errorf("%s: window %q missing %q", tc.name, got, tc.want)
		}
		// dataOffset must locate the returned text in the chunk.
		runes := []rune(tc.data)
		if total != len(runes) {
			t.Errorf("%s: dataTotal=%d, want %d", tc.name, total, len(runes))
		}
		if n := utf8.RuneCountInString(got); off+n > total || string(runes[off:off+n]) != got {
			t.Errorf("%s: dataOffset %d does not locate data\n at offset: %q\n returned:  %q",
				tc.name, off, string(runes[off:min(off+n, total)]), got)
		}
	}
}

// Whitespace at the snapped edge must not desynchronise dataOffset.
func TestSnippetOffsetLocatesDataAfterTrim(t *testing.T) {
	data := strings.Repeat("a", 380) + "  " + strings.Repeat("x", 19) + "needle" + strings.Repeat("b", 400)
	got, off, total := snippet(data, foldTerms([]string{"needle"}), 64)
	runes := []rune(data)
	n := utf8.RuneCountInString(got)
	if off+n > total || string(runes[off:off+n]) != got {
		t.Errorf("dataOffset %d does not locate data\n at offset: %q\n returned:  %q", off, string(runes[off:min(off+n, total)]), got)
	}
}
