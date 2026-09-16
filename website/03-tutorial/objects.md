---
title: 1. Objects
description: The first level — create a plain page with a name and a description, read it back, subscribe to changes, rename it and delete it. No schema of your own.
order: 10
---
# 1. Objects

Create a page, give it a name, read it back, and watch a rename. You will then delete that example page. The later parts create their own objects.

**Prerequisites:** the running server and `API` / `SPACE` variables from the [tutorial setup](index.html#before-you-start). An object is a row in the space's `objects` storage collection. The built-in `page` type gives it a document body without a schema of your own.

## Create one

```bash
OBJ=$(curl -fsS "$API/spaces/$SPACE/objects" -H 'content-type: application/json' \
  -d '{"type": "page", "initialProperties": {"any": {"name": "Groceries", "description": "for Saturday"}}}' \
  | jq -er .objectId)
printf 'Object: %s\n' "$OBJ"
```

```json
{ "objectId": "bafyreib…" }
```

The create body has three keys: `type` — **required**, the one type the object is — `collections` (optional, what it is filed under) and `initialProperties` (starting values, grouped by owner). The group `any` is the **universal** property group every object has: `name`, `description`, `icon`, `tags`, plus `type` and `collections`. It is not something you set; it is the base every object starts from, and it is where the system keeps track of the type and the collections the object does have.

`page` is the built-in plain document — the right type when the thing is just a note. Every object has exactly one type and it is never cleared; [Part 2](properties.html) defines a type of your own, then files objects under a collection.

The command saved the returned ID in `OBJ`; later requests use it. The shortened ID shown in the response example is illustrative.

## Read it back

Reads are POSTs with a Mongo-style body against the space's `objects` storage collection:

```bash
curl -s -X POST $API/spaces/$SPACE/objects/query -H 'content-type: application/json' \
  -d '{"filter": {"id": "'$OBJ'"}}'
```

```json
{ "records": [ {
  "id": "bafyreib…",
  "any": { "type": "page", "name": "Groceries", "description": "for Saturday" },
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

Filters use the same grammar throughout the system — `{"any.name": "Groceries"}`, `{"createdAt": {"$gte": {"$date": "2026-09-01T00:00:00Z"}}}`, and from Part 2 on `{"any.type": "<typeId>"}` for the type and `{"any.collections": "<collectionId>"}` for membership in a collection (an array field matches on any element). The full grammar is in [Reading data](../database/reading-data.html).

## Watch it change

A query has a live form at the sibling `/subscribe` path. Before opening it, print the variables to copy into the terminal where you will rename the page:

```bash
printf 'API=%s\nSPACE=%s\nOBJ=%s\n' "$API" "$SPACE" "$OBJ"
```

Then start the stream in your current terminal:

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

Leave it open. In another terminal, paste the three assignments you printed, then rename the object with a write to the `any` group:

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

> **Why it matters.** The write above does not wait for a remote peer. It lands in the object's change log on this device, the local query sees it, and sync to other devices runs in the background whenever a peer is reachable ([Local-first](../understanding/local-first.html)). Nothing in this part changes when you are offline.

## What a write returns

Property and dataset writes return a change receipt rather than the record. Object creation returns `objectId`, as above; deletion returns no content. A property write returns:

```json
{ "versionId": "…", "changeId": "bafyreic…", "recordIds": ["bafyreib…"] }
```

`changeId` is the content address of the change — the handle [version history](../database/version-history.html) works with. Read the row back through the query, or let your open subscription deliver it.

## Delete

Use the terminal holding `API`, `SPACE`, and `OBJ`. If it is still streaming, stop that subscription with Ctrl-C first. This deletes only the page created in this part:

```bash
curl -s -X DELETE $API/spaces/$SPACE/objects/$OBJ     # → 204
```

The row disappears from every query, and open subscriptions see the id under `removed` with `"reason": "deleted"`.

## Where this level ends

You now have named, described pages that sync and stream. What you do not have is a *shape*: nothing says a grocery list has a store and a budget, and nothing lets you ask "every list for the store on Main Street". So far, you have used only the universal property group. The next part adds columns — a type with properties — and that is already enough for a password manager.

Next: [2. Properties](properties.html). Reference: [Objects](../database/objects.html).
