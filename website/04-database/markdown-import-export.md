---
title: Markdown import & export
description: Read a document as markdown, replace it wholesale, patch it by quoting text, or append — a lossless bridge over the editor's block records.
order: 130
---
# Markdown import & export

A document's body is a tree of atomic block records in an [editor](../types/editor.html) storage collection — `editor_blocks` for the shared body, `<typeId>_<key>` for a part with its own editor; the routes name it as `:collection`, the examples use `editor_blocks`. The markdown routes are a lossless bridge over that storage collection: `GET` renders the blocks to markdown, `PUT` parses markdown and diffs it against the current blocks, `PATCH` applies quoted-text replacements server-side, and `append` adds a fragment at the tail without reading the document. They exist for "Export as .md" / "Import .md" flows, LLM tooling, and any caller that doesn't want to walk the block tree.

```
GET   /v1/spaces/:spaceId/objects/:objectId/editor/editor_blocks/markdown          render blocks → markdown
PUT   /v1/spaces/:spaceId/objects/:objectId/editor/editor_blocks/markdown          parse markdown → diff → block ops
PATCH /v1/spaces/:spaceId/objects/:objectId/editor/editor_blocks/markdown          oldText → newText replacements
POST  /v1/spaces/:spaceId/objects/:objectId/editor/editor_blocks/markdown/append   append at tail, no read or diff
```

All four write through the same block write path a per-block edit would, so the same live events fire on that storage collection and other clients update in place. The object's type must declare the part that owns it (`page` declares `editor_blocks`) — a write otherwise is `400 dataset.not_declared` — and a `:collection` the space does not serve is `404 dataset.not_found`.

## Export — `GET`

```sh
curl http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ/editor/editor_blocks/markdown
# → {"content": "# Title\n\nFirst paragraph…"}
```

Returns `{"content": "<markdown>"}`: every top-level block rendered to its canonical markdown and joined with a blank line. Block `text` holds inline markdown only; block-level structure (headings, list items, checkboxes) comes from the block's `type` and `style`.

## Import — `PUT`

```sh
curl -X PUT http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ/editor/editor_blocks/markdown \
  -H 'Content-Type: application/json' \
  -d "$(jq -n --rawfile md doc.md '{content: $md}')"
# → {"inserted": ["blk_…"], "updated": [], "deleted": ["blk_…"], "unchanged": 12}
```

The server parses the markdown, diffs against the current block tree by type + position + text, and emits per-block create / update / delete ops. Unchanged blocks keep their ids. Re-`PUT`ting what `GET` returned writes nothing — `unchanged` equals the block count.

## Targeted edits — `PATCH`

For callers that know the *text* they want changed but not the block ids. Each `oldText` must already be in the document:

```sh
curl -X PATCH http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ/editor/editor_blocks/markdown -d '{
  "edits": [
    { "oldText": "- [ ] Children of Time", "newText": "- [x] Children of Time" },
    { "oldText": "typo", "newText": "fixed", "replaceAll": true }
  ] }'

any editor edit $SPACE $OBJ --old '- [ ] Children of Time' --new '- [x] Children of Time'
any editor edit $SPACE $OBJ --edits @edits.json
any editor edit $SPACE $OBJ --collection "${TYPE}_summary" --old 'draft' --new 'final'
```

The server renders the current canonical markdown (the exact bytes `GET` returns), resolves every edit against it, splices, and feeds the result through `PUT`'s diff. A checkbox tick therefore lands as a single `$set style.checked` on one block; ids and untouched blocks stay stable; the reply is `PUT`'s shape.

Matching rules:

- Every `oldText` matches against the original document, independently of the other edits; matched regions must not overlap.
- Without `replaceAll` the match must be unique. `newText` may be empty (deletes the text). Deleting a whole block takes one blank-line separator with it, so neighbours end up adjacent.
- Exact match first; on zero hits a whole-line fuzzy fallback folds unicode punctuation to ASCII (curly quotes, dashes, NBSP; NFKC) and ignores trailing whitespace. A mid-line fragment is never fuzzy-matched — re-`GET` and quote exactly.
- All-or-nothing: any failing edit rejects the whole request and nothing is written. A request whose result is byte-identical to the current document is a `200` no-op, but repeating an edit that already applied is not: the `oldText` is gone, so the retry answers `400 markdown.no_match`. After an uncertain retry, `GET` the document and check for `newText` before treating the edit as lost.

| Error | Meaning / recovery |
|---|---|
| `markdown.no_match` | `oldText` not in the current rendering — `GET` and quote the exact text (`details.editIndex`) |
| `markdown.ambiguous_match` | more than one occurrence without `replaceAll` — add context or set `replaceAll` (`details.editIndex`, `occurrences`) |
| `markdown.overlapping_edits` | two edits matched intersecting text — merge them (`details.editIndices`) |

PATCH matches text against the server's current document. A stale quote fails instead of replacing unrelated concurrent edits. The request size follows the size of the edit rather than the whole document.

## Append — `POST …/append`

```sh
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects/$OBJ/editor/editor_blocks/markdown/append \
  -d '{"content": "## Turn 12\n\nAgent replied with…"}'
# → {"inserted": ["blk_…", "blk_…"], "updated": [], "deleted": [], "unchanged": 0}
```

Parses the fragment, looks up only the tail position (one indexed query, never the existing block bodies), and creates the new blocks in a single change. Cost is O(appended content) regardless of document size — a run of N appends is O(N), where N `PUT`s would be O(N²). It is purely additive: no update or delete, it inserts no leading separator, and it happily creates a block identical to an existing one. Blank content is a `200` no-op. Use it for grow-by-append pages such as logs.

## Empty paragraphs

An empty paragraph is a real block — a `paragraph` record with `text: ""` — and blank lines are how the markdown routes carry it. `PUT` and `GET` apply the same rule, so they are exact inverses:

| Markdown | Blocks |
|---|---|
| `alpha\n\nbeta` | two blocks — one blank line is the separator |
| `alpha\n\n\nbeta` | two blocks with one empty paragraph between — every blank line beyond the separator is an empty paragraph |
| `\n\nalpha` | at either edge there is no separator, so every blank line is an empty paragraph |
| `alpha\n` | one block — a single trailing newline is a terminator, identical to `alpha` |
| `alpha\n\n` | one block plus a trailing empty paragraph |

> **Note.** This encoding is any's, not CommonMark's: every other markdown renderer collapses blank runs. Content that round-trips through an external tool or a paste loses its empty paragraphs unless that tool emits and parses blank runs the same way. `append` is the exception — blank lines wrapping a fragment are framing and dropped; empty paragraphs *between* the fragment's own blocks are kept.

## Reading blocks directly

The markdown routes are a transform, not a read path. Block records themselves are read through the per-object query surface with `dataset: "editor_blocks"`, sorted by `nav.pos`, and watched through [subscribe](../realtime/subscribe.html):

```sh
any query-subscribe $SPACE $OBJ --dataset editor_blocks --sort nav.pos --limit 500
```
