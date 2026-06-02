## Tool Description

`any` API client for managing objects, types, properties, datasets, and collections in an `any` space.

**Properties are namespaced per type.** An object can carry more than one type, and each type has its own property bag. The server stores properties under internal ids, but you always use readable names:

- **Writing**: property keys are dotted `"<Type>.<prop>"`, e.g. `"Movie.title"`, `"Agent Memory.chat_id"`. A bare key (`"title"`) is allowed only when it resolves unambiguously against the object's type(s); otherwise it errors — prefer dotted. Unknown property / wrong value-kind writes fail with a clear error (server-side validation), they are NOT silently dropped.
- **Reading**: records come back nested — `obj["Movie"].title`, `obj["Agent Memory"].chat_id`. Use `getProp(obj, "Movie.title")` to read a dotted path. Builtin namespaces stay literal: `obj.name` (display name), `obj.id`, `obj.any.types` (the object's type ids), `obj.nav.parentId`.

Structured, non-property content lives in **datasets** (e.g. `mini_app`, `program_source`); use `getRecord` / `setRecord` for those.

## Tool Schema

### getObjects(typeKey, options?)
List/query objects of a type. Returns normalized records (nested per type).
- typeKey: type name (e.g. "Pages", "Agent Memory") or built-in ID (e.g. "chat", "program")
- options.space: "user" (default) or "system"
- options.filter: mongo-style filter, keys as readable dotted paths
  ("Agent Memory.tags", "Film.year"). Operators: $eq (bare value), $ne, $gt,
  $gte, $lt, $lte, $in, $nin, $all, $exists, $regex, $and, $or, $not. Against
  an ARRAY property a scalar means "contains" and {$in:[...]} means
  "intersects" — this is how to filter by a tag/category array server-side.
- options.sort: array of dotted paths, "-" prefix = descending (e.g.
  ["-Film.year"]). options.limit / options.offset: paging.
Full guide: docs/09-query.md. (Note: negation filters like $ne also match
objects lacking the field — getObjects already scopes by type so that's safe.)

### getObject(objId, opts?)
Fetch one object by ID. Returns the object with properties nested per type
(`obj["Type"].prop`, read via `getProp(obj, "Type.prop")`) plus `markdown` body.
- objId: object ID
- opts.space: "user" or "system"
- opts.from, opts.to: line range for markdown slicing

### createObject(typeKey, data)
Create a new object.
- typeKey: primary type name (e.g. "Pages", "Agent Memory") or built-in ID (e.g. "chat", "program")
- data.types: optional array of ADDITIONAL type names/ids (multitype object)
- data.name: display name
- data.body: markdown content
- data.properties: dotted-key map `{ "Type.prop": value }`, or bare `{ prop: value }` (resolves against the object's type(s)), or legacy array `[{key, text/number/checkbox/objects/value}]`

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
- opts.properties: [{key, name?, format}] where format ∈ text | number | checkbox | objects (ref list) | object

### getRecord(objId, dataset, recordId?, opts?)
Read one record from an object's dataset (raw, not type-namespaced). Returns the
record or null. Datasets hold structured content (e.g. `mini_app`,
`program_source`). Omit recordId to get the first record.

### queryRecords(objId, dataset, query?, opts?)
Read all records of a dataset. `query` = {filter, sort, limit, offset, includeTotal}.
Returns {ok, records, total?}.

### setRecord(objId, dataset, recordId, fields, opts?)
Upsert a dataset record, setting each field at its own path atomically (updating
one field never rewrites the others). `fields` = flat `{ field: value }` map.

### deleteRecord(objId, dataset, recordIds, opts?)
Tombstone one or more dataset records. recordIds is a single id or an array.
Completes the dataset CRUD set with getRecord/queryRecords/setRecord.

### describeType(typeKey)
Inspect a type: metadata, properties, sample object, object count.
- typeKey: type name (e.g. "Pages", "Agent Memory") or built-in ID (e.g. "chat", "program")

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
