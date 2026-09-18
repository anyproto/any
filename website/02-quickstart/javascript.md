---
title: JavaScript
description: A fetch-based client with no dependencies — create a space and an object, query, read the SSE subscription from a streaming POST, and keep an ordered window that survives a dropped stream.
order: 40
---
# JavaScript

Everything is `fetch`. The one wrinkle is live reads: the subscribe endpoints are POSTs (the filter body does not fit a query string), so the browser's `EventSource` does not apply — you read the response body as a stream and split SSE frames yourself. The blocks below form one file: create a space and a page, keep an ordered live window over the space, rename the page while the subscription is open, and reopen the stream when it drops.

<a href="../assets/examples/client.mjs" download>Download client.mjs</a>, or copy the `js` blocks in order; they run top to bottom as an ES module (top-level `await`) in Node 18+ (`node client.mjs`) or a browser (`<script type="module">`). Each run creates a new space on the server from [Install](install.html).

> **Note.** In a browser the server's CORS allowlist covers the desktop-shell webview origins and the Vite dev origins (`http://localhost:5173`, `http://127.0.0.1:5173`); a page served from another origin is blocked by the browser even though the server is on loopback. Serve your dev page from Vite, or proxy `/v1` through your dev server ([Security model](../operations/security-model.html)).

## 1. Make an HTTP call

All ordinary calls share one helper. Keep the HTTP status even if a proxy returns an HTML or empty error body, so the reconnect loop can still recognize a temporary server failure.

```js
const API = "http://127.0.0.1:7001/v1";

async function checked(response) {
  if (response.ok) return response;
  const error = (await response.json().catch(() => null))?.error;
  throw Object.assign(new Error(`${response.status}: ${error?.message ?? response.statusText}`),
    { status: response.status, code: error?.code });
}

async function call(method, path, body, signal) {
  const response = await checked(await fetch(API + path, {
    method, signal,
    headers: body === undefined ? {} : { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  }));
  return response.status === 204 ? null : response.json();
}

const account = await call("GET", "/auth");
if (!account.authorized) throw new Error("Create or select an account before running this example.");
```

## 2. Create and read a page

A document is an object whose type has a part declaring the `editor` module — the built-in `page` for a plain body, or a document type of your own (registered as a bundle so every device agrees on one). `type` is required; `collections` is optional; `name` and `description` live in the universal `any` property group.

```js
const { id: SPACE } = await call("POST", "/spaces", { name: "JavaScript notebook" });
const { objectId } = await call("POST", `/spaces/${SPACE}/objects`, {
  type: "page", initialProperties: { any: { name: "Reading list" } },
});
const query = { filter: { "any.type": "page" }, sort: ["-modifiedAt", "id"], limit: 20 };
const page = await call("POST", `/spaces/${SPACE}/objects/query`, { ...query, includeTotal: true });
console.log("Pages:", page.total, page.records.map(r => r.any.name));
```

Expect `Pages: 1 [ 'Reading list' ]`. The query uses an explicit ascending ID as the second sort key, so equal timestamps have the same order in the server and renderer.

## 3. Read the stream

One POST returns `text/event-stream`. Parse it frame by frame: frames are separated by a blank line (LF or CRLF), each has `event:` and `data:` lines, several `data:` lines join, and `: keepalive` comments are ignored. The generator releases the reader when the loop exits.

```js
async function* sse(path, body, signal) {
  const response = await checked(await fetch(API + path, {
    method: "POST", signal,
    headers: { "content-type": "application/json", accept: "text/event-stream" },
    body: JSON.stringify(body),
  }));
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      buffer += value;
      let separator;
      while ((separator = /\r?\n\r?\n/.exec(buffer))) {
        const frame = buffer.slice(0, separator.index);
        buffer = buffer.slice(separator.index + separator[0].length);
        let event = "message";
        const data = [];
        for (const line of frame.split(/\r?\n/)) {
          if (line.startsWith("event:")) event = line.slice(6).trim();
          if (line.startsWith("data:")) data.push(line.slice(5).replace(/^ /, ""));
        }
        if (data.length) yield { event, data: JSON.parse(data.join("\n")) };
      }
    }
  } finally {
    try { await reader.cancel(); } catch { /* an aborted stream is already closed */ }
    reader.releaseLock();
  }
}
```


## 4. Keep a live, ordered window

A snapshot replaces the window. Deltas change membership and values; they do not reorder a JavaScript Map. Sort by the query's two keys each time you render. These object IDs are ASCII, so direct string comparison matches their byte ordering.

```js
const ctl = new AbortController();
let view = new Map();
let resolveLive, rejectLive;
const live = new Promise((resolve, reject) => { resolveLive = resolve; rejectLive = reject; });

function render() {
  const rows = [...view.values()].sort((a, b) =>
    Date.parse(b.modifiedAt.$date) - Date.parse(a.modifiedAt.$date) ||
    (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  console.log("Live:", rows.map(r => r.any.name));
}

function pause(ms, signal) {
  return new Promise((resolve, reject) => {
    if (signal.aborted) return reject(signal.reason);
    const timer = setTimeout(() => { signal.removeEventListener("abort", abort); resolve(); }, ms);
    function abort() { clearTimeout(timer); reject(signal.reason); }
    signal.addEventListener("abort", abort, { once: true });
  });
}

async function watch() {
  const signal = ctl.signal;
  while (!signal.aborted) {
    try {
      // Check on every connection, including after EOF or a 401 without a closed frame.
      const auth = await call("GET", "/auth", undefined, signal);
      if (!auth.authorized) {
        throw Object.assign(new Error("Waiting for the original account to be authorized."), { status: 401 });
      }
      if (auth.accountId !== account.accountId) {
        view.clear(); render();
        throw Object.assign(new Error("Account changed; stopped the subscription."), { fatal: true });
      }
      for await (const { event, data } of sse(`/spaces/${SPACE}/objects/query/subscribe`, query, signal)) {
        if (event === "snapshot") {
          view = new Map(data.records.map(r => [r.id, r]));
          render(); resolveLive();
        } else if (event === "changes") {
          for (const change of data) {
            for (const row of change.added ?? []) view.set(row.id, row.doc);
            for (const row of change.updated ?? []) view.set(row.id, row.doc);
            for (const row of change.removed ?? []) view.delete(row.id);
          }
          render();
        } else if (event === "closed") {
          if (data.reason === "deauthorized") { view.clear(); render(); }
          console.log("Reopening:", data.reason);
          break;
        }
      }
    } catch (error) {
      if (error.status === 401) { view.clear(); render(); }
      if (signal.aborted || error.fatal || error instanceof SyntaxError ||
          (error.status >= 400 && error.status < 500 && ![401, 429].includes(error.status))) throw error;
      console.warn("Retrying:", error.message);
    }
    await pause(1000, signal);
  }
}
```

The loop retries network failures, server 5xx, HTTP 429, EOF and terminal frames. `closed` carries a reason — `server_shutdown`, `sdk_closed`, `overflow` (you drained too slowly), `drifted` (too much of the window left), `deauthorized` (the account was signed out or switched; read `GET /v1/auth` first). Each means the same thing for the stream: open a new POST and replace your window with the new `snapshot`. A stream that ends without `closed` means the same. There is no replay and nothing to reconcile.

While `GET /auth` reports `authorized: false`, the loop clears the view and waits. This state can occur during a restart or while a managed host authorizes an account. It subscribes again only when the original account is authorized.

A different account, cancellation, invalid JSON in a successful response or stream, or another client error stops the loop. Call `stopAnyDemo()` on deliberate sign-out: the auth status alone cannot distinguish sign-out from a temporary interruption. [Subscriptions](../realtime/subscribe.html) explains each close reason and removal reason.

## 5. Rename while watching

The watcher runs concurrently. Its first snapshot releases the rename below; a failure or cancellation before that snapshot rejects the wait instead of leaving the file stuck.

```js
const stop = () => ctl.abort();
globalThis.stopAnyDemo = stop;                       // browser console: stopAnyDemo()
if (typeof process !== "undefined" && process.once) process.once("SIGINT", stop);

// Observe completion immediately, so no background rejection is left unhandled.
const finished = watch().then(() => null, error => error);
finished.then(error => rejectLive(error ?? new DOMException("Stopped", "AbortError")));

try {
  await live;
  await call("POST", `/spaces/${SPACE}/properties/${objectId}/set/any`,
    { patch: { name: "Reading list 2026" } }, ctl.signal);
  console.log("Rename saved. Leave this process open to receive further changes.");
  const error = await finished;
  if (error) throw error;
} catch (error) {
  if (error.name !== "AbortError") throw error;
} finally {
  stop();
  await finished;
  delete globalThis.stopAnyDemo;
  if (typeof process !== "undefined" && process.removeListener) process.removeListener("SIGINT", stop);
}
```

Expect the live output to contain `Reading list 2026`. Stop with Ctrl-C in Node, or `stopAnyDemo()` in the browser console. The object remains in the space.

## Writing and reading back

Object creation returns `objectId`. Dataset writes, chat messages included, return `{versionId, changeId, recordIds}` and never the record. Read it back through a query, or let the open subscription deliver it — stamp `versionId` on what you wrote if you need to recognise your own change on the stream ([Best practices](../understanding/best-practices.html)); it is not a version to compare across devices.

A chat uses the same pattern: install `general-chat` through `POST /catalog/general-chat/setup`, take `bundles[0].bundle.rootId`, and send to `POST /spaces/:spaceId/objects/:chatId/chat/messages`. [Python](python.html#3-read-a-dataset) shows the complete dataset write and pagination sequence.

Next: [Reading data](../database/reading-data.html), [Python](python.html), or [the tutorial](../tutorial/index.html).
