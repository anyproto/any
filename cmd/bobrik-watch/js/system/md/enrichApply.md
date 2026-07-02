## Tool Description

Deterministically APPLY a reviewed `enrich_proposal` — no LLM, no judgment. It executes each `enrich_proposal_items` record and then deletes the proposal (proposals are ephemeral). Call this only AFTER the user has approved the consolidated proposal (stage 3 of the meeting-enrich flow).

Per item it: creates the target object for `new` items; for `property` items sets the REAL property value on the target (`updateObject`); and always writes an `enriched_data` record onto the target `{text, source, target, value}` — the durable, sourced collection that survives the proposal's deletion. For property items the enriched_data record also captures the set value + its property path so the UI can show the value was sourced.

Because it deletes the proposal on success, it is idempotent: a second apply reads no items and no-ops. It never edits markdown bodies — enrichment lives in the `enriched_data` collection (and, for property items, the real property).

## Tool Schema

### apply(proposalId, opts) [mutator]

Apply a reviewed proposal and delete it.

**Input:**
- `proposalId` (string, required) — the `enrich_proposal` object id.
- `opts` (object, optional):
  - `opts.space` (string) — the space holding the proposal and the target objects. Defaults to your own space; pass the target space id (the usual case).

**Output:** `{ ok, proposalId, created, propertiesSet, enrichedDataWritten, proposalDeleted, failures }`. `failures` is a list of per-item problems (a non-empty list still means the rest applied); relay it to the user.

```js
var r = enrichApply.apply("bafyrei…proposal", { space: "bafyrei…devspace" });
r.enrichedDataWritten   // 18
r.created               // 6
r.failures              // []
```
