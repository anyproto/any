---
title: Your first page with curl
description: Create a page, read it back, then watch a rename arrive through a live subscription.
order: 20
---
# Your first page with curl

Create a named page and watch it change. This is the core Any client flow: write through HTTP, read through a query, and subscribe when you need live updates.

**Before you start:** leave the server from [Install](install.html) running in its own terminal. Use a second terminal for this page, with `curl` and `jq` installed. Keep the variables below in that terminal.

## 1. Create a space

A space is a group of objects with its own membership and encryption keys.

```bash
API=http://127.0.0.1:7001/v1
curl -fsS "$API/auth" | jq -e '.authorized == true'
SPACE=$(curl -fsS "$API/spaces" -H 'content-type: application/json' \
  -d '{"name":"Notebook","description":"First Any example"}' | jq -er .id)
printf 'Space: %s\n' "$SPACE"
```

The first check prints `true`. The create request returns a space; `jq` saves its `id` in `SPACE`. If the account is unauthorized, finish [account setup](install.html#create-an-account) before continuing.

## 2. Create a page

Every object has exactly one type. `page` is the built-in document type. Its name lives in the universal `any` property group.

```bash
OBJ=$(curl -fsS "$API/spaces/$SPACE/objects" -H 'content-type: application/json' \
  -d '{"type":"page","initialProperties":{"any":{"name":"Reading list"}}}' \
  | jq -er .objectId)
printf 'Object: %s\n' "$OBJ"
```

The reply is `{"objectId":"…"}`. The create body accepts `type`, optional `collections`, and optional `initialProperties`. A collection is something you file the object under; it does not replace the object's type. Unknown body fields are rejected ([Objects](../database/objects.html)).

## 3. Read it back

A query returns the records that match right now:

```bash
curl -fsS "$API/spaces/$SPACE/objects/query" -H 'content-type: application/json' \
  -d '{"filter":{"any.type":"page"},"sort":["-modifiedAt","id"],"limit":20,"includeTotal":true}' \
  | jq '{names: [.records[].any.name], total, hasNext}'
```

For this new space, expect:

```json
{"names":["Reading list"],"total":1,"hasNext":false}
```

`any.type` is a scalar. `any.collections` is an array, where a scalar filter means “contains.” The sort puts recently modified pages first and uses the object ID to break timestamp ties. Timestamps in full rows use `{"$date":"…"}`; timestamp filters use that wrapper too ([Reading data](../database/reading-data.html)).

## 4. Watch a rename

First print a complete rename command. You will paste its output into another terminal, so that terminal needs no variables:

```bash
printf "curl -fsS '%s/spaces/%s/properties/%s/set/any' -H 'content-type: application/json' -d '%s'\n" \
  "$API" "$SPACE" "$OBJ" '{"patch":{"name":"Reading list 2026"}}'
```

Now open the subscription in the current terminal:

```bash
curl -fsSN "$API/spaces/$SPACE/objects/query/subscribe" -H 'content-type: application/json' \
  -d '{"filter":{"any.type":"page"},"sort":["-modifiedAt","id"],"limit":20}'
```

The connection stays open. It starts with `ready`, then a `snapshot` containing the page:

```text
event: ready
data: {}

event: snapshot
data: {"records":[{"id":"…","any":{"type":"page","name":"Reading list"},"…":"…"}]}
```

Paste the printed rename command into another terminal. The write returns a change receipt, and the subscription prints a `changes` event with your page under `updated`. Stop the subscription with Ctrl-C; this leaves the server running.

## Use the same flow in your app

Keep the latest window in your client:

1. Replace it with each `snapshot`.
2. Apply `added` and `updated` records, and drop `removed` IDs.
3. Sort the records before displaying them; a change can move a row.
4. Reopen after a terminal `closed` frame or a lost connection. There is no replay: the next snapshot is authoritative. Check the account before reconnecting after sign-out or a switch.

Dataset writes return `{versionId, changeId, recordIds}`, not the record. Object creation returns `objectId`; deletion returns no content. Read values through a query or receive them from the subscription ([Subscriptions](../realtime/subscribe.html)).

Errors use `{"error":{"code":"…","message":"…","details":{}}}`. `401 auth.required` means no account is booted. With recent curl, `--fail-with-body` preserves the error body while returning a nonzero exit code; the examples use `-f` for broader curl compatibility.

Next: **[JavaScript](javascript.html)** or **[Python](python.html)** for a complete client, or **[the tutorial](../tutorial/index.html)** to add your own properties and datasets.
