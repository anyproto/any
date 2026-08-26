package indexer

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anyproto/any-store/v2/query"
)

// snippetTerms is the lowercase term list a hit's Data window is
// centered on: the query's words and the require terms, with phrase
// quotes and prefix stars stripped so "zeppelin disaster" and zepp*
// locate the same text the FTS leg matched on. Stop words stay — the
// list only picks a window, it doesn't rank.
func snippetTerms(q string, require []string) []string {
	var out []string
	add := func(s string) {
		for _, c := range query.ParseTextSearch(s) {
			for _, f := range strings.Fields(strings.ToLower(c.Raw)) {
				if f = strings.TrimRight(f, "*"); f != "" {
					out = append(out, f)
				}
			}
		}
	}
	add(q)
	for _, r := range require {
		add(r)
	}
	return out
}

// snippet windows data to at most maxRunes runes around the earliest
// occurrence of any term (case-insensitive, rune offsets), snapping the
// window's edges outward-then-inward to whitespace so it doesn't open or
// close mid-word. No match, or maxRunes < 0, keeps the head / the whole
// text. Returns the window, its rune offset in data, and data's rune
// length.
func snippet(data string, terms []string, maxRunes int) (string, int, int) {
	total := utf8.RuneCountInString(data)
	if maxRunes < 0 || total <= maxRunes {
		return data, 0, total
	}
	runes := []rune(data)
	lower := []rune(strings.ToLower(data))
	at := -1
	for _, t := range terms {
		if i := indexRunes(lower, []rune(t)); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	start := 0
	if at > 0 {
		// Lead with roughly a third of context before the match.
		start = at - maxRunes/3
		if start < 0 {
			start = 0
		}
		if start+maxRunes > total {
			start = total - maxRunes
		}
		// Snap the start forward to a word boundary (never past the match).
		for start > 0 && start < at && !unicode.IsSpace(runes[start-1]) {
			start++
		}
	}
	end := start + maxRunes
	if end > total {
		end = total
	}
	// Snap the end back to a word boundary, but not before the match.
	for end < total && end > start+1 && !unicode.IsSpace(runes[end]) && (at < 0 || end > at+1) {
		end--
	}
	return strings.TrimSpace(string(runes[start:end])), start, total
}

// indexRunes is strings.Index over rune slices.
func indexRunes(hay, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(hay) {
		return -1
	}
outer:
	for i := 0; i+len(needle) <= len(hay); i++ {
		for j := range needle {
			if hay[i+j] != needle[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}
