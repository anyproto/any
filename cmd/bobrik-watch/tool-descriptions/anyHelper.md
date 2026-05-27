Core API library for creating, reading, updating, and deleting objects, types, and programs.

## Tool Schema

### getObjects(typeKey, options?)
List objects of a type.
- typeKey: type name (e.g. "Pages", "Agent Memory") or built-in ID (e.g. "chat", "program")
- options.space: "user" (default) or "system"

### getObject(objId, opts?)
Fetch one object by ID. Returns properties + markdown body.
- objId: object ID
- opts.space: "user" or "system"
- opts.from, opts.to: line range for markdown slicing

### createObject(typeKey, data)
Create a new object.
- typeKey: type name (e.g. "Pages", "Agent Memory") or built-in ID (e.g. "chat", "program")
- data.name: display name
- data.body: markdown content
- data.properties: array of {key, text/number/checkbox} or object

### updateObject(objId, data)
Update an existing object.
- objId: object ID
- data.name, data.body/data.markdown, data.properties

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
Create a type with properties. Idempotent — skips if a type with the same name exists.
- opts.name: type name (required)
- opts.properties: [{key, format}]

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
