# ui

## Tool Description

Drive the user's any-ui window. Use these when the user asks you to take them somewhere in the UI — **open a space**, or **open a specific object/document in a space**. One HTTP call, no token burn. Fire-and-forget: the command reaches the user's open any-ui window if one is connected, and does nothing if no UI is listening.

The command names its **target** space/object directly, so you can open anything on the account regardless of which space you're working in. Pair with `getUIContext()` (what the user is currently looking at) when the user says "this" — e.g. "open the doc I'm looking at" → read the context, then `openObject(ctx.spaceId, ctx.objectId)`.

Each call returns `subscribers` — how many UI windows received it. `subscribers: 0` means nobody was listening (the user's UI isn't open or isn't connected); the call still succeeds. There is no delivery-confirmed ack beyond this count, and no navigation history — it's an at-most-once directive, not stored.

You usually want a real object/space id (from a search hit, `getUIContext`, or a `getObjects` result), not a name. Pass ids, not display names.

## Tool Schema

### openSpace(spaceId) [mutator]

Tell the any-ui window to navigate to a space.

**Input:**
- `spaceId` (string, required) — the id of the space to open.

**Output:** `{ok, subscribers}` — or `{ok: false, error, code}` on failure.
- `subscribers`: number of UI windows that received the command (0 = nobody listening).

```js
ui.openSpace("bafyreib...spaceid");   // → {ok: true, subscribers: 1}
```

### openObject(spaceId, objectId) [mutator]

Tell the any-ui window to open a specific object/document, switching space first if needed.

**Input:**
- `spaceId` (string, required) — the space that holds the object.
- `objectId` (string, required) — the id of the object/document to open.

**Output:** `{ok, subscribers}` — or `{ok: false, error, code}` on failure.

```js
// Open a known object.
ui.openObject("bafy...space", "bafy...obj");   // → {ok: true, subscribers: 1}

// "Open the thing I'm looking at" — resolve the user's current view first.
var ctx = anyHelper.getUIContext();
if (ctx && ctx.objectId) ui.openObject(ctx.spaceId, ctx.objectId);

// Open a search hit. A semsearch hit carries objectId but not a spaceId, so
// pass the space you searched (the agent's own space, or the space you passed
// as opts.space).
var ctx = anyHelper.getUIContext();
var r = semsearch.search("quarterly planning doc", {space: ctx.spaceId, scopes: ["basic"]});
var hit = r.hits[0];
if (hit) ui.openObject(ctx.spaceId, hit.objectId);
```
