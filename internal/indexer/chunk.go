package indexer

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anyproto/any/internal/index"
)

// Long records are split into several index docs. A record longer than
// the bound would otherwise be one oversized `data` value on every hit,
// and carry vector recall only for the head its embedding covered.
// Chunking is generic: the worker splits every chunker's entry, so
// chunkers keep emitting one entry per record and never learn about it.
//
// Doc ids: chunk 0 keeps the record's id (`objectId:dataset:recordId`);
// chunk n > 0 is that id + chunkSep + n. chunkSep is U+001F, a control
// byte the SDK's id patterns never admit (auto ids are CIDs, user ids
// default to `[A-Za-z0-9._:-]+`), so a record's docs occupy exactly the
// primary-key range [base, base+" ") — every byte a real id can follow
// `base` with sorts at or above 0x20 — and record-level removal stays a
// single seek (recordUpper).
const (
	chunkSep = "\x1f"
	// recordRangeEnd is the exclusive upper bound suffix of a record's
	// doc range: the first byte a record id can legally continue with.
	recordRangeEnd = "\x20"
	// DefaultChunkRunes is the split target: ~500 tokens of prose, well
	// inside the local embedder's 2048-token clamp so every chunk embeds
	// whole, and a lexical unit small enough that a hit's data is the
	// matching passage rather than the whole mail.
	DefaultChunkRunes = 2000
)

// chunkDocId is the primary key of chunk n of the record with doc id base.
func chunkDocId(base string, n int) string {
	if n == 0 {
		return base
	}
	return base + chunkSep + strconv.Itoa(n)
}

// recordBase strips a chunk suffix, returning the record's base doc id.
func recordBase(id string) string {
	if i := strings.Index(id, chunkSep); i >= 0 {
		return id[:i]
	}
	return id
}

// recordUpper is the exclusive upper bound of the id range holding a
// record's docs (the base id plus every chunk suffix).
func recordUpper(base string) string {
	return base + recordRangeEnd
}

// expandEntry splits one chunker entry into its chunk upserts. Chunk 0
// carries the entry's Data head; later chunks re-prefix the entry's
// Title (when present) so a mid-record chunk keeps the record's
// identity for both BM25F and the embedder, mirroring how the chunkers
// fold the title into Data. An entry within the bound is one chunk —
// the common case for blocks, messages and property values.
func expandEntry(e index.IndexEntry, maxRunes int) []DocUpsert {
	if e.Data == "" {
		return nil
	}
	if maxRunes <= 0 {
		maxRunes = DefaultChunkRunes
	}
	title := e.Title
	// The title is re-prefixed onto later chunks, so it spends the same
	// budget as the text. It is not a curated string — a runtime dataset
	// maps whatever field x-search.title names — so clamp it to half the
	// bound. Unclamped it both overruns maxRunes and, since it is
	// prefixed FIRST and the embedder truncates the head, crowds the
	// record's own text out of the vector.
	if half := maxRunes / 2; utf8.RuneCountInString(title) > half {
		title = string([]rune(title)[:half])
	}
	pieces := splitText(e.Data, maxRunes)
	if len(pieces) > 1 && title != "" {
		// Split so a prefixed chunk still fits the bound.
		pieces = splitText(e.Data, maxRunes-utf8.RuneCountInString(title)-1)
	}
	out := make([]DocUpsert, 0, len(pieces))
	for i, p := range pieces {
		ce := e
		if i > 0 && title != "" && !strings.HasPrefix(p, title) {
			p = title + "\n" + p
		}
		ce.Data = p
		out = append(out, DocUpsert{Entry: ce, Chunk: i})
	}
	return out
}

// splitText cuts text into pieces of at most maxRunes runes, preferring
// to break — in that order — at a blank line, a line break, a sentence
// end, or whitespace found in the second half of the window, and
// falling back to a hard cut. Pieces are trimmed of surrounding
// whitespace; an empty piece is dropped. Deterministic, no overlap.
func splitText(text string, maxRunes int) []string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return []string{text}
	}
	var out []string
	rest := []rune(text)
	for len(rest) > maxRunes {
		cut := breakPoint(rest[:maxRunes])
		piece := strings.TrimSpace(string(rest[:cut]))
		if piece != "" {
			out = append(out, piece)
		}
		rest = rest[cut:]
	}
	if tail := strings.TrimSpace(string(rest)); tail != "" {
		out = append(out, tail)
	}
	return out
}

// breakPoint returns the cut index inside window — just after the best
// boundary in its second half, or len(window) when there is none.
func breakPoint(window []rune) int {
	half := len(window) / 2
	best := func(ok func(i int) bool) int {
		for i := len(window) - 1; i >= half; i-- {
			if ok(i) {
				return i + 1
			}
		}
		return -1
	}
	if i := best(func(i int) bool { return window[i] == '\n' && i > 0 && window[i-1] == '\n' }); i > 0 {
		return i
	}
	if i := best(func(i int) bool { return window[i] == '\n' }); i > 0 {
		return i
	}
	if i := best(func(i int) bool {
		return (window[i] == '.' || window[i] == '!' || window[i] == '?') && i+1 < len(window) && unicode.IsSpace(window[i+1])
	}); i > 0 {
		return i
	}
	if i := best(func(i int) bool { return unicode.IsSpace(window[i]) }); i > 0 {
		return i
	}
	return len(window)
}
