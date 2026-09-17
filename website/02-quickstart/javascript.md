---
title: JavaScript
description: A complete fetch client that creates a page, watches a sorted result window, and recovers from an interrupted stream.
order: 40
---
# JavaScript

Create a page, subscribe to its space, and rename it while the subscription is open. The example maintains an ordered result window and reconnects after an interrupted stream.

**Before you start:** complete [Install](install.html) and leave the authorized server running. <a href="../assets/examples/client.mjs" download>Download client.mjs</a>, or copy the JavaScript blocks below in order. Run `node client.mjs` with Node 18 or newer. Each run creates a new space.

The same file works in a browser module. Serve it from `http://localhost:5173` or `http://127.0.0.1:5173`, the allowed development origins. Other origins need an appropriate proxy and a same-origin `API` URL ([Security model](../operations/security-model.html)).

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

An object requires one `type`; `collections` is optional. Names and descriptions belong to the universal `any` property group.

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

Subscriptions are POSTs, so use `fetch` instead of `EventSource`. This parser accepts LF or CRLF frames, joins multiple `data:` lines, ignores keepalive comments, and releases the reader when the loop exits.

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

The loop retries network failures, server errors, HTTP 429, EOF, and terminal frames. There is no event replay: reconnecting supplies a fresh snapshot.

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

Object creation returns `objectId`. Dataset writes, including chat messages, return `{versionId, changeId, recordIds}`; read the new record through a query or the open subscription. A change's `versionId` can identify your own write on that server's stream, but is not a version to compare across devices.

A chat uses the same pattern: install `general-chat` through `POST /catalog/general-chat/setup`, take `bundles[0].bundle.rootId`, and send to `POST /spaces/:spaceId/objects/:chatId/chat/messages`. [Python](python.html#3-read-a-dataset) shows the complete dataset write and pagination sequence.

Next: [Reading data](../database/reading-data.html), [Python](python.html), or [the tutorial](../tutorial/index.html).
