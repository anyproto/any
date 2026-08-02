package api

// MarkdownEdit is one targeted replacement against an object's
// rendered markdown. oldText is matched against the current canonical
// rendering (the bytes GET .../editor/markdown returns) — exact match
// first, whole-line fuzzy fallback (unicode punctuation / trailing
// whitespace tolerant). Without replaceAll the match must be unique.
type MarkdownEdit struct {
	OldText    string `json:"oldText"`
	NewText    string `json:"newText"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
}

// MarkdownEditRequest is the body of PATCH .../editor/markdown. All
// edits match against the ORIGINAL document independently; matched
// regions must not overlap. All-or-nothing: any failing edit rejects
// the whole request and nothing is written.
type MarkdownEditRequest struct {
	Edits []MarkdownEdit `json:"edits"`
}

// Error code namespace for the markdown edit endpoint.
const (
	// ErrMarkdownNoMatch — an oldText was not found in the current
	// rendering (even fuzzily). Recovery: GET .../editor/markdown and
	// quote the exact text.
	ErrMarkdownNoMatch = "markdown.no_match"
	// ErrMarkdownAmbiguous — an oldText occurs more than once and
	// replaceAll is not set. Recovery: include more surrounding
	// context, or set replaceAll.
	ErrMarkdownAmbiguous = "markdown.ambiguous_match"
	// ErrMarkdownOverlap — two edits matched overlapping text.
	// Recovery: merge them into one edit.
	ErrMarkdownOverlap = "markdown.overlapping_edits"
)
