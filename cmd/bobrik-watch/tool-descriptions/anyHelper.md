## Tool Description

`any` API client for managing objects, types, properties, datasets, and collections in an `any` space.

**Properties are namespaced per type.** An object can carry more than one type, and each type has its own property bag. Dotted paths are `"<typeXKey>.<propXKey>"` — both segments are **xKeys**, the stable programmatic keys, NOT display names:

- **type xKey** — a stable slug. `createType` derives it from the name (`"Agent Memory"` → `agent_memory`, `"Mini App"` → `mini_app`) and returns it as `result.type.xKey`; builtins use their id (`program`, `nav`). It does NOT change when the display name is renamed — so hardcoded paths survive renames.
- **prop xKey** — the `key` you pass to `createType` properties (`vector`, `title`).

- **Writing**: property keys are dotted `"<typeXKey>.<prop>"`, e.g. `"movie.title"`, `"agent_memory.chat_id"`. A bare key (`"title"`) is allowed only when it resolves unambiguously against the object's type(s); otherwise it errors — prefer dotted. The `typeKey` argument of createObject/getObjects/etc. is the type **xKey** or id — NOT the display name (name is display-only metadata). Unknown property / wrong value-kind writes fail with a clear error (server-side validation), never silently dropped.
- **Reading**: records come back nested keyed by xKey — `obj["movie"].title`, `obj["agent_memory"].chat_id`. Use `getProp(obj, "agent_memory.chat_id")`. Builtin namespaces stay literal: `obj.name` (display name), `obj.id`, `obj.any.types` (the object's type ids), `obj.nav.parentId`, `obj.program.name`.

Structured, non-property content lives in **datasets** (e.g. `mini_app`, `program_source`); use `getRecord` / `setRecord` for those.

## Tool Schema

### getObjects(typeOrQuery)
The one query method. Returns `{ ok, records, total?, error }` (errors surfaced,
never a silent []). The argument is polymorphic:
- **string** → the type xKey/id: `getObjects("agent_memory")` = all objects of that type.
- **object** → the full query:
  - cross-object: `getObjects({ type, filter, sort, limit, offset, includeTotal, space })` — records NORMALIZED (nested, readable `rec["xkey"].prop`).
  - per-object dataset: `getObjects({ objectId, dataset, filter, sort, limit, ... })` — records RAW (datasets aren't type-namespaced). Subsumes the old queryRecords.

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

### getObject(objId, opts?)
Fetch one object by ID. Returns the object with properties nested per type
(`obj["<typeXKey>"].prop`, read via `getProp(obj, "<typeXKey>.prop")`) plus `markdown` body.
- objId: object ID
- opts.space: "user" or "system"
- opts.from, opts.to: line range for markdown slicing

### createObject(typeKey, data)
Create a new object.
- typeKey: type xKey (e.g. "pages", "agent_memory") or id; builtins use their id ("chat", "program"). NOT the display name.
- data.types: optional array of ADDITIONAL type xKeys/ids (multitype object)
- data.name: display name
- data.body: markdown content
- data.properties: dotted-key map `{ "typeXKey.prop": value }`, or bare `{ prop: value }` (resolves against the object's type(s)), or legacy array `[{key, text/number/checkbox/objects/value}]`

### updateObject(objId, data)
Update an existing object. Property/name write failures (including server
validation — unknown property, wrong kind) are returned as `{ok:false, error}`.
- objId: object ID
- data.name — display name
- data.body / data.markdown — full markdown body (use appendToObject to append cheaply)
- data.properties — same shapes as createObject; dotted keys can target multiple types in one call

### deleteObject(objId)
Delete an object.
- objId: object ID

### editObject(objId, opts)
Surgical string replacement on markdown body.
- objId: object ID
- opts.oldString, opts.newString, opts.replaceAll

### appendToObject(objId, text)
Append text to markdown body.
- objId: object ID
- text: text to append

### getTypes(opts?)
List all types in the space.

### createType(opts)
Create a type with properties. Idempotent AND additive — if the type already
exists, only the missing properties are added (safe for multiple programs to
declare the same type with different properties).
- opts.name: type name (required)
- opts.xKey: stable programmatic key (optional; derived snake_case from name if omitted, e.g. "Agent Memory" → agent_memory)
- opts.properties: [{key, name?, format}] where format ∈ text | number | checkbox | objects (ref list) | object | array | date
Returns `{ ok, type: { id, name, xKey }, created }`. Use `type.xKey` as the
stable namespace segment in dotted property paths.

### getRecord(objId, dataset, recordId?, opts?)
Read one record from an object's dataset (raw, not type-namespaced). Returns the
record or null. Datasets hold structured content (e.g. `mini_app`,
`program_source`). Omit recordId to get the first record. (For multiple records,
use getObjects dataset mode: `getObjects(null, {objectId, dataset, ...})`.)

### setRecord(objId, dataset, recordId, fields, opts?)
Upsert a dataset record, setting each field at its own path atomically (updating
one field never rewrites the others). `fields` = flat `{ field: value }` map.

### deleteRecord(objId, dataset, recordIds, opts?)
Tombstone one or more dataset records. recordIds is a single id or an array.
Completes the dataset CRUD set with getRecord/queryRecords/setRecord.

### describeType(typeKey)
Inspect a type: metadata, properties, sample object, object count.
- typeKey: type xKey (e.g. "pages", "agent_memory") or id; builtins use their id ("chat", "program"). NOT the display name.

### getProperties()
List all properties across all types.

### search(queries...)
Search objects by text (limited — no FTS indexer yet).

### getSpaceMember(identityOrId)
Get a space member by identity or ID.

### listSpaceMembers()
List all space members.

### getCollectionObjects(collectionId)
List objects in a folder/collection.
- collectionId: folder object ID

### createCollection(name)
Create a folder.
- name: folder name

### addToCollection(collectionId, objectIds)
Move objects into a folder.
- collectionId: folder ID
- objectIds: single ID or array

### removeFromCollection(collectionId, objectId)
Move object back to root.
