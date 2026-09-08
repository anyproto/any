---
title: Subscribe
description: The windowed query/subscribe primitive — one POST returns a snapshot and then streams added/updated/removed deltas until the stream closes.
order: 10
---
# Subscribe

A subscription is a live version of a [query](../database/reading-data.html): the same filter, sort and limit, but instead of one response you get a snapshot followed by a stream of windowed deltas over Server-Sent Events. The engine holds the window for you — no client-side diffing, buffering, or version bookkeeping.

## Endpoints

| Scope | Endpoint | Records |
|---|---|---|
| Cross-object | `POST /v1/spaces/:spaceId/objects/query/subscribe` | one row per object in the space (the `objects` collection) |
| Per-object dataset | `POST /v1/spaces/:spaceId/query/subscribe` | rows of one dataset on one object (`chat_messages`, `editor_blocks`, a runtime dataset…) |
| Space list | `POST /v1/spaces/query/subscribe` | the account's spaces — see [Live space list](space-list.html) |

The body is the same one `…/query` takes, plus two stream-only knobs:

```json
{
  "objectId": "obj_abc",
  "dataset": "chat_messages",
  "filter": { "creator": "A5k…" },
  "sort": ["-_ver.id"],
  "limit": 50,
  "offset": 0,
  "includeTotal": true,
  "mailboxCapacity": 256,
  "driftBudgetPercent": 30
}
```

| Field | Notes |
|---|---|
| `objectId`, `dataset` | per-object variant only |
| `filter`, `sort`, `limit`, `offset` | as in a snapshot query; `sort` is required when `limit > 0` |
| `includeTotal` | populates `total` and `hasNext` in the snapshot frame |
| `mailboxCapacity` | per-subscriber event mailbox; default 256, minimum 16 |
| `driftBudgetPercent` | how much of the window may leave unreplaced before the stream closes; default 30 |

The field set is closed: an unknown top-level key answers `400 request.unknown_field`. Errors raised before the stream opens (`space.not_found`, `request.missing_field`, …) come back as the normal JSON error envelope; once the response is `200 text/event-stream`, problems are `closed` frames.

## Frames

```
event: ready
data: {}

event: snapshot
data: {"records":[{"id":"msg_1","text":"hello","_ver":{…}}],"total":17,"hasNext":true}

event: changes
data: [{"versionId":"!!%>",
        "added":  [{"id":"msg_2","doc":{…},"ops":[{"type":"$set","path":[],"payload":{"text":"hi"}}]}],
        "updated":[{"id":"msg_1","doc":{…},"ops":[{"type":"$set","path":["text"],"payload":"hello!"}]}],
        "removed":[{"id":"msg_0","reason":"displaced"}]}]

: keepalive

event: closed
data: {"reason":"overflow"}
```

1. **`ready`** — sent once the subscription is registered. Wait for it before treating the stream as live.
2. **`snapshot`** — sent once, right after `ready`. `records` is the materialized window; `total` / `hasNext` appear only with `includeTotal`. The snapshot and the first event sit at adjacent versions with nothing missed in between, so integrate the snapshot first, then apply changes in order.
3. **`changes`** — a JSON array of one or more events, each `{versionId, added, updated, removed}`. `added` and `updated` carry the full post-apply `doc` plus the `$set` / `$unset` `ops` of the triggering change. Most clients overwrite their local entry with `doc`; clients that want atomic field merges apply `ops` (`path: []` with an object payload is a multi-field set at the record root). `$inc`, `$addToSet` and `$pull` are never emitted — the engine collapses them to the merged `$set` before delivery, so non-Go clients never reimplement CRDT merge rules.
4. **`: keepalive`** — a comment every ~25 s while idle, defeating idle proxy timeouts.
5. **`closed`** — terminal.

`versionId` is the per-change DAG order on *this* peer. It is useful for fencing ("processed up to X") but is not comparable across devices.

### `removed` carries a reason

| Reason | Meaning | What to do |
|---|---|---|
| `deleted` | the record was tombstoned | drop it for good |
| `filtered-out` | an update made it stop matching `filter` | it still exists; drop from this view only |
| `displaced` | a sort-key change or a higher-ranked arrival pushed it past `limit` | it still exists, just outside the window |

Only `deleted` means the object is gone. For the other two, a fresh snapshot would return the record again.

## Closing and recovery

| Reason | Cause |
|---|---|
| `server_shutdown` | the server received a signal or `POST /v1/shutdown`; in-flight streams emit this frame before the listener goes down (10 s deadline) |
| `deauthorized` | the account behind the stream was torn down in place (`DELETE /v1/auth`, or a switch to another account) while the server stays up — re-read `GET /v1/auth` before resubscribing |
| `sdk_closed` | the space or the engine was closed |
| `overflow` | events arrived faster than the client drained them and the mailbox (`mailboxCapacity`) filled; the engine closes the stream rather than drop events |
| `drifted` | more than `driftBudgetPercent` of the window left without replacements; the engine refuses to re-query on the hot path |

Recovery is the same for all of them: **open a new POST and take the fresh snapshot** — after `deauthorized`, once `GET /v1/auth` shows the account you expect. There is no replay across reconnects and no resume cursor — the new snapshot already reflects current state, which is strictly cheaper than reconstructing it from a backlog. `overflow` and `drifted` are split only so you can log and back off sensibly; a burst of `overflow` on a hot collection is the hint to raise `mailboxCapacity`, a stream of `drifted` on a churny list is the hint to raise `driftBudgetPercent` or widen `limit`.

> **Note.** Drift detection needs a window to measure against: with `limit: 0` there is no window auto-shift and no drift safety net. Always subscribe with a limit.

## Client example: fetch streaming, no EventSource

Windowed subscribes are `POST`, so the browser's `EventSource` cannot open them. Parse the SSE frames from a streaming `fetch` body instead:

```js
async function subscribe(url, body, onFrame) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error((await res.json()).error.code);

  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;                       // connection dropped without `closed`
    buf += value;
    let i;
    while ((i = buf.indexOf("\n\n")) >= 0) {  // one frame per blank line
      const frame = buf.slice(0, i); buf = buf.slice(i + 2);
      let event = "message", data = "";
      for (const line of frame.split("\n")) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("data:")) data += line.slice(5).trim();
        // lines starting with ":" are keepalive comments — ignore
      }
      if (data) onFrame(event, JSON.parse(data));
    }
  }
}

const window = new Map();
function run() {
  subscribe("http://127.0.0.1:7001/v1/spaces/SPACE/query/subscribe",
    { objectId: "CHAT", dataset: "chat_messages", sort: ["-_ver.id"], limit: 50 },
    (event, data) => {
      if (event === "snapshot") for (const r of data.records) window.set(r.id, r);
      if (event === "changes") for (const ev of data) {
        for (const r of ev.added)   window.set(r.id, r.doc);
        for (const r of ev.updated) window.set(r.id, r.doc);
        for (const r of ev.removed) window.delete(r.id);
      }
      if (event === "closed") setTimeout(run, 500);   // every reason: resubscribe
    }).catch(() => setTimeout(run, 2000));
}
run();
```

From the shell, `curl -N` shows the raw frames, and the CLI prints one JSON object per frame (`{"event": …, "data": …}`) so the stream pipes through `jq`:

```bash
curl -N http://127.0.0.1:7001/v1/spaces/SPACE/objects/query/subscribe \
  -H 'Content-Type: application/json' \
  -d '{"filter":{"any.types":"page"},"sort":["-modifiedAt"],"limit":20}'

any query-subscribe SPACE --filter '{"any.types":"page"}' --sort -modifiedAt --limit 20
any query-subscribe SPACE CHAT --dataset chat_messages --sort -_ver.id --limit 50 --total \
  | jq 'select(.event=="changes") | .data[]'
```

## What to subscribe to

- **`objects`** (cross-object) — one row per object holding computed property values. Creates land as `added`, property writes as `updated`, deletes as `removed` with `reason: "deleted"` — the canonical place to observe deletion across a space.
- **`editor_blocks`** — the block tree of one document; every frame ships the full post-apply block, whether it came from a block write or a bulk markdown import. See [Editor](../types/editor.html).
- **`chat_messages`** — one message per `added`; a reaction toggle arrives as an `updated` event with a `$set` / `$unset` op under `reactions.<emoji>.<accountId>`. See [Chat](../types/chat.html).

Records ship their full stored form — `_ver` included (and `_traces` / `_deletedAt` when present) — unless you send a `projection`, which shapes the snapshot and every later `changes` record alike, so the stream cannot widen on you. Hold one subscription per open view and size the window for the UI — the server streams straight from the indexed store and is indifferent to window size; the memory cost is the client's.
