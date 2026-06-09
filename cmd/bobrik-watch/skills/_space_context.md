## Space Context

The system prompt embeds a singleton **Main Space Context** object — a bird's-eye README of this space, describing *structure and conventions*. Every new conversation reads it. When Main grows past a soft ceiling it's split into child context files; their titles + ids appear in the **Child Context Files** section and you fetch their content on demand.

Main is **NOT a database.** Per-object state changes often and belongs in the objects themselves, looked up at the moment of need. Main also tends to go stale — verify non-meta facts (types, objects, counts) at lookup rather than trusting Main. Support the space's meta-structure and goal, not its inventory.

### What belongs

- Types and their key properties
- Naming, tag, and platform conventions
- Active tools and mini apps (names only, no source)
- Domain workflows and pipelines
- Standing rules and decisions ("prefer X over Y", "never auto-overwrite Z")
- Pointers to child context files

### What does NOT belong

- Per-object IDs, ratings, statuses, progress — read them at query time with `getObjects` (describe it first; it takes a filter/sort query, not just a type)
- Full property lists — use `describeType` (describe it first)
- Ephemeral state, drafts, in-progress notes — those ride chat-history compression

### Entry style: rules, not inventory

A good entry stays true after the user adds five more objects of that kind tomorrow. If it would need updating just because the space grew, it's inventory — don't write it.

- Avoid: "~10 movies tracked; Neuromancer is the only finished book"
- Prefer: "On create, check for name duplicates; describe conventions, not counts"

When migrating existing Main content, apply the same test: replace tallies and named-object asides with the rule they hint at, or delete and trust the lookup.

### How to edit

Main's id is in the `[Main](any://spaceId/objectId)` link at the top of the section. Pick the smallest tool:

- **`editObject(mainId, { oldString, newString })`** — surgical change. The default. `oldString` must match once (pass `replaceAll: true` to relax).
- **`appendToObject(mainId, "\n## New section\n…")`** — wholly new section.
- **`updateObject(mainId, { markdown: … })`** — full replace. Only when intentionally restructuring.

After editing, don't re-output Main — the next turn's prompt reflects the change. When a rule changes, keep both forms at a high level: `RULE: bookmarks use category tags (was: emoji prefix until 2026-04)` — but skip history for trivial enumerations.

### Child context files

Each child is a focused subsection of the broader context — a workflow, a security model, a billing flow. If the current topic matches a title, fetch first:

```
var ctx = anyHelper.getObject(childId);
```

Edit children with the same primitives as Main.

### Don't

- **Delete** space-context files — the post-turn split is the only thing that may merge or discard them.
- **Duplicate** facts across Main and a child — link from Main, or lift into Main if it belongs at the top.
- **Summarize** — space context is source of truth, not a summary.
- **Enumerate objects** — Main is a nav map, not a catalogue.
