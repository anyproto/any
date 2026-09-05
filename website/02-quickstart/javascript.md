---
title: JavaScript
description: A minimal fetch-based client — create a space, create an object, query, and read the SSE subscription from a streaming POST — with no dependencies.
order: 40
---
# JavaScript

Everything is `fetch`. The one wrinkle is live reads: the subscribe endpoints are POSTs (the filter body does not fit a query string), so the browser's `EventSource` does not apply — you read the response body as a stream and split SSE frames yourself. Below is the whole loop in ~60 lines, runnable in Node 18+ or a browser.

```js
const API = "http://127.0.0.1:7001/v1";

async function call(method, path, body) {
  const res = await fetch(API + path, {
    method,
    headers: body ? { "content-type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const { error } = await res.json();            // uniform shape: {error:{code,message,details?}}
    throw new Error(`${res.status} ${error.code}: ${error.message}`);
  }
  return res.status === 204 ? null : res.json();
}
```

## 1. Create a space

```js
const space = await call("POST", "/spaces", { name: "Notebook" });
const SPACE = space.id;                              // "bafyreig…"
```

## 2. Create a page type, then an object

A document is an object carrying a type whose part declares the `editor` module — the built-in `page` for a plain body, or a document type of your own (registered as a bundle so every device agrees on one).

```js
const { typeId: PAGE } = await call("POST", `/spaces/${SPACE}/types`,
  { name: "Page", xKey: "page", weight: 10, layout: { type: "page" } });
await call("POST", `/spaces/${SPACE}/types/${PAGE}/parts`,
  { key: "body", datasets: [{ module: "editor", shared: true }] });

const { objectId } = await call("POST", `/spaces/${SPACE}/objects`, {
  types: [PAGE],
  initialProperties: { any: { name: "Reading list" } },
});
```

## 3. Query

```js
const page = await call("POST", `/spaces/${SPACE}/objects/query`, {
  filter: { "any.types": PAGE },
  sort: ["-modifiedAt"],
  limit: 20,
  includeTotal: true,
});
console.log(page.total, page.records.map(r => r.any.name));
// timestamps arrive as {"$date": "…"}:
const modified = new Date(page.records[0].modifiedAt.$date);
```

## 4. Subscribe

One POST returns `text/event-stream`. Parse it frame by frame: frames are separated by a blank line, each has `event:` and `data:` lines, and `: keepalive` comments can be ignored.

```js
async function* sse(path, body, signal) {
  const res = await fetch(API + path, {
    method: "POST", signal,
    headers: { "content-type": "application/json", accept: "text/event-stream" },
    body: JSON.stringify(body),
  });
  if (!res.ok) { const { error } = await res.json(); throw new Error(error.code); }
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += value;
    let i;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const frame = buf.slice(0, i); buf = buf.slice(i + 2);
      let event = "message", data = "";
      for (const line of frame.split("\n")) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("data:")) data += line.slice(5).trim();
      }
      if (data) yield { event, data: JSON.parse(data) };
    }
  }
}

const ctl = new AbortController();
const window = new Map();                            // id → record: hold a window, not a database

for await (const { event, data } of sse(`/spaces/${SPACE}/objects/query/subscribe`,
    { filter: { "any.types": PAGE }, sort: ["-modifiedAt"], limit: 20 }, ctl.signal)) {
  if (event === "ready") continue;                   // stream is live after this
  if (event === "snapshot") { for (const r of data.records) window.set(r.id, r); render(); }
  if (event === "changes") {
    for (const ch of data) {
      for (const r of ch.added)   window.set(r.id, r.doc);
      for (const r of ch.updated) window.set(r.id, r.doc);
      for (const r of ch.removed) window.delete(r.id);
    }
    render();
  }
  if (event === "closed") break;                     // terminal: reopen for a fresh snapshot
}
```

Rename the object from another tab or with `curl` and watch an `updated` entry arrive:

```js
await call("POST", `/spaces/${SPACE}/properties/${objectId}/set/any`, { name: "Reading list 2026" });
```

## Recovery

`closed` carries a reason — `server_shutdown`, `sdk_closed`, `overflow` (you drained too slowly), `drifted` (too much of the window left). All four mean the same thing: open a new POST and replace your window with the new `snapshot`. There is no replay and nothing to reconcile ([Subscriptions](../realtime/subscribe.html)).

## Writing and reading back

Writes return `{versionId, changeId, recordIds}` and never the record. Read it back through a query, or let the open subscription deliver it — stamp `versionId` on what you wrote if you need to recognise your own change on the stream ([Best practices](../understanding/best-practices.html)).

```js
const r = await call("POST", `/spaces/${SPACE}/objects/${chatId}/chat/messages`, { text: "hello" });
r.recordIds[0];                                     // the new message id
```

> **Note.** In a browser the server's CORS allowlist covers the desktop-shell webview origins and the Vite dev origins; a page served from another origin will be blocked by the browser even though the server is on loopback. Serve your dev page from Vite, or proxy `/v1` through your dev server ([Security model](../operations/security-model.html)).

Next: [Python](python.html), or on to [Reading data](../database/reading-data.html).
