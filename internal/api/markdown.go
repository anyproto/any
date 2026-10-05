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

// MarkdownDocument is the response of GET .../editor/markdown: the
// rendered body and its version. Version is opaque and valid only
// against the server that issued it.
type MarkdownDocument struct {
	Content string `json:"content"`
	Version string `json:"version"`
}

// MarkdownSetRequest is the body of PUT .../editor/markdown. A
// non-empty IfVersion makes the write conditional on the document
// still being at that version (409 markdown.conflict otherwise).
type MarkdownSetRequest struct {
	Content   string `json:"content"`
	IfVersion string `json:"ifVersion,omitempty"`
}

// Error code namespace for the markdown endpoints.
const (
	ErrMarkdownNoMatch   = "markdown.no_match"          // 400 — oldText not found in the current rendering
	ErrMarkdownAmbiguous = "markdown.ambiguous_match"   // 400 — >1 occurrences without replaceAll
	ErrMarkdownOverlap   = "markdown.overlapping_edits" // 400 — two edits matched intersecting text
	ErrMarkdownConflict  = "markdown.conflict"          // 409 — the body changed after ifVersion (details: content, version)
)
