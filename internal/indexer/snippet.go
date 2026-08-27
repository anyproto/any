package indexer

import (
	"slices"
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
	// Stop words are dropped from the free-text query for the same reason
	// the lexical leg drops them: "the" occurs in the first line of almost
	// every record, so centering on it returns the head instead of the
	// passage. require terms are explicit constraints and stay.
	add(stripStopWords(q))
	for _, r := range require {
		add(r)
	}
	// Longest first: a selective term locates the passage, a short one
	// matches incidentally.
	slices.SortStableFunc(out, func(a, b string) int { return len(b) - len(a) })
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
	// Anchored first: a token-boundary match is the text the analyzer
	// actually scored. Only when no term matches that way does an
	// unanchored match stand in — better a window on the characters than
	// a default to the head (an unbroken alphanumeric run, a hash).
	at := -1
	for _, anchored := range []bool{true, false} {
		// terms arrive longest-first (snippetTerms): the most selective
		// one that occurs picks the window, so an incidental short term
		// earlier in the text does not drag it away from the passage.
		for _, t := range terms {
			if i := indexRunesAt(lower, t, anchored); i >= 0 {
				at = i
				break
			}
		}
		if at >= 0 {
			break
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
	// Trim inside the window, then move start past what was trimmed, so
	// the returned offset still locates the returned text in data.
	for start < end && unicode.IsSpace(runes[start]) {
		start++
	}
	for end > start && unicode.IsSpace(runes[end-1]) {
		end--
	}
	return string(runes[start:end]), start, total
}

// foldTerms lowercases snippet terms once per request into rune slices.
func foldTerms(terms []string) [][]rune {
	out := make([][]rune, 0, len(terms))
	for _, t := range terms {
		out = append(out, []rune(t))
	}
	return out
}

// indexRunesAt finds needle in hay. Anchored, the match must start at a
// token boundary — the analyzer matches tokens, so an unanchored
// substring ("one" inside "money") would point the window at text the
// engine never scored. A prefix term keeps its trailing boundary open,
// so only the leading edge is checked.
func indexRunesAt(hay, needle []rune, anchored bool) int {
	if len(needle) == 0 || len(needle) > len(hay) {
		return -1
	}
	wordRune := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
outer:
	for i := 0; i+len(needle) <= len(hay); i++ {
		if anchored && i > 0 && wordRune(hay[i-1]) && wordRune(needle[0]) {
			continue // mid-word start
		}
		for j := range needle {
			if hay[i+j] != needle[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}
