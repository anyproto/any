# meeting-enrich

Use this when the user asks to **enrich or ingest a meeting / transcript into a space** — turning what was discussed into structured, sourced enrichments of existing objects (and a few new ones), with provenance back to the source.

Enrichment is NOT markdown edits. It produces an **`enriched_data` collection** on target objects (sourced facts; for a property enrichment the real property is set AND the source recorded). The plan you review/apply is a typed **`enrich_proposal`** object whose `enrich_proposal_items` dataset holds one record per item. Three stages: `meetingEnrich.propose` drafts it → you consolidate the items → `enrichApply.apply` applies and deletes it. The draft is raw material; your consolidation is the value.

## Workflow

1. **Resolve inputs** — the transcript object id and the target space.
   - "this page / here / this space" → `anyHelper.getUIContext()` + the `[user's current view]` line.
   - If unclear, ask which transcript and which space.

2. **Draft (stage 1):**
   ```js
   var p = meetingEnrich.propose(transcriptId, { space: targetSpace });
   // → { proposalId, proposalLink, items, tally }  (a draft enrich_proposal; nothing applied)
   ```

3. **Read the draft items:**
   ```js
   var items = anyHelper.getObjects({ objectId: p.proposalId, dataset: "enrich_proposal_items", space: targetSpace });
   ```
   Each item: `{ id, text, source, outcome, targetObjectId, targetKind, targetProperty, value, newType, newName }`.

4. **CONSOLIDATE by EDITING the proposal's items — this is the whole job.**
   The user reviews the *proposal object*, not your chat message. So your consolidation
   MUST be written back into `enrich_proposal_items`. Do NOT just describe a consolidated
   plan in chat — a plan that lives only in a chat message is a FAILURE of this skill.
   The reliable way: decide your final consolidated set, then **replace the draft** —
   delete every raw item and write your consolidated items back:
   - **Cluster.** Many `new` items are facets of ONE topic → merge them into ONE item
     (combine their `text`; for an existing target set `outcome:"enrich"` +
     `targetObjectId`; or ONE new object). Do not leave 20 fragments.
   - **Re-ground.** Confirm targets with `semsearch.search(text, { space: targetSpace })`
     and your memory; avoid duplicates.
   - **targetKind per item:** `collection` (default — fact lives in `enriched_data`) or
     `property` (set a real field: `targetKind:"property"`,
     `targetProperty:"<typeXKey>.<propXKey>"` via `anyHelper.describeType(type,{space})`,
     `value` = the typed value). Use `property` only when a fact maps cleanly to a field.
   - **Types for new objects:** prefer existing types (`anyHelper.getTypes({space})`);
     propose a new type only if clearly missing, and ASK before creating types.
   - Write with `anyHelper.deleteRecord(p.proposalId, "enrich_proposal_items", itemId, {space})`
     to drop drafts and `anyHelper.setRecord(p.proposalId, "enrich_proposal_items", "", {field:value}, {space})`
     to add each consolidated item (`itemId:""` derives a new id). Re-read the items
     afterward to confirm the object now holds your consolidated set.

5. **HAND OFF the proposal object — do NOT paste the plan as a markdown table.**
   Post a SHORT chat message: one line of counts (e.g. "Consolidated to 6 items: enrich
   *organisation follow-up* (3), set 1 property, 2 new pages") + the `proposalLink`, and
   tell the user to **open it to review/edit interactively, then say "apply" when ready.**
   The proposal object is the review surface; your message is just a pointer. Wait for
   approval — do NOT apply before they agree.

6. **APPLY on approval (stage 3):**
   ```js
   var r = enrichApply.apply(p.proposalId, { space: targetSpace });
   // deterministic: creates new objects, sets properties, writes enriched_data, deletes the proposal
   ```
   Relay `r` — especially `r.failures` (non-empty still means the rest applied). Report what landed with `any://` links.

## Notes
- `source` on every item is the transcript provenance (`any://…#blockId,…`); it is preserved into `enriched_data` and survives the proposal's deletion.
- Cross-space is normal: transcript + objects live in the user's space, not yours — always pass `{ space: targetSpace }`.
- If `meetingEnrich.propose` returns `ok:false`, relay the error; don't fabricate a proposal.
- The proposal is ephemeral — `enrichApply` deletes it after applying. Don't rely on it afterward.
