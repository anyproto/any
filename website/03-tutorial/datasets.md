---
title: 3. Datasets
description: The third level — a dataset is a table inside one object; when to use it instead of many objects, and how it holds thousands of records with an enforced schema, idempotent import, paging, search and aggregation. Built here as a mailbox of 10 000 emails.
order: 30
---
# 3. Datasets

A dataset is a table inside one object. Where properties give an object a handful of columns, a dataset gives it rows: thousands of records with a schema every peer enforces, ids you control, and the same query, subscribe, search and aggregation surface as everything else. This part builds a mailbox — one object holding ten thousand emails.

## Many objects, or many records?

You already know one way to hold many things: [Part 2](properties.html) stored each credential as its own object, one row each in the space-wide `objects` collection, all carrying the same type. A dataset is the other way: **one** object, and the many things as records in a table that object owns.

```
 many objects, one type                    one object, one dataset
 ───────────────────────                   ───────────────────────
 objects                                   objects
 ├─ GitHub      (credential)               └─ Inbox   (mailbox)
 ├─ Bank        (credential)                    └─ messages          ← the table
 ├─ Email       (credential)                         ├─ <msg-1>   subject, from, body, …
 └─ …                                                ├─ <msg-2>
                                                     └─ … 10 000 rows
```

Both are read, subscribed to and synced the same way. They differ in what a row *is*:

| | An object with properties | A record in a dataset |
|---|---|---|
| Lives in | the space-wide `objects` collection | a collection owned by one object |
| Query scope | the whole space — every object, any mix of types | that one object's records |
| Id | minted by the server | chosen by you (`idRule: user`) or derived from the change |
| Shape | the columns of every type it carries; can carry several | one schema, declared once |
| Rules | kind and format checks on values | required fields, write-once fields, server stamps, author-only delete |
| Cost of many | one change per object | one change per page of 500 records |
| Bulk import | one create per object | `/upsert` — safe to re-run, unchanged records skipped |

The rule of thumb: a thing that stands on its own — that you would open, show in a list next to unrelated things, or give more than one type — is an object. Things that belong to one object and are read together as one list — the messages of a mailbox, the rows of a ledger, the entries of a log — are records in a dataset on that object. The test that settles most cases is the query you will need: "every credential in the space" is an objects filter; "every unread message in this inbox" is a dataset query. A dataset never answers across objects, and the objects collection never gives you a schema-enforced table.

## Declare the dataset on a type

A dataset belongs to a **part** of a type — the display unit a client renders it in. Create a `mailbox` type, then a part with the dataset inline:

```bash
MAILBOX=$(curl -s -X POST $API/spaces/$SPACE/types -H 'content-type: application/json' \
  -d '{"name": "Mailbox", "xKey": "mailbox"}' | jq -r .typeId)

curl -s -X POST $API/spaces/$SPACE/types/$MAILBOX/parts -H 'content-type: application/json' -d '{
  "key": "messages", "name": "Messages", "ui": {"type": "table"},
  "datasets": [ {
    "key": "messages", "idRule": "user",
    "search": {"title": "subject", "text": ["from", "body"]},
    "fields": [
      {"key": "subject",    "kind": "string",   "required": true},
      {"key": "from",       "kind": "string",   "required": true},
      {"key": "body",       "kind": "string"},
      {"key": "receivedAt", "kind": "datetime", "required": true},
      {"key": "read",       "kind": "boolean",  "mutableBy": "any"},
      {"key": "labels",     "kind": "array",    "mutableBy": "any"},
      {"key": "importedAt", "stamp": "createTime"} ] } ] }'
# → 201 {"partId": "…"}
```

```bash
any type part add $SPACE $MAILBOX --draft @messages-part.json
```

What the declaration says:

| Piece | Meaning |
|-------|---------|
| `key: messages` | The dataset's slug inside the type. Its records live in the collection **`<typeId>_messages`** — namespaced to the type, so another type's `messages` never collides. |
| `idRule: user` | You supply record ids — here the mailbox's IMAP uid. Ids match `[A-Za-z0-9._:-]+` up to 128 bytes unless the declaration sets `idPattern` / `idMaxLen`. The alternative, `auto`, derives ids from the change. |
| `required` | Must be present on create; an email without a subject is rejected on every peer. |
| `mutableBy: any` | `read` and `labels` can be edited after creation. A field without it is **write-once** — subject, sender and body are immutable once imported. |
| `stamp: createTime` | Filled in by the server at apply; a client that sets it is rejected. |
| `search` | Which fields the search index extracts, and which is the title. |

The rules travel with the data: a second device, an offline peer and a member on another continent all reject the same malformed write. There is no server-side function to put validation in, and none is needed ([Runtime datasets](../database/runtime-datasets.html)).

## Create the mailbox

The object must carry the declaring type — no write attaches one for you:

```bash
INBOX=$(curl -s -X POST $API/spaces/$SPACE/objects -H 'content-type: application/json' \
  -d '{"types": ["'$MAILBOX'"], "initialProperties": {"any": {"name": "Inbox"}}}' | jq -r .objectId)
DS=${MAILBOX}_messages
```

## Import ten thousand emails

`/upsert` is the ingest path for a dataset with user ids. Every record is keyed by the id you give it: absent ids are created, present ones are diffed field by field, identical ones are skipped. Re-running the same batch writes nothing, so an import job can run on a schedule without ever duplicating a message. A request body is capped at 1 MB, so the importer sends the mailbox in batches that fit:

```bash
curl -s -X POST $API/spaces/$SPACE/upsert -H 'content-type: application/json' -d '{
  "objectId": "'$INBOX'", "dataset": "'$DS'", "pageSize": 500,
  "records": [
    {"id": "imap:INBOX:4201", "fields": {
       "subject": "Invoice 2026-09", "from": "billing@example.com",
       "body": "Please find attached…", "receivedAt": {"$date": "2026-09-08T09:12:00Z"},
       "read": false, "labels": ["finance"]}},
    {"id": "imap:INBOX:4202", "fields": {"…": "…"}}
  ] }'
```

```json
{ "pages": [ {"versionId": "…", "changeId": "…", "recordIds": ["imap:INBOX:4201", "…"]} ],
  "created": 500, "updated": 0, "skipped": 0 }
```

```bash
any upsert $SPACE $INBOX --dataset $DS --records @batch.json
```

Each call writes one CRDT change per `pageSize` records (500 by default), not one per email. A record that breaks the schema — no subject, a string where an instant belongs, a write to `importedAt` — comes back in `rejections` with a code and a reason while the rest of the batch lands; a clean batch has no `rejections` key ([Upsert](../database/upsert.html)). Mark a message read later with the ordinary record write:

```bash
curl -s -X POST $API/spaces/$SPACE/modify -H 'content-type: application/json' -d '{
  "objectId": "'$INBOX'", "dataset": "'$DS'",
  "records": [{"id": "imap:INBOX:4201",
               "ops": [{"type": "$set", "path": "read", "value": true}]}]}'
```

The same op against `subject` is refused: write-once.

## Read it like a mailbox

Dataset reads are the per-object query with the collection name. Newest first, one screen at a time:

```bash
curl -s -X POST $API/spaces/$SPACE/query -H 'content-type: application/json' -d '{
  "objectId": "'$INBOX'", "dataset": "'$DS'",
  "filter": {"read": false},
  "sort": ["-receivedAt"], "limit": 50, "offset": 0, "includeTotal": true}'
```

`…/query/subscribe` with the same body is the live inbox: a snapshot of the window, then `added` / `updated` / `removed` frames as messages arrive or get marked read — on this device or any other. A `limit` on a subscription requires a `sort`, because a live window has to be ordered ([Subscribe](../realtime/subscribe.html)).

> **Note.** A dataset is *per object*. "All unread mail across every mailbox in the space" is not one query — the space-wide collection is `objects` only. Model what you read together as one dataset on one object; a second mailbox is a second object with its own collection.

## Search it

The `search` mapping in the declaration put every message into the space's search index under its subject, sender and body. Search is a separate call, hybrid by default — full-text plus a semantic leg that joins whenever an embedder is available:

```bash
curl -s -X POST $API/spaces/$SPACE/search -H 'content-type: application/json' \
  -d '{"query": "invoice september", "limit": 10}'

any search $SPACE "invoice september"
```

A hit names the object, the dataset and the record id, so it leads straight back to a `/query` on the mailbox. The index lives in the data dir, follows every write after a short debounce, and needs no external service ([Search](../search/index.html)).

## Summarise it

Aggregation pipelines run over a dataset the same way queries do:

```bash
curl -s -X POST $API/spaces/$SPACE/aggregate -H 'content-type: application/json' -d '{
  "objectId": "'$INBOX'", "dataset": "'$DS'",
  "pipeline": [
    {"$match": {"read": false}},
    {"$group": {"_id": "$from", "count": {"$sum": 1}}},
    {"$sort": {"count": -1}}, {"$limit": 10}]}'
```

```json
{ "records": [ {"id": "billing@example.com", "count": 212}, … ] }
```

The group key comes back as `id`. Snapshot only — re-run to refresh ([Aggregation](../database/aggregation.html)).

## Where this level ends

You have a type whose objects carry a table: a declared, enforced schema; ids you own; an import safe to repeat; paging, liveness, search and aggregation. The mailbox works — for a client that already knows what a mailbox is.

That is the gap. The type says *messages* is a part with a `table` widget, but nothing yet says how a mailbox object should be laid out, what to show when it opens, whether it has a notes body, or where it appears in the space. And if you set the type up on two devices while they were apart, each minted its own `mailbox`. The last part closes both gaps: parts and modules give the object behaviour it inherits rather than implements, a bundle makes every device converge on one definition, and a `miniapp` marker puts it in the sidebar next to the apps the catalog installs.

Next: [4. Apps](apps.html). Reference: [Runtime datasets](../database/runtime-datasets.html), [Data model](../database/data-model.html).
