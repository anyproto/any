---
title: Editor
description: Block-structured documents in an editor collection — atomic block writes, live subscriptions, and a lossless markdown bridge for imports, exports and LLM edits.
order: 20
---
# Editor

The `editor` module stores an object's body as a tree of atomic blocks: one CRDT record per block, ordered by a lexicographic position, nested by parent id. Two members editing different paragraphs merge cleanly; an offline edit lands as a per-block change when the device reconnects. On top of the block collection sits a markdown bridge, so tools that think in text — exporters, importers, LLM agents — never have to walk the tree.

An object holds an editor collection while it carries a type whose part declares the module ([modules](index.html)). Every editor route names the collection: `editor_blocks`, the canonical collection a shared part declares — the body every document type contributes to — or `<typeId>_<key>` for a part that wants an editor of its own (a meeting's `summary` next to its shared notes). A write into a collection none of the object's types declare is `400 dataset.not_declared`; a collection no editor part in the space declares is `404 dataset.not_found`. The examples below use `editor_blocks`.

## Blocks

One record per block in the object's editor collection:

```json
{
  "id":    "<auto-derived from changeId>",
  "_ver":  { "id": "<VersionId of last change>" },
  "type":  "heading",
  "style": { "level": 2 },
  "text":  "**bold** inline markdown",
  "nav":   { "parentId": "", "pos": "<lexid>" }
}
```

| Field | Meaning |
|-------|---------|
| `type` | Required, ≤ 64 bytes. Known values: `paragraph`, `heading`, `list_item`, `check_list_item`, `code`, `quote`, `divider`, `html`, `table`, `image`; any other non-empty string is accepted, so clients can add block kinds. |
| `style` | Open-ended object. Known keys: `level` (heading, 1–6), `ordered` (list_item), `checked` (check_list_item), `lang` (code). |
| `text` | **Inline** markdown only — bold, italic, inline code, links, strikethrough. ≤ 64 KiB per block. |
| `nav.parentId` | Parent block id; `""` for top-level. |
| `nav.pos` | Lexid ordering siblings. |

Block-level syntax — heading hashes, list bullets, code fences, quote prefixes — lives in `type` + `style`, not in `text`, so a client renders blocks structurally without re-parsing. An empty paragraph is a real block: a `paragraph` with `text: ""`.

## Reading and subscribing

Reads go through the generic per-object query with `dataset=editor_blocks`, sorted by position:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/query \
  -H 'Content-Type: application/json' \
  -d '{"objectId": "'$OBJ'", "dataset": "editor_blocks", "sort": ["nav.pos"]}'
```

Records come back in flat `nav.pos` order, not depth-first — a client that wants the tree groups children under each `nav.parentId`. An object with no body yet returns an empty `records` array. For a live document view use the same body against `…/query/subscribe`: `added` carries the full block as `doc` plus its per-field ops, `updated` the post-apply doc and the triggering ops, `removed` just the id. The same frames fire whether the change came from a block PATCH, a markdown PUT, or another member's device — see [subscribe](../realtime/subscribe.html).

## Writing blocks

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/spaces/:spaceId/objects/:objectId/editor/editor_blocks/blocks` | create one block |
| PATCH | `…/editor/editor_blocks/blocks/:blockId` | `$set` / `$unset` fields on one block |
| DELETE | `…/editor/editor_blocks/blocks/:blockId` | tombstone one block |

Every write returns `{versionId, changeId, recordIds}`; `recordIds[0]` on create is the server-allocated block id.

```bash
# create — nav.parentId defaults to "" (top-level), nav.pos to the next lexid past the parent's last child
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/editor/editor_blocks/blocks \
  -H 'Content-Type: application/json' \
  -d '{"type": "check_list_item", "style": {"checked": false}, "text": "buy milk"}'
# → 201 { "versionId": "…", "changeId": "…", "recordIds": ["<blockId>"] }

# patch — each set key is a dotted path applied as one $set, each unset entry one $unset
curl -X PATCH http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/editor/editor_blocks/blocks/$BLK \
  -H 'Content-Type: application/json' \
  -d '{"set": {"style.checked": true, "text": "buy oat milk"}, "unset": ["style.level"]}'

# delete
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/editor/editor_blocks/blocks/$BLK
```

CLI equivalents: `any editor blocks create $SP $OBJ --type paragraph --text "…" [--style JSON] [--parent ID] [--pos LEXID]`, `any editor blocks patch … --set JSON --unset PATH`, `any editor blocks delete …`. Every editor command takes `--collection` (default `editor_blocks`) to write a namespaced editor instead.

Patch semantics worth knowing:

- All ops in one PATCH land in a single change (one `versionId`). An empty patch is a no-op with an empty `versionId`.
- Dotted keys are field **paths**: `"style.level": 2` touches one sub-field; `"style": {"level": 2}` replaces the whole `style` object.
- Required fields (`type`, `nav.parentId`, `nav.pos`) cannot be unset — the handler rejects those ops while still applying the rest of the batch.
- Delete tombstones the record (sticky — the id cannot be re-created) and does **not** cascade to children; delete descendants explicitly or rewrite the body through the markdown PUT. An unknown block id is `404 blocks.not_found`.

> **Why it matters.** Because each field of each block is its own CRDT path, "tick this checkbox" is a single `$set style.checked` that merges with anyone else's edit to the same document — including a concurrent rename of the same block's text. No document-level lock, no last-writer-wins over the whole body.

## The markdown bridge

The `…/editor/editor_blocks/markdown` routes are a lossless import/export layer over the same dataset — the one aggregating exception to the rule that endpoints map 1:1 onto SDK methods, kept because export/import flows and LLM tooling depend on it.

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/v1/spaces/:spaceId/objects/:objectId/editor/editor_blocks/markdown` | render blocks as markdown |
| PUT | `…/editor/editor_blocks/markdown` | parse markdown, diff against the tree, write the delta |
| PATCH | `…/editor/editor_blocks/markdown` | targeted `oldText → newText` replacements |
| POST | `…/editor/editor_blocks/markdown/append` | append a fragment at the tail without reading the document |

**GET** reads every top-level block, renders each to its canonical bytes, joins them with `\n\n` and returns `{"content": "<markdown>"}`. **PUT** takes the same `{"content": …}` body, parses the markdown, diffs it against the current tree by (type + position + text), and emits per-block create / update / delete ops through the same write path a block PATCH uses — so the same subscribe events fire, untouched blocks keep their ids, and the reply lists what changed:

```bash
curl -X PUT http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/editor/editor_blocks/markdown \
  -H 'Content-Type: application/json' \
  -d "$(jq -Rs '{content: .}' notes.md)"
# → { "inserted": ["…"], "updated": ["…"], "deleted": [], "unchanged": 12 }
```

Re-PUTting a GET writes nothing (`unchanged` equals the block count), so a client that hydrates from GET never sees its own save come back reshaped. The wider import/export story is on [markdown import & export](../database/markdown-import-export.html).

### Surgical edits with PATCH

PATCH is for callers — LLM agents above all — that know the *text* they want changed but not the block ids:

```bash
curl -X PATCH http://127.0.0.1:7001/v1/spaces/$SP/objects/$OBJ/editor/editor_blocks/markdown \
  -H 'Content-Type: application/json' \
  -d '{"edits": [
        {"oldText": "- [ ] Children of Time", "newText": "- [x] Children of Time"},
        {"oldText": "typo", "newText": "fixed", "replaceAll": true}
      ]}'
```

```bash
any editor edit $SP $OBJ --old '- [ ] buy milk' --new '- [x] buy milk'
any editor edit $SP $OBJ --edits @edits.json
```

The server renders the current canonical markdown (exactly the bytes GET returns), resolves every edit against it, splices, and feeds the result through PUT's diff — so a checkbox tick lands as one `$set style.checked` on the matched block, and the reply is PUT's shape. Matching rules:

- Every `oldText` matches against the **original** document, independently of the other edits; matched regions must not overlap.
- Without `replaceAll` the match must be unique. `newText` may be empty; deleting a whole block takes one blank-line separator with it so the neighbours become adjacent.
- Exact match first; on zero hits a whole-line fuzzy fallback folds unicode punctuation to ASCII (curly quotes, dashes, NBSP) and ignores trailing whitespace. A mid-line fragment is never fuzzy-matched — re-GET and quote exactly.
- All-or-nothing: any failing edit rejects the whole request and nothing is written. Byte-identical results are a 200 no-op, so ticking an already-ticked box is idempotent.

| Error code | Meaning / recovery |
|------------|--------------------|
| `markdown.no_match` | `edits[i].oldText` is not in the current rendering — GET and quote the exact text (`details.editIndex`) |
| `markdown.ambiguous_match` | occurs more than once without `replaceAll` — add context or set `replaceAll` (`details.editIndex`, `details.occurrences`) |
| `markdown.overlapping_edits` | two edits matched intersecting text — merge them (`details.editIndices`) |

Because the match runs server-side against current state, a stale quote fails loudly instead of silently reverting someone else's concurrent edit elsewhere in the document — and the caller ships O(edit) bytes instead of O(document).

### Append

`POST …/editor/editor_blocks/markdown/append` with `{"content": "…"}` is the append-only fast path: it parses the fragment, looks up only the tail position (one indexed query, never the existing block bodies), and creates the new blocks in one batch. Cost is O(appended content) regardless of document size, which makes a run of N appends O(N) rather than the O(N²) of repeated PUTs — the right tool for grow-by-append pages such as logs. It is purely additive (it will happily create a block identical to an existing one), inserts no leading separator, and answers PUT's shape with only `inserted` populated. Blank content is a 200 no-op.

## Empty paragraphs and blank lines

Blank lines are how the markdown routes carry empty paragraphs, and PUT (parse) and GET (render) apply the same rule so they are exact inverses:

- Between two content blocks, one blank line is the separator; **every blank line beyond it is one empty paragraph**. `alpha\n\nbeta` is two blocks; `alpha\n\n\nbeta` is two blocks with an empty paragraph between.
- At either edge there is no separator to spend, so every leading or trailing blank line is an empty paragraph — except that a single trailing newline is a terminator, not content (`alpha\n` reads back as `alpha`).
- Append is the exception: blank lines wrapping the fragment are framing and are dropped; empty paragraphs between the fragment's own blocks are kept.

> **Note.** This encoding is any's, not CommonMark's: every other markdown renderer collapses blank runs. Content that round-trips through an external tool loses its empty paragraphs unless that tool emits and parses blank runs the same way.
