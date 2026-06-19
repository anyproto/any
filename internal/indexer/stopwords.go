package indexer

import "strings"

// stopWords is a small, conservative English stop list applied to the FTS
// leg only (chunker-hybrid-search-report § 6.2). any-store's BM25 engine
// is bag-of-words OR with no stop-word support, so each common word in a
// query matches a large fraction of the corpus and pulls ranking toward
// length/frequency noise. Kept deliberately short and English-only: the
// vector leg always sees the full query, so anything dropped here still
// participates in semantic recall.
var stopWords = map[string]struct{}{}

func init() {
	for _, w := range []string{
		"a", "an", "the",
		"and", "or", "but", "if", "then", "else",
		"of", "to", "in", "on", "at", "for", "with", "by", "from", "as",
		"into", "about", "over", "under",
		"is", "are", "was", "were", "be", "been", "being",
		"do", "does", "did", "done",
		"have", "has", "had",
		"how", "what", "when", "where", "who", "whom", "why", "which",
		"that", "this", "these", "those",
		"i", "you", "we", "they", "it", "he", "she",
		"my", "me", "our", "your",
		"can", "could", "would", "should", "will", "shall", "may", "might", "must",
		"so", "than", "too", "very", "just",
	} {
		stopWords[w] = struct{}{}
	}
}

// stripStopWords drops stop words from a query, preserving the order and
// original spelling of the words it keeps. Comparison is case-insensitive
// on the alphanumeric core of each whitespace-delimited token (so
// trailing punctuation like "how?" still matches "how"). If stripping
// would remove every token (a query made entirely of stop words, e.g.
// "what is the"), the original query is returned unchanged — better to
// run a noisy query than an empty one.
func stripStopWords(q string) string {
	fields := strings.Fields(q)
	kept := make([]string, 0, len(fields))
	for _, f := range fields {
		if _, stop := stopWords[normalizeToken(f)]; stop {
			continue
		}
		kept = append(kept, f)
	}
	if len(kept) == 0 {
		return q
	}
	return strings.Join(kept, " ")
}

// normalizeToken lowercases a token and strips any non-alphanumeric
// runes, yielding the bare word used for stop-list lookup.
func normalizeToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
