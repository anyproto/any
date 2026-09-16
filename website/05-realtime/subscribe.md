---
title: Subscribe
description: The windowed query/subscribe primitive — one POST returns a snapshot and then streams added/updated/removed deltas until the stream closes.
order: 10
---
# Subscribe

A subscription sends the current results of a [query](../database/reading-data.html), then updates them as records change. Its **window** is the set selected by your filter, sort, and limit. The server maintains which records belong in that window.

For a client, the core loop is small:

1. Replace the local window with `snapshot.records`.
2. Store the full `doc` for each added or updated record; remove IDs in `removed`.
3. Render the retained records in the query's sort order.
4. On reconnect, replace the window with the new snapshot.

A `Map` keyed by record ID handles membership. It does not maintain query order when a sort key changes; your renderer must sort the retained records.

## Endpoints

| Scope | Endpoint | Records |
|---|---|---|
| Cross-object | `POST /v1/spaces/:spaceId/objects/query/subscribe` | one row per object in the space (the `objects` storage collection) |
| Per-object dataset | `POST /v1/spaces/:spaceId/query/subscribe` | rows of one dataset on one object (`chat_messages`, `editor_blocks`, a runtime dataset…) |
| Space list | `POST /v1/spaces/query/subscribe` | the account's spaces — see [Live space list](space-list.html) |
| Files | `POST /v1/spaces/:spaceId/objects/:objectId/files/query/subscribe` | an object's payload rows — see [Downloading](../files/downloading.html) |
| Devices | `POST /v1/devices/query/subscribe` | the account's devices — see [Devices](../auth/devices.html) |

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
| `filter`, `sort`, `limit`, `offset` | as in a snapshot query; `limit > 0` without `sort` is `400 request.invalid_field` — a live window has to be ordered |
| `includeTotal` | populates `total` and `hasNext` in the snapshot frame |
| `projection` | shapes the snapshot and every later `changes` record — see [Reading data](../database/reading-data.html) |
| `mailboxCapacity` | per-subscriber event mailbox; default 256, minimum 16 |
| `driftBudgetPercent` | how much of the window may leave unreplaced before the stream closes; default 30 |

`includeDeleted` is not supported on a subscription. The field set is closed: an unknown top-level key answers `400 request.unknown_field`. Errors raised before the stream opens (`space.not_found`, `request.missing_field`, …) come back as the normal JSON error envelope; once the response is `200 text/event-stream`, problems are `closed` frames.

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
3. **`changes`** — a JSON array of one or more events, each `{versionId, added?, updated?, removed?}` (an empty list is omitted). `added` and `updated` carry the full post-apply `doc` plus the `$set` / `$unset` `ops` of the triggering change. Most clients overwrite their local entry with `doc`; clients that want atomic field merges apply `ops` (`path: []` with an object payload is a multi-field set at the record root). `$inc`, `$addToSet` and `$pull` are never emitted — the engine collapses them to the merged `$set` before delivery, so non-Go clients never reimplement CRDT merge rules.
4. **`: keepalive`** — a comment every ~25 s while idle, defeating idle proxy timeouts.
5. **`closed`** — terminal.

`versionId` is the per-change DAG order on *this* peer. It is useful for fencing ("processed up to X") but is not comparable across devices.

### `removed` carries a reason

| Reason | Meaning | What to do |
|---|---|---|
| `deleted` | the record was tombstoned | drop it for good |
| `filtered-out` | an update made it stop matching `filter` | it still exists; drop from this view only |
| `displaced` | a sort-key change or a higher-ranked arrival pushed it past `limit` | it still exists, just outside the window |

Only `deleted` means the record or object is gone. The other reasons remove it from this view only: the same filtered, limited snapshot can still exclude it. A separate query by ID can establish whether it still exists.

An object deletion can produce a synthetic removal with an empty `versionId`. Apply the removal directly; do not discard it because a version comparison fails. Tombstone rows are not streamed: `includeDeleted` is snapshot-only and refused on `/subscribe` with `400 request.invalid_field`.

## Closing and recovery

| Reason | Cause |
|---|---|
| `server_shutdown` | the server received a signal (or, on a managed server, `POST /v1/shutdown`); in-flight streams emit this frame before the listener goes down (10 s deadline) |
| `deauthorized` | the account behind the stream was torn down in place (`DELETE /v1/auth`, or a switch to another account) while the server stays up — re-read `GET /v1/auth` before resubscribing |
| `sdk_closed` | the space or the engine was closed |
| `overflow` | events arrived faster than the client drained them and the mailbox (`mailboxCapacity`) filled; the engine closes the stream rather than drop events |
| `drifted` | more than `driftBudgetPercent` of the window left without replacements; the engine refuses to re-query on the hot path |

Recovery is **a new POST and a fresh snapshot**. Before every attempt, check `GET /v1/auth`: the account must be authorized and match the one the view belongs to. Otherwise stop and clear the old view. Make the same check after HTTP 401, a failed request, or an end of stream without `closed`; an account switch can interrupt delivery of the terminal frame.

There is no replay or resume cursor. Back off before retrying. Repeated `overflow` suggests the consumer cannot drain its mailbox quickly enough; inspect processing time or increase `mailboxCapacity`. Repeated `drifted` suggests widening `limit` or adjusting `driftBudgetPercent`.

> **Note.** Drift detection needs a window to measure against: with `limit: 0` there is no window auto-shift and no drift safety net. Always subscribe with a limit.

## Client example: fetch streaming, no EventSource

Windowed subscriptions use POST, so use streaming `fetch` rather than `EventSource`.

**Before you start:** run this in Node.js 18+ or an allowed desktop renderer. The server must be authorized. Replace `SPACE`, `CHAT`, and `ACCOUNT` with your space ID, the [general chat's root ID](../types/chat.html#finding-the-chat-object), and the expected account ID from `GET /v1/auth`. Ordinary web pages are subject to the server's CORS policy.

This example maintains the message window and logs changes in its size. Connect `windowChanged` to your renderer and sort the records by descending `_ver.id` for this query. Call `stop.abort()` when the view closes.

```js
const API = "http://127.0.0.1:7001/v1";
const SPACE = "<spaceId>", CHAT = "<chatId>", ACCOUNT = "<accountId>";
const stop = new AbortController();
let messages = new Map();

function windowChanged() {
  console.log(`${messages.size} messages in the current window`);
  // Render [...messages.values()] sorted by the query's sort keys.
}

async function requireOK(res) {
  if (res.ok) return res;
  const body = await res.json().catch(() => ({}));
  throw Object.assign(new Error(body.error?.code ?? `HTTP ${res.status}`),
    { status: res.status });
}

async function expectedAccount(signal) {
  const res = await fetch(`${API}/auth`, { signal });
  if (res.status === 401) return false;
  const auth = await (await requireOK(res)).json();
  return auth.authorized && auth.accountId === ACCOUNT;
}

function pause(ms, signal) {
  return new Promise((resolve, reject) => {
    signal.throwIfAborted();
    const aborted = () => { clearTimeout(timer); reject(signal.reason); };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", aborted);
      resolve();
    }, ms);
    signal.addEventListener("abort", aborted, { once: true });
  });
}

// Returns after `closed` or clean EOF. Both return to the same retry loop.
async function subscribe(url, body, onFrame, signal) {
  const res = await requireOK(await fetch(url, {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body), signal,
  }));
  if (!res.body) throw new Error("Subscription response has no body");
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      buf += value;
      let boundary;
      while ((boundary = /\r?\n\r?\n/.exec(buf))) {
        const frame = buf.slice(0, boundary.index);
        buf = buf.slice(boundary.index + boundary[0].length);
        let event = "message";
        const data = [];
        for (const line of frame.split(/\r?\n/)) {
          if (line.startsWith("event:")) event = line.slice(6).trim();
          if (line.startsWith("data:")) data.push(line.slice(5).replace(/^ /, ""));
          // Keepalive comments and fields this client does not use are ignored.
        }
        if (!data.length) continue;
        const payload = JSON.parse(data.join("\n"));
        if (event === "closed") return;
        onFrame(event, payload);
      }
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

function onFrame(event, data) {
  if (event === "snapshot") {
    messages = new Map((data.records ?? []).map(r => [r.id, r]));
  } else if (event === "changes") {
    for (const ev of data) {
      for (const r of ev.added ?? []) messages.set(r.id, r.doc);
      for (const r of ev.updated ?? []) messages.set(r.id, r.doc);
      for (const r of ev.removed ?? []) messages.delete(r.id);
    }
  } else return;
  windowChanged();
}

async function run(signal) {
  while (!signal.aborted) {
    try {
      if (!await expectedAccount(signal)) {
        messages.clear();
        windowChanged();
        return;                            // signed out or switched accounts
      }
      await subscribe(`${API}/spaces/${SPACE}/query/subscribe`,
        { objectId: CHAT, dataset: "chat_messages", sort: ["-_ver.id"], limit: 50 },
        onFrame, signal);
    } catch (e) {
      if (signal.aborted) return;
      // Retry transport/server failures and 401; a malformed request needs a fix.
      if (e.status >= 400 && e.status < 500 && e.status !== 401) throw e;
      console.warn("Subscription interrupted:", e.message);
    }
    await pause(1000, signal);
  }
}

run(stop.signal).catch(e => { if (!stop.signal.aborted) console.error(e); });
// When the view closes: stop.abort();
```

From the shell, `curl -N` shows the raw frames, and the CLI prints one JSON object per frame (`{"event": …, "data": …}`) so the stream pipes through `jq`. `--properties` opens the cross-object stream; an object id plus `--dataset` opens a per-object one:

```bash
curl -N http://127.0.0.1:7001/v1/spaces/SPACE/objects/query/subscribe \
  -H 'Content-Type: application/json' \
  -d '{"filter":{"any.type":"page"},"sort":["-modifiedAt"],"limit":20}'

any query-subscribe SPACE --properties --filter '{"any.type":"page"}' --sort -modifiedAt --limit 20
any query-subscribe SPACE CHAT --dataset chat_messages --sort -_ver.id --limit 50 --total \
  | jq 'select(.event=="changes") | .data[]'
```

## What to subscribe to

- **`objects`** (cross-object) — one row per object holding computed property values. Creates land as `added`, property writes as `updated`, deletes as `removed` with `reason: "deleted"` — the canonical place to observe deletion across a space.
- **`editor_blocks`** — the block tree of one document; every frame ships the full post-apply block, whether it came from a block write or a bulk markdown import. See [Editor](../types/editor.html).
- **`chat_messages`** — one message per `added`; a reaction toggle arrives as an `updated` event with a `$set` / `$unset` op under `reactions.<emoji>.<accountId>`. See [Chat](../types/chat.html).

Records ship their full stored form — `_ver` included (and `_traces` / `_deletedAt` when present) — unless you send a `projection`, which shapes the snapshot and every later `changes` record alike, so the stream cannot widen on you. Hold one subscription per open view and keep its limit close to what the UI needs. Both the subscription and the client retain window state.
