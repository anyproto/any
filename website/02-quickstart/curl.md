---
title: curl
description: Create a space, create an object, query the space, and read a live subscription — four raw HTTP calls against the local server.
order: 20
---
# curl

Every any client is an HTTP client. This page is the whole loop in raw JSON, so you know exactly what the CLI and the language recipes are sending.

Assumes `any run` is up on `127.0.0.1:7001` ([Install](install.html)).

```bash
API=http://127.0.0.1:7001/v1
```

## 1. Create a space

```bash
curl -s -X POST $API/spaces -H 'content-type: application/json' \
  -d '{"name":"Notebook","description":"first space"}'
```

```json
{ "id": "bafyreig…", "spaceType": "any.space", "name": "Notebook",
  "description": "first space", "status": "active", "ownRole": "owner",
  "createdAt": "2026-08-24T10:00:00Z", "spaceIndexObjectId": "bafyreia…" }
```

Keep the id:

```bash
SPACE=bafyreig…
```

## 2. Create an object

A document is an object carrying a type whose part declares the `editor` module. The built-in [`page`](../types/page.html) type is the plain one — hidden from the picker, present in every space, no properties — so `"types": ["page"]` is all it takes; a client that needs columns declares its own document type, normally registered as a [bundle](../collaboration/bundles.html) so every device agrees on it. Properties always ride `initialProperties` keyed by type; the universal `any` type owns `name` / `description`.

```bash
curl -s -X POST $API/spaces/$SPACE/objects -H 'content-type: application/json' \
  -d '{"types":["page"],"initialProperties":{"any":{"name":"Reading list"}}}'
```

```json
{ "objectId": "bafyreib…" }
```

```bash
OBJ=bafyreib…
```

The two body keys `types`, `initialProperties` are the whole vocabulary; anything else is `400 request.unknown_field`. The object carries exactly the types it names — a place in the space's tree is one more type, the wiki usecase's ([Objects](../database/objects.html)).

## 3. Query

Reads are POSTs with a Mongo-style body. The cross-object query reads the space's `objects` collection — one row per object with its property values and derived stamps:

```bash
curl -s -X POST $API/spaces/$SPACE/objects/query -H 'content-type: application/json' \
  -d '{"filter":{"any.types":"page"},"sort":["-modifiedAt"],"limit":20,"includeTotal":true}'
```

```json
{ "records": [
    { "id": "bafyreib…",
      "any": { "types": ["page"], "name": "Reading list" },
      "author": "A8tR…", "spaceId": "bafyreig…",
      "createdAt": { "$date": "2026-08-24T10:01:00.000Z" },
      "modifiedAt": { "$date": "2026-08-24T10:01:00.000Z" },
      "modifiedBy": "A8tR…",
      "_ver": { "…": "…" } } ],
  "total": 1, "hasNext": false }
```

A scalar against an array field is the "contains" spelling (`{"any.types":"<typeId>"}`). Timestamps are `{"$date": …}` instants and must be written the same way in filters ([Reading data](../database/reading-data.html)).

## 4. Subscribe

Same body, sibling path, `-N` to keep the stream open. The response is `text/event-stream`:

```bash
curl -s -N -X POST $API/spaces/$SPACE/objects/query/subscribe -H 'content-type: application/json' \
  -d '{"filter":{"any.types":"page"},"sort":["-modifiedAt"],"limit":20}'
```

```
event: ready
data: {}

event: snapshot
data: {"records":[{"id":"bafyreib…", …}]}

: keepalive
```

Now, from another terminal, rename the object:

```bash
curl -s -X POST $API/spaces/$SPACE/properties/$OBJ/set/any \
  -H 'content-type: application/json' -d '{"patch":{"name":"Reading list 2026"}}'
```

The stream prints the delta:

```
event: changes
data: [{"versionId":"…",
        "updated":[{"id":"bafyreib…","doc":{…"name":"Reading list 2026"…},
                    "ops":[{"type":"$set","path":["any","name"],"payload":"Reading list 2026"}]}]}]
```

Wait for `ready`, integrate `snapshot`, then apply each `changes` batch to your window. A batch carries only the non-empty lists among `added` / `updated` / `removed`, and each `removed` entry names its `reason` (`deleted`, `filtered-out`, `displaced`). A terminal `event: closed` (`server_shutdown`, `deauthorized`, `sdk_closed`, `overflow`, `drifted`) means "open a fresh POST" — there is no replay ([Subscriptions](../realtime/subscribe.html)).

## A write's reply

Dataset writes never return the record — only the change:

```json
{ "versionId": "…", "changeId": "bafyreic…", "recordIds": ["bafyreib…"] }
```

Read it back with a query; that is the one read path for every dataset ([The zen of any](../understanding/zen-of-any.html)).

## Errors

Every non-2xx has one shape:

```bash
curl -s $API/spaces/nope
```

```json
{ "error": { "code": "space.not_found", "message": "space not found", "details": { "spaceId": "nope" } } }
```

`401 auth.required` means the server has no account booted yet — `any init` was skipped, or the data dir holds several accounts and none was selected. `POST $API/auth` with `{}` generates one in place ([Accounts](../auth/accounts.html)).

Next: the same four steps as [CLI](cli.html) commands, or in [JavaScript](javascript.html) / [Python](python.html).
