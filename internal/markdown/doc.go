// Package markdown is the lossless markdown import/export surface
// over the body_blocks dataset (internal/editor). It exists for LLM
// tools, "Export as .md" / "Import .md" UI flows, and programmatic
// API users that don't want to walk the block tree manually.
//
// The package contains:
//
//   - Split / Join: a CommonMark-ish block splitter and its inverse
//     joiner. Pure string→[]string transforms, no SDK access.
//   - ParseBlock / RenderBlock: the typed view — convert a raw
//     markdown block into the {type, style, text} shape stored on the
//     body_blocks dataset, and the inverse rendering. Inline-only
//     `text` (no block-level syntax inside).
//   - Set / Get / List: helpers that read existing top-level blocks
//     via the blocks package, diff against the supplied markdown, and
//     emit per-record create/update/delete ops on the body_blocks
//     dataset. PUT /v1/.../markdown is one HTTP wrapper around Set;
//     other clients can call it directly.
//
// All splitting, parsing, and diffing is caller-side — the SDK's
// per-record CRDT handler (in internal/editor) is what enforces the
// block-shape on apply.
package markdown
