package api

// MarkdownEdit is one targeted replacement against an object's
// rendered markdown: oldText is matched against the bytes
// GET .../editor/markdown returns and must be unique unless
// replaceAll. See docs/03-api.md § Objects for the matching rules.
type MarkdownEdit struct {
	OldText    string `json:"oldText"`
	NewText    string `json:"newText"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
}

// MarkdownEditRequest is the body of PATCH .../editor/markdown. All
// edits match against the original document independently and must
// not overlap; any failing edit rejects the whole request.
type MarkdownEditRequest struct {
	Edits []MarkdownEdit `json:"edits"`
}

// Error code namespace for the markdown edit endpoint.
const (
	ErrMarkdownNoMatch   = "markdown.no_match"          // 400 — oldText not found in the current rendering
	ErrMarkdownAmbiguous = "markdown.ambiguous_match"   // 400 — >1 occurrences without replaceAll
	ErrMarkdownOverlap   = "markdown.overlapping_edits" // 400 — two edits matched intersecting text
)
