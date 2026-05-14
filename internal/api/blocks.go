package api

import "encoding/json"

// Block is the wire shape of one record in the editor_blocks dataset.
// Mirrors blocks.Block 1:1; the duplicate definition keeps
// internal/api self-contained for clients that import it without
// pulling internal/editor.
type Block struct {
	Id    string         `json:"id"`
	Ver   map[string]any `json:"_ver,omitempty"`
	Type  string         `json:"type"`
	Style map[string]any `json:"style,omitempty"`
	Text  string         `json:"text,omitempty"`
	Nav   BlockNav       `json:"nav"`
}

// BlockNav is the per-record sibling-ordering namespace. parentId
// references another block's id; pos is a lexid that sorts siblings.
type BlockNav struct {
	ParentId string `json:"parentId"`
	Pos      string `json:"pos"`
}

// BlockListResponse is the body of GET .../blocks. Blocks are
// returned in depth-first document order — top-level by nav.pos
// ascending, each block followed inline by its children.
type BlockListResponse struct {
	Records []Block `json:"records"`
}

// BlockCreateRequest is the body of POST .../blocks. `type` is
// required; everything else is optional with safe defaults.
//
// If `nav.pos` is empty the server allocates the next lexid after
// the current max for `nav.parentId`. If `nav.parentId` is omitted
// the new block is top-level.
type BlockCreateRequest struct {
	Type  string         `json:"type"`
	Style map[string]any `json:"style,omitempty"`
	Text  string         `json:"text,omitempty"`
	Nav   *BlockNav      `json:"nav,omitempty"`
}

// BlockPatchRequest is the body of PATCH .../blocks/:blockId.
//
//   - `set` maps a dotted field path to its new JSON value. Each
//     entry becomes one $set op against that path. Empty/omitted ==
//     no $set ops.
//   - `unset` is a list of dotted field paths to $unset. Empty/omitted
//     == no $unset ops.
//
// Both lists may be supplied together; the handler emits them as one
// atomic record-modify (single DAG change). Empty patch is a no-op
// that still returns the record's current _ver.
type BlockPatchRequest struct {
	Set   map[string]json.RawMessage `json:"set,omitempty"`
	Unset []string                   `json:"unset,omitempty"`
}

// BlockPatchResponse is the body of PATCH .../blocks/:blockId. Just
// the post-apply VersionId — clients dedup live events against it.
// Returned even for a no-op patch (then VersionId is the unchanged
// record's existing _ver.id).
type BlockPatchResponse struct {
	VersionId string `json:"versionId"`
}

// Error code namespace for block endpoints.
const (
	ErrBlockNotFound    = "blocks.not_found"
	ErrBlockTypeMissing = "blocks.type_required"
	ErrBlockRejected    = "blocks.rejected"
)
