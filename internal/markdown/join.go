package markdown

import "strings"

// Join is the inverse of Split for the canonical case: it reinserts
// a single blank line between every block. Round-trip is idempotent
// — Split(Join(blocks)) yields back the same blocks (empty entries
// are dropped; whitespace inside a block is preserved).
func Join(blocks []string) string {
	if len(blocks) == 0 {
		return ""
	}
	nonEmpty := blocks[:0:0]
	for _, b := range blocks {
		if b == "" {
			continue
		}
		nonEmpty = append(nonEmpty, b)
	}
	if len(nonEmpty) == 0 {
		return ""
	}
	return strings.Join(nonEmpty, "\n\n")
}
