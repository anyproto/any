package markdown

import "strings"

// Join is the inverse of Split: it renders block entries back to one
// canonical markdown document.
//
// Content blocks are separated by a single blank line. An empty entry
// is an empty paragraph and adds one blank line beyond that
// separator; at either edge of the document, where there is no
// separator to build on, it adds one blank line of its own — and a
// trailing run needs one extra newline to survive splitLines eating
// the terminator.
//
// Round-trip is idempotent — Split(Join(blocks)) yields back the same
// blocks (whitespace inside a block is preserved).
func Join(blocks []string) string {
	if len(blocks) == 0 {
		return ""
	}

	// Leading empties have no preceding block to hang off.
	first := 0
	for first < len(blocks) && blocks[first] == "" {
		first++
	}
	if first == len(blocks) {
		return strings.Repeat("\n", first)
	}

	var b strings.Builder
	b.WriteString(strings.Repeat("\n", first))
	b.WriteString(blocks[first])

	gap := 0
	for _, block := range blocks[first+1:] {
		if block == "" {
			gap++
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(strings.Repeat("\n", gap))
		b.WriteString(block)
		gap = 0
	}
	if gap > 0 {
		b.WriteString(strings.Repeat("\n", gap+1))
	}
	return b.String()
}
