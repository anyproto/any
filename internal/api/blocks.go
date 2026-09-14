package api

import "encoding/json"

// Editor block writes (create / patch / delete) return the shared
// api.ModifyResult — {versionId, changeId, recordIds} — like every
// other dataset write. recordIds[0] is the new block id on create
// (server-derived) and the target id on patch / delete. The block
// record is read back through POST /query (or live via
// /query/subscribe) with dataset=editor_blocks; there is no curated
// per-block wire struct. See docs/03-api.md § Blocks.

// BlockNav is the per-record sibling-ordering namespace. parentId
// references another block's id; pos is a lexid that sorts siblings.
type BlockNav struct {
	ParentId string `json:"parentId"`
	Pos      string `json:"pos"`
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
	Set   map[string]json.RawMessage `json:"set,omitempty" swaggertype:"object"`
	Unset []string                   `json:"unset,omitempty"`
}

// Error code namespace for block endpoints.
const (
	ErrBlockNotFound    = "blocks.not_found"
	ErrBlockTypeMissing = "blocks.type_required"
	ErrBlockRejected    = "blocks.rejected"
)
