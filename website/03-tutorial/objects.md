---
title: 1. Objects
description: The first level — create an object with a name and a description, read it back, subscribe to changes, rename it and delete it. No types, no schema.
order: 10
---
# 1. Objects

An object is a row in the space's `objects` collection. In the simplest case it carries no type of your own at all: a name, a description and the stamps the server derives. That is enough for a notebook, and it is the level every later part builds on.

## Create one

```bash
curl -s -X POST $API/spaces/$SPACE/objects -H 'content-type: application/json' \
  -d '{"initialProperties": {"any": {"name": "Groceries", "description": "for Saturday"}}}'
```

```json
{ "objectId": "bafyreib…" }
```

The create body has two keys, both optional: `types` (which types the object carries — none here) and `initialProperties` (starting values, grouped by type). The group `any` is the **universal** property group every object has: `name`, `description`, `icon`, `tags` and `types`. It is not a type you attach; it is the base every object starts from, and it is where the system keeps track of which types the object does carry.

`types` is a list, and it stays one for the life of the object: an object can carry several types at once, gaining each one's columns side by side, and types can be attached and detached later. This part uses none; [Part 2](properties.html) attaches one, then a second.

Keep the id:

```bash
OBJ=bafyreib…
```

## Read it back

Reads are POSTs with a Mongo-style body against the space's objects collection:

```bash
curl -s -X POST $API/spaces/$SPACE/objects/query -H 'content-type: application/json' \
  -d '{"filter": {"id": "'$OBJ'"}}'
```

```json
{ "records": [ {
  "id": "bafyreib…",
  "any": { "name": "Groceries", "description": "for Saturday" },
  "author": "A8tR…", "spaceId": "bafyreig…",
  "createdAt":  { "$date": "2026-09-08T10:00:00.000Z" },
  "modifiedAt": { "$date": "2026-09-08T10:00:00.000Z" },
  "modifiedBy": "A8tR…",
  "_ver": { "…": "…" }
} ] }
```

`author`, `createdAt`, `modifiedAt` and `modifiedBy` are derived by the server and read-only. `modifiedAt` bumps on every synced write and `modifiedBy` names who signed it, so "recently edited" is `{"sort": ["-modifiedAt"]}` with no work on your side ([System fields](../database/system-fields.html)).

Make a few more objects, then list them newest first:

```bash
curl -s -X POST $API/spaces/$SPACE/objects/query -H 'content-type: application/json' \
  -d '{"sort": ["-modifiedAt"], "limit": 20, "includeTotal": true}'
```

Filters use the same grammar throughout the system — `{"any.name": "Groceries"}`, `{"createdAt": {"$gte": {"$date": "2026-09-01T00:00:00Z"}}}`, and from Part 2 on `{"any.types": "<typeId>"}` (an array field matches on any element). The full grammar is in [Reading data](../database/reading-data.html).

## Watch it change

Every query has a live twin. Same body, sibling path, and the response is a server-sent event stream:

```bash
curl -s -N -X POST $API/spaces/$SPACE/objects/query/subscribe -H 'content-type: application/json' \
  -d '{"sort": ["-modifiedAt"], "limit": 20}'
```

```
event: ready
data: {}

event: snapshot
data: {"records":[{"id":"bafyreib…", "any": {"name": "Groceries", …}}]}
```

Leave it open. From another terminal, rename the object — a property write on the `any` group:

```bash
curl -s -X POST $API/spaces/$SPACE/properties/$OBJ/set/any -H 'content-type: application/json' \
  -d '{"patch": {"name": "Groceries (Saturday)"}}'
```

The stream prints the delta:

```
event: changes
data: [{"versionId":"…",
        "updated":[{"id":"bafyreib…","doc":{…},
                    "ops":[{"type":"$set","path":["any","name"],"payload":"Groceries (Saturday)"}]}]}]
```

That is the whole reactive model: a snapshot, then `added` / `updated` / `removed` entries for as long as the connection is open — each batch carries only the lists that have something in them. Renames from your other devices, and from every member of the space, arrive on the same stream ([Subscribe](../realtime/subscribe.html)).

> **Why it matters.** The write above returned before any network traffic happened. It landed in the object's change log on this device, the query saw it immediately, and sync to other devices runs in the background whenever a peer is reachable ([Local-first](../understanding/local-first.html)). Nothing in this part changes when you are offline.

## What a write returns

No write echoes the record. Every one returns the same small receipt:

```json
{ "versionId": "…", "changeId": "bafyreic…", "recordIds": ["bafyreib…"] }
```

`changeId` is the content address of the change — the handle [version history](../database/version-history.html) works with. Read the row back through the query, or let your open subscription deliver it.

## Delete

```bash
curl -s -X DELETE $API/spaces/$SPACE/objects/$OBJ     # → 204
```

The row disappears from every query, and open subscriptions see the id under `removed` with `"reason": "deleted"`.

## Where this level ends

You now have named, described objects that sync and stream. What you do not have is a *shape*: nothing says a grocery list has a store and a budget, and nothing lets you ask "every list for the store on Main Street". A name and a description are all the columns there are. The next part adds columns — a type with properties — and that is already enough for a password manager.

Next: [2. Properties](properties.html). Reference: [Objects](../database/objects.html).
