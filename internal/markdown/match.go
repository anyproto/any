package markdown

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Edit is one targeted replacement against the rendered markdown
// document. OldText is matched against the current canonical
// rendering (the exact bytes Get returns); the caller quotes what it
// wants changed and never addresses lines or block ids.
type Edit struct {
	// OldText is the text to replace. Must be non-empty and, unless
	// ReplaceAll is set, occur exactly once in the rendered document.
	OldText string
	// NewText replaces OldText. Empty deletes the matched text.
	NewText string
	// ReplaceAll replaces every occurrence instead of requiring a
	// unique match.
	ReplaceAll bool
}

// NoMatchError reports an edit whose OldText was not found, even by
// the fuzzy fallback. The right recovery is to re-read the document
// and quote the exact text.
type NoMatchError struct {
	Index int
}

func (e NoMatchError) Error() string {
	return fmt.Sprintf("edits[%d]: oldText not found in the document", e.Index)
}

// AmbiguousMatchError reports an edit whose OldText occurs more than
// once without ReplaceAll. The right recovery is to include more
// surrounding context, or set ReplaceAll.
type AmbiguousMatchError struct {
	Index       int
	Occurrences int
}

func (e AmbiguousMatchError) Error() string {
	return fmt.Sprintf("edits[%d]: oldText occurs %d times; must be unique", e.Index, e.Occurrences)
}

// OverlapError reports two edits whose matched regions intersect.
// The right recovery is to merge them into one edit.
type OverlapError struct {
	IndexA int
	IndexB int
}

func (e OverlapError) Error() string {
	return fmt.Sprintf("edits[%d] and edits[%d] match overlapping text", e.IndexA, e.IndexB)
}

// span is one resolved replacement region in the original content.
type span struct {
	start   int
	end     int
	newText string
	edit    int
}

// applyEdits resolves every edit against content and returns the
// edited content. All-or-nothing: any unresolvable edit returns one
// of the typed errors above and no partial result.
func applyEdits(content string, edits []Edit) (string, error) {
	spans, err := resolveEdits(content, edits)
	if err != nil {
		return "", err
	}
	// Splice back-to-front so earlier offsets stay valid.
	out := content
	for _, s := range slices.Backward(spans) {
		out = out[:s.start] + s.newText + out[s.end:]
	}
	return out, nil
}

// resolveEdits maps each edit to its byte span(s) in content. Every
// edit matches against the ORIGINAL content independently — not
// against the output of earlier edits — so resolved spans must not
// overlap. Returned spans are sorted by start offset.
func resolveEdits(content string, edits []Edit) ([]span, error) {
	var all []span
	for i, e := range edits {
		oldText := normalizeLF(e.OldText)
		if oldText == "" {
			return nil, fmt.Errorf("edits[%d]: oldText must not be empty", i)
		}
		matches := exactMatches(content, oldText)
		if len(matches) == 0 {
			matches = fuzzyMatches(content, oldText)
		}
		if len(matches) == 0 {
			return nil, NoMatchError{Index: i}
		}
		if len(matches) > 1 && !e.ReplaceAll {
			return nil, AmbiguousMatchError{Index: i, Occurrences: len(matches)}
		}
		newText := normalizeLF(e.NewText)
		for _, m := range matches {
			all = append(all, span{start: m[0], end: m[1], newText: newText, edit: i})
		}
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].start != all[b].start {
			return all[a].start < all[b].start
		}
		return all[a].end < all[b].end
	})
	for j := 1; j < len(all); j++ {
		if all[j-1].end > all[j].start {
			return nil, OverlapError{IndexA: all[j-1].edit, IndexB: all[j].edit}
		}
	}
	return all, nil
}

// exactMatches returns the [start, end) offsets of every
// non-overlapping occurrence of oldText in content.
func exactMatches(content, oldText string) [][2]int {
	var out [][2]int
	for from := 0; ; {
		i := strings.Index(content[from:], oldText)
		if i < 0 {
			return out
		}
		start := from + i
		out = append(out, [2]int{start, start + len(oldText)})
		from = start + len(oldText)
	}
}

// fuzzyMatches is the whole-line fallback for when the exact scan
// finds nothing: both sides are compared line-by-line in normalized
// space (NFKC, unicode quotes/dashes → ASCII, trailing whitespace
// stripped), and a hit spans the matched original lines whole. A
// mid-line quote that differs only in unicode punctuation is NOT
// rescued — partial-line matching would need a byte mapping through
// NFKC; re-reading the document and quoting exactly covers that case.
func fuzzyMatches(content, oldText string) [][2]int {
	if strings.TrimSpace(oldText) == "" {
		return nil // all-blank pattern would match every separator
	}
	lines := strings.Split(content, "\n")
	starts := make([]int, len(lines))
	for i, off := 0, 0; i < len(lines); i++ {
		starts[i] = off
		off += len(lines[i]) + 1
	}
	normContent := make([]string, len(lines))
	for i, l := range lines {
		normContent[i] = normalizeFuzzyLine(l)
	}
	oldLines := strings.Split(strings.TrimSuffix(oldText, "\n"), "\n")
	normOld := make([]string, len(oldLines))
	for i, l := range oldLines {
		normOld[i] = normalizeFuzzyLine(l)
	}
	var out [][2]int
	for i := 0; i+len(normOld) <= len(normContent); i++ {
		hit := true
		for j := range normOld {
			if normContent[i+j] != normOld[j] {
				hit = false
				break
			}
		}
		if !hit {
			continue
		}
		last := i + len(normOld) - 1
		out = append(out, [2]int{starts[i], starts[last] + len(lines[last])})
		i = last // resume after the match (loop adds 1)
	}
	return out
}

// fuzzyReplacer folds the unicode punctuation NFKC leaves alone —
// curly quotes, dash family, non-breaking space — onto ASCII.
var fuzzyReplacer = strings.NewReplacer(
	"‘", "'", "’", "'", "‚", "'", "‛", "'",
	"“", `"`, "”", `"`, "„", `"`,
	"‐", "-", "‑", "-", "‒", "-", "–", "-",
	"—", "-", "−", "-",
	" ", " ",
)

func normalizeFuzzyLine(s string) string {
	s = norm.NFKC.String(s)
	s = fuzzyReplacer.Replace(s)
	return strings.TrimRight(s, " \t")
}

// normalizeLF folds CRLF / CR line endings to LF. The canonical
// rendering is LF-only; callers pasting from other environments
// shouldn't miss on line endings alone.
func normalizeLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}
