package markdown

import (
	"slices"
	"testing"
)

// TestMarkdownStyleKeysCoverParse pins markdownStyleKeys to what the
// parser produces: every style key ParseBlock sets for a block type is
// one an update owns for that type. A key the parser writes but the
// table misses would survive a type change, so the GET after a save
// would render something other than the saved body.
func TestMarkdownStyleKeysCoverParse(t *testing.T) {
	for _, raw := range []string{
		"# heading",
		"###### deep heading",
		"- bullet",
		"3. numbered",
		"- [x] done",
		"- [ ] todo",
		"```go\nfmt.Println()\n```",
		"plain paragraph",
		"> quote",
	} {
		p := ParseBlock(raw)
		for key := range p.Style {
			if !slices.Contains(markdownStyleKeys[p.Type], key) {
				t.Errorf("%q parses to %s with style key %q, which markdownStyleKeys does not own", raw, p.Type, key)
			}
		}
	}
}
