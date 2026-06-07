## Tool Description

`any` API client for managing objects, types, properties, datasets, and collections in an `any` space.

**Properties are namespaced per type.** An object can carry more than one type, and each type has its own property bag. Dotted paths are `"<typeXKey>.<propXKey>"` — both segments are **xKeys**, the stable programmatic keys, NOT display names:

- **type xKey** — a stable slug. `createType` derives it from the name (`"Agent Memory"` → `agent_memory`, `"Mini App"` → `mini_app`) and returns it as `result.type.xKey`; builtins use their id (`program`, `nav`). It does NOT change when the display name is renamed — so hardcoded paths survive renames.
- **prop xKey** — the `key` you pass to `createType` properties (`vector`, `title`).

- **Writing**: properties go in as nested type groups, mirroring the read shape — `createObject("book", { name: "Dune", book: { author: "Frank Herbert", year: 1965 } })`. Every top-level data key that isn't a reserved field (`name`, `body`, `markdown`, `types`, `space`) must be a type xKey/id with a `{ prop: value }` map; anything else fails with a clear error — misplaced/typo'd keys are NEVER silently dropped. The `typeKey` argument of createObject/getObjects/etc. is the type **xKey** or id — NOT the display name (name is display-only metadata). Unknown property / wrong value-kind writes also fail loud (client + server validation).
- **Reading**: records come back nested keyed by xKey — `obj["movie"].title`, `obj["agent_memory"].chat_id`. Use `getProp(obj, "agent_memory.chat_id")`. Builtin namespaces stay literal: `obj.name` (display name), `obj.id`, `obj.any.types` (the object's type ids), `obj.nav.parentId`, `obj.program.name`. Dotted `"type.prop"` paths are for READS (getProp) and query filter/sort keys — writes always use the nested group shape.

Structured, non-property content lives in **datasets** (e.g. `mini_app`, `program_source`); read with `getObjects({ objectId, dataset })`, write with `setRecord` / `deleteRecord`.

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
- space: "user" (default) or "system"
- filter: mongo-style. Cross-object keys are dotted xKey paths ("agent_memory.tags",
  "movie.year") resolved to ids; dataset keys are literal fields ("nav.pos", "_ver.id").
  Operators: $eq (bare value), $ne, $gt, $gte, $lt, $lte, $in, $nin, $all, $exists,
  $regex, $and, $or, $not. Against an ARRAY property a scalar means "contains" and
  {$in:[...]} means "intersects" — how to filter by a tag/category array server-side.
- sort: array of dotted paths, "-" prefix = descending (e.g. ["-movie.year"]).
  limit / offset / includeTotal.
Full guide: docs/09-query.md. (Note: negation filters like $ne also match
objects lacking the field — cross-object getObjects scopes by type so that's safe.)

### getObject(objId, opts?) [getter]
Fetch one object by ID. Returns the object with properties nested per type
(`obj["<typeXKey>"].prop`, read via `getProp(obj, "<typeXKey>.prop")`) plus `markdown` body.
- objId: object ID
- opts.space: "user" or "system"
- opts.from, opts.to: line range for markdown slicing

### createObject(typeKey, data) [mutator]
Create a new object. Properties are nested type groups (write what you read):
```js
createObject("book", { name: "Dune", book: { author: "Frank Herbert", year: 1965 } })
```
- typeKey: type xKey (e.g. "pages", "agent_memory") or id; builtins use their id ("chat", "program"). NOT the display name.
- data.types: optional array of ADDITIONAL type xKeys/ids (multitype object)
- data.name: display name
- data.body: markdown content
- data.<typeXKey>: `{ prop: value }` property group for that type. One group per type — a multitype object takes several. Any other top-level key errors.

### updateObject(objId, data) [mutator]
Update an existing object. Property groups use the same nested shape as
createObject (`{ book: { rating: 9 } }`). Property/name write failures
(including server validation — unknown property, wrong kind) are returned
as `{ok:false, error}`.
- objId: object ID
- data.name — display name
- data.body / data.markdown — full markdown body (use appendToObject to append cheaply)
- data.<typeXKey> — `{ prop: value }` group; several groups update multiple types in one call

### deleteObject(objId) [mutator]
Delete an object.
- objId: object ID

### editObject(objId, opts) [mutator]
Surgical string replacement on markdown body.
- objId: object ID
- opts.oldString, opts.newString, opts.replaceAll

### appendToObject(objId, text) [mutator]
Append text to markdown body.
- objId: object ID
- text: text to append

### getTypes(opts?) [getter]
List all types in the space.

### createType(opts) [mutator]
Create a type with properties. Idempotent AND additive — if the type already
exists, only the missing properties are added (safe for multiple programs to
declare the same type with different properties).
- opts.name: type name (required)
- opts.xKey: stable programmatic key (optional; derived snake_case from name if omitted, e.g. "Agent Memory" → agent_memory)
- opts.properties: [{key, name?, format}] where format ∈ text | number | checkbox | objects (ref list) | object | array | date
Returns `{ ok, type: { id, name, xKey }, created }`. Use `type.xKey` as the
property-group key in writes and the namespace segment in dotted read/filter paths.

### setRecord(objId, dataset, recordId, fields, opts?) [mutator]
Upsert a dataset record, setting each field at its own path atomically (updating
one field never rewrites the others). `fields` = flat `{ field: value }` map.
(Read datasets with `getObjects({ objectId, dataset, ... })`.)

### deleteRecord(objId, dataset, recordIds, opts?) [mutator]
Tombstone one or more dataset records. recordIds is a single id or an array.

### describeType(typeKey) [getter]
Inspect a type: metadata, properties, sample object, object count.
- typeKey: type xKey (e.g. "pages", "agent_memory") or id; builtins use their id ("chat", "program"). NOT the display name.

### getProperties() [getter]
List all properties across all types.

### getSpaceMember(identityOrId) [getter]
Get a space member by identity or ID.

### listSpaceMembers() [getter]
List all space members.

### getCollectionObjects(collectionId) [getter]
List objects in a folder/collection.
- collectionId: folder object ID

### createCollection(name) [mutator]
Create a folder.
- name: folder name

### addToCollection(collectionId, objectIds) [mutator]
Move objects into a folder.
- collectionId: folder ID
- objectIds: single ID or array

### removeFromCollection(collectionId, objectId) [mutator]
Move object back to root.
