## Tool Description

Turn a meeting transcript into a **structured, sourced enrichment proposal** against a space. It reads the transcript's blocks, synthesizes discrete knowledge units (each citing the source block ids it came from), grounds each against the space's existing objects (cheap semantic search on a clean concept-level probe), reconciles them into `enrich | new | conflict | redundant`, and **persists a draft `enrich_proposal` object** — one `enrich_proposal_items` record per item `{text, source, outcome, targetObjectId, targetKind, targetProperty, value, newType, newName}`. NOTHING is written to target objects.

This is stage 1 of meeting enrichment. It does the heavy, mechanical work (reading the whole transcript, grounding, first-pass reconcile) so it never floods your context — it hands you back a compact handle, not the full map. Stage 2 — clustering facets of one topic, deduping against memory, choosing enrich-vs-create and the exact `targetProperty`/`value`, then applying — is YOUR job: follow the **meeting-enrich** skill. Call this first, then read and EDIT the proposal's items (consolidate), then apply via `enrichApply` after the user approves.

Expect ~30–90s and real token use (two LLM passes over the transcript). Read the items back with `anyHelper.getObjects({objectId: proposalId, dataset: "enrich_proposal_items", space})`.

## Tool Schema

### propose(transcriptId, opts) [mutator]

Generate and persist a draft enrichment proposal for one transcript.

**Input:**
- `transcriptId` (string, required) — the transcript object's id (its `editor_blocks` are read for block-cited provenance).
- `opts` (object, optional):
  - `opts.space` (string) — the space holding the transcript and the objects to reconcile against. Defaults to your own space; pass the target space id for cross-space (the usual case).
  - `opts.limit` (number, default 6) — grounding candidates per unit.

**Output:** `{ ok, proposalId, proposalLink, space, transcriptId, items, tally }` on success, or `{ ok:false, error }`. `items` is the number of records written; `tally` is the per-outcome count. The full item set lives in the proposal object's `enrich_proposal_items` dataset.

```js
var p = meetingEnrich.propose("bafyrei…transcript", { space: "bafyrei…devspace" });
p.tally   // { enrich: 18, new: 6 }
p.proposalId   // read/edit its enrich_proposal_items, then enrichApply.apply(p.proposalId, {space})
```
