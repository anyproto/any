## Tool Description

`any` API client for managing objects, types, properties, datasets, and collections in an `any` space.

**Spaces.** You live in your own agent space — your chat, programs, skills, and memory are here — but you can read and write EVERY space on the account: almost every method takes a `space` option (`data.space` on createObject/updateObject, `opts.space` elsewhere) that accepts any space id; omitted = your own space. `listSpaces()` enumerates the spaces, `createSpace(name)` mints one, and `getUIContext()` tells you what space/object the user is currently looking at in the UI. **To act on "this page" / "what I'm looking at": take `getUIContext().spaceId` (and `.objectId`) and pass it as `space:` in your calls.** Type/property xKeys are per-space — a user type that exists in your space may not exist in another; resolve against the target space (e.g. `getTypes({space: id})`, `describeType(t, {space: id})`).

**An object is multitype, and every type is a scope.** One object can carry several types at once; each type owns a separate property bag (namespace) keyed by its xKey, so the same object reads as `obj["book"].author` AND `obj["nav"].pos`. The **builtin subsystems are scopes too** — `any` (display name + type list), `nav` (tree placement), `program`, `editor` — keyed by a literal name instead of an xKey. Named/typed *data* always lives under some scope; only a handful of **system primitives sit at the root**: `obj.id`, `obj._ver`, `obj.createdAt`, `obj.author`, `obj.spaceId`. The two that surprise people are `name` and `types` — those are NOT root, they live under `any` (`obj.any.name`, `obj.any.types`). Dotted paths are `"<scope>.<propXKey>"`; for user types both segments are **xKeys**, the stable programmatic keys, NOT display names:

- **type xKey** — a stable slug. `createType` derives it from the name (`"Agent Memory"` → `agent_memory`, `"Mini App"` → `mini_app`) and returns it as `result.type.xKey`; builtins use their id (`program`, `nav`). It does NOT change when the display name is renamed — so hardcoded paths survive renames.
- **prop xKey** — the `key` you pass to `createType` properties (`vector`, `title`).

- **Writing**: properties go in as nested type groups, mirroring the read shape — `createObject("book", { name: "Dune", book: { author: "Frank Herbert", year: 1965 } })`. Every top-level data key that isn't a reserved field (`name`, `body`, `markdown`, `types`, `space`) must be a type xKey/id with a `{ prop: value }` map; anything else fails with a clear error — misplaced/typo'd keys are NEVER silently dropped. The `type` argument of createObject/getObjects/etc. is the type **xKey** or id — NOT the display name (name is display-only metadata). Unknown property / wrong value-kind writes also fail loud (client + server validation).
- **Reading**: records come back nested keyed by xKey — `obj["movie"].title`, `obj["agent_memory"].chat_id`. Use `getProp(obj, "agent_memory.chat_id")`. Builtin scopes read the same way: **the display name lives at `obj.any.name`**, the type ids at `obj.any.types`, tree placement at `obj.nav.parentId` / `obj.nav.pos`, `obj.program.name`. For convenience `obj.name` is hoisted as a read-only **alias** of `obj.any.name` — but it's only an alias on the in-memory record: in **filter/sort dotted paths use the real scope path** (`"any.name"`, `"any.types"`), never bare `"name"`, because those hit the wire shape where no root `name` exists. Dotted `"scope.prop"` paths are for READS (getProp) and query filter/sort keys — writes always use the nested group shape. When unsure of an object's shape, inspect a sample (`inferSchema(getObjects(type)[0])`) rather than guessing field names.

Structured, non-property content lives in **datasets** (e.g. `mini_app`, `program_source`, and built-in ones like `chat_messages` / `editor_blocks`); read with `getObjects({ objectId, dataset })`, write with `setRecord` / `deleteRecord`. This is the **universal** read/write path — there are almost no per-type wrappers. To list objects of any type (built-in included), `getObjects("<type>")` — e.g. `getObjects("chat")` for the space's chats; to read a built-in's records, dataset mode — e.g. `getObjects({ objectId: chatId, dataset: "chat_messages", sort: ["_ver.id"] })` for chat history. The few bespoke write helpers (`sendChatMessage`, editor markdown) exist only where the server endpoint isn't a plain dataset op. See the `getObjects` getter and `setRecord` / `deleteRecord` setters below.

## Tool Schema

### getObjects(typeOrQuery) [getter]
The one query method. Returns the records **array directly** — iterate it as-is.
An empty array means "no matches"; it **throws** on a real failure (unknown
type — the error lists the available types; server error; bad arguments), so
failures surface instead of looking like an empty result. With `includeTotal`
the page-bounded total is attached as `arr.total`. The argument is polymorphic:
- **string** → the type xKey/id: `getObjects("agent_memory")` = all objects of that type.
- **object** → the full query:
  - cross-object: `getObjects({ type, filter, sort, limit, offset, includeTotal, space })` — records NORMALIZED (nested, readable `rec["xkey"].prop`).
  - per-object dataset: `getObjects({ objectId, dataset, filter, sort, limit, ... })` — records RAW (datasets aren't type-namespaced).

Fields:
- type: type xKey (e.g. "pages", "agent_memory") or id; builtins use their id ("chat", "program"). NOT the display name. Omit to query across all types.
- objectId + dataset: select per-object dataset mode.
- space: any space id (default: your own space). Use getUIContext().spaceId to query what the user is viewing.
- filter: mongo-style. Cross-object keys are dotted xKey paths ("agent_memory.tags",
  "movie.year") resolved to ids; dataset keys are literal fields ("nav.pos", "_ver.id").
  Operators: $eq (bare value), $ne, $gt, $gte, $lt, $lte, $in, $nin, $all, $exists,
  $regex, $and, $or, $not. Operators MUST carry the `$` prefix — a bare key like
  {eq: x} is NOT an error; mongo reads it as "field equals the literal document
  {eq:x}", which silently matches nothing, so an unexpected empty result usually
  means a dropped `$`. Against an ARRAY property a scalar means "contains" and
  {$in:[...]} means "intersects" — how to filter by a tag/category array server-side.
- sort: array of dotted paths, "-" prefix = descending (e.g. ["-movie.year"]).
  limit / offset / includeTotal.
Full guide: docs/09-query.md. (Note: negation filters like $ne also match
objects lacking the field — cross-object getObjects scopes by type so that's safe.)

### getObject(objId, opts?) [getter]
Fetch one object by ID. Returns the object with properties nested per type
(`obj["<typeXKey>"].prop`, read via `getProp(obj, "<typeXKey>.prop")`) plus `markdown` body.
When a page has no markdown of its own but carries `enriched_data` facts (the
meeting-enrich flow), the facts come back as `obj.enrichedData` (raw rows) and a
rendered "## Enriched facts" section on `markdown`/`body` — an empty-looking
body means the object truly has no content, not that you missed a dataset.
- objId: object ID
- opts.space: any space id (default: your own space)
- opts.from, opts.to: line range for markdown slicing

### createObject(type, data) [mutator]
Create a new object. Properties are nested type groups (write what you read):
```js
createObject("book", { name: "Dune", book: { author: "Frank Herbert", year: 1965 } })
```
- type: type xKey (e.g. "pages", "agent_memory") or id; builtins use their id ("chat", "program"). NOT the display name.
- data.types: optional array of ADDITIONAL type xKeys/ids (multitype object)
- data.name: display name
- data.body: markdown content
- data.space: any space id — create the object in that space (default: your own)
- data.<typeXKey>: `{ prop: value }` property group for that type. One group per type — a multitype object takes several. Any other top-level key errors.

### updateObject(objId, data) [mutator]
Update an existing object. Property groups use the same nested shape as
createObject (`{ book: { rating: 9 } }`). Property/name write failures
(including server validation — unknown property, wrong kind) are returned
as `{ok:false, error}`.
- objId: object ID
- data.name — display name
- data.body / data.markdown — full markdown body (use appendToObject to append cheaply)
- data.space — any space id (default: your own)
- data.<typeXKey> — `{ prop: value }` group; several groups update multiple types in one call

### deleteObject(objId, opts?) [mutator]
Delete an object.
- objId: object ID
- opts.space: any space id (default: your own)

### editObject(objId, opts) [mutator]
Surgical string replacement on markdown body.
- objId: object ID
- opts.oldString, opts.newString, opts.replaceAll
- opts.space: any space id (default: your own)

### appendToObject(objId, text, opts?) [mutator]
Append text to markdown body.
- objId: object ID
- text: text to append
- opts.space: any space id (default: your own)

### getTypes(opts?) [getter]
List all types in a space.
- opts.space: any space id (default: your own)

### createType(opts) [mutator]
Create a type with properties. Idempotent AND additive — if the type already
exists, only the missing properties are added (safe for multiple programs to
declare the same type with different properties).
- opts.name: type name (required)
- opts.xKey: stable programmatic key (optional; derived snake_case from name if omitted, e.g. "Agent Memory" → agent_memory)
- opts.properties: [{key, name?, format}] where format ∈ text | number | checkbox | objects (ref list) | object | array | date
- opts.space: any space id (default: your own)
Returns `{ ok, type: { id, name, xKey }, created }`. Use `type.xKey` as the
property-group key in writes and the namespace segment in dotted read/filter paths.

### setRecord(objId, dataset, recordId, fields, opts?) [mutator]
Upsert a dataset record, setting each field at its own path atomically (updating
one field never rewrites the others). `fields` = flat `{ field: value }` map.
(Read datasets with `getObjects({ objectId, dataset, ... })`.)
- opts.space: any space id (default: your own)

### deleteRecord(objId, dataset, recordIds, opts?) [mutator]
Tombstone one or more dataset records. recordIds is a single id or an array.
- opts.space: any space id (default: your own)

### sendChatMessage(chatObjectId, text, opts?) [mutator]
Post a message to a chat. `chatObjectId` is the id of a chat object — find chats
with `getObjects("chat")` (optionally `{space}`); read history with
`getObjects({ objectId: chatObjectId, dataset: "chat_messages", sort: ["_ver.id"] })`.
Returns `{ ok, messageId, versionId, changeId }`. The message is stamped
agent-authored — the `agent` group `{ name, done:true }` marks it as written by
you (a UI hint, not signature-verified), so a watching agent can tell your posts
from human ones. `name` defaults to `"bao (<your display name>)"`.
- opts.agentName: override the agent display name (default `"bao (<your name>)"`)
- opts.agent: pass `null` to post WITHOUT the agent group (relay a human message)
- opts.replyToMessageId: thread the message as a reply
- opts.attachments: `{ id: { type, link } }` map (create-only)
- opts.space: any space id (default: your own)

### describeType(type, opts?) [getter]
Inspect a type: metadata, properties, sample object, object count.
- type: type xKey (e.g. "pages", "agent_memory") or id; builtins use their id ("chat", "program"). NOT the display name.
- opts.space: any space id (default: your own)

### getProperties(opts?) [getter]
List all properties across all types.
- opts.space: any space id (default: your own)

### getSpaceMember(identityOrId, opts?) [getter]
Get a space member by identity or ID.
- opts.space: any space id (default: your own)

### listSpaceMembers(opts?) [getter]
List all space members.
- opts.space: any space id (default: your own)

### listSpaces() [getter]
List every space on the account as raw rows `{id, name, status, type, ...}`.
Only `status === "active"` spaces are live — the rest are deleted/joining/
leaving; skip them unless asked otherwise. Throws on server failure.

### createSpace(name, opts?) [mutator]
Create a new space. Returns `{ ok, id, space }`.
- name: display name (required)
- opts.description, opts.iconCid, opts.spaceType (spaceType is pinned at
  create — it can never be changed afterwards)
The new space starts empty: no user types. Create the types you need there
(`createType({..., space: id})`) before writing typed objects.

### getUIContext() [getter]
What the user is currently looking at in the UI. Returns
`{ spaceId, objectId, view, updatedAt }`, or `null` if the UI has never
reported a view. `objectId` is `""` on views with no open object (space
home, grids); `updatedAt` is a client ms timestamp — a very old value means
the pointer may be stale. **The standard move for "this" / "this page" /
"summarize what I'm reading": `var ctx = getUIContext();` then pass
`ctx.spaceId` as `space:` (and use `ctx.objectId`) in the follow-up calls.**
Re-call it mid-task if the user may have navigated since.

### getCollectionObjects(collectionId, opts?) [getter]
List objects in a folder/collection.
- collectionId: folder object ID
- opts.space: any space id (default: your own)

### createCollection(name, opts?) [mutator]
Create a folder.
- name: folder name
- opts.space: any space id (default: your own)

### addToCollection(collectionId, objectIds, opts?) [mutator]
Move objects into a folder.
- collectionId: folder ID
- objectIds: single ID or array
- opts.space: any space id (default: your own)

### removeFromCollection(collectionId, objectId, opts?) [mutator]
Move object back to root.
- opts.space: any space id (default: your own)
