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
			for _, f := range strings.Fields(c.Raw) {
				if f = strings.TrimRight(f, "*"); f != "" {
					out = append(out, strings.Map(unicode.ToLower, f))
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
// occurrence of any term (case-folded per rune so offsets stay exact),
// snapping the window's edges to whitespace within snapSlack runes so
// it doesn't open or close mid-word — beyond that (CJK prose, URLs,
// hashes) the unsnapped window stands. No match, or maxRunes < 0,
// keeps the head / the whole text. Returns the window, its rune offset
// in data, and data's rune length. terms are lowercase rune slices
// (foldTerms).
func snippet(data string, terms [][]rune, maxRunes int) (string, int, int) {
	total := utf8.RuneCountInString(data)
	if maxRunes < 0 || total <= maxRunes {
		return data, 0, total
	}
	runes := []rune(data)
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	at := -1
	for _, t := range terms {
		if i := indexRunes(lower, t); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	slack := maxRunes / 4
	start := 0
	if at > 0 {
		// Lead with roughly a third of context before the match.
		start = max(at-maxRunes/3, 0)
		if start+maxRunes > total {
			start = total - maxRunes
		}
		// Snap the start forward to a word boundary (never past the match).
		s := start
		for s > 0 && s < at && s-start < slack && !unicode.IsSpace(runes[s-1]) {
			s++
		}
		if s == 0 || s-start < slack || unicode.IsSpace(runes[s-1]) {
			start = s
		}
	}
	end := min(start+maxRunes, total)
	// Snap the end back to a word boundary, but not before the match.
	e := end
	for e < total && e > start+1 && end-e < slack && !unicode.IsSpace(runes[e]) && (at < 0 || e > at+1) {
		e--
	}
	if e == total || end-e < slack || unicode.IsSpace(runes[e]) {
		end = e
	}
	return strings.TrimSpace(string(runes[start:end])), start, total
}

// foldTerms lowercases snippet terms once per request into rune slices.
func foldTerms(terms []string) [][]rune {
	out := make([][]rune, 0, len(terms))
	for _, t := range terms {
		out = append(out, []rune(t))
	}
	return out
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
