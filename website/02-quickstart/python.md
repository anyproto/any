---
title: Python
description: The create → query → subscribe loop with the standard library only — urllib for calls, http.client for the streaming SSE subscription, plus a per-object dataset read with an absolute cursor.
order: 50
---
# Python

No third-party packages: `urllib.request` for ordinary calls, `http.client` to read the subscribe stream incrementally. Python 3.9+. The blocks form one file: create a space and a page, query, read the space's chat as a per-object dataset, then keep a live window of pages.

<a href="../assets/examples/client.py" download>Download client.py</a>, or copy the `python` blocks in order and run `python3 client.py` against the server from [Install](install.html). Each run creates a new space. The subscription comes last because `readline()` blocks on the socket until the next frame.

## 1. Connect and create a page

`urllib.request` handles ordinary HTTP calls. `API` sets the address for both requests and subscriptions. Keep the HTTP status in failures even when the response is not a JSON error envelope.

```python
import json
import shlex
import urllib.request
import urllib.error
import urllib.parse
import http.client
from contextlib import closing
from datetime import datetime

API = "http://127.0.0.1:7001/v1"


def response_error(status, response):
    try:
        error = json.load(response)["error"]
        return RuntimeError(f"HTTP {status} {error['code']}: {error['message']}")
    except (ValueError, KeyError, TypeError):
        return RuntimeError(f"HTTP {status}: unexpected error response")


def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        API + path, data=data, method=method,
        headers={"content-type": "application/json"} if data is not None else {},
    )
    try:
        with urllib.request.urlopen(req) as res:
            return json.load(res) if res.status != 204 else None
    except urllib.error.HTTPError as exc:
        with exc:
            raise response_error(exc.code, exc) from None


if not call("GET", "/auth")["authorized"]:
    raise RuntimeError("Create or select an account before running this example.")

SPACE = call("POST", "/spaces", {"name": "Python notebook"})["id"]
OBJ = call("POST", f"/spaces/{SPACE}/objects", {
    "type": "page", "initialProperties": {"any": {"name": "Reading list"}},
})["objectId"]
print("Space:", SPACE, "Object:", OBJ)
```

A document is an object carrying a type whose part declares the `editor` module — the built-in `page` for a plain body, or a document type of your own (registered as a bundle so every device agrees on one). `type` is required; `collections` (what the object is filed under) is optional; values ride `initialProperties` keyed by owner ([Objects](../database/objects.html)).

## 2. Read the space

A query is a POST with a Mongo-style body against the space's `objects` storage collection — an indexed read on your disk. A live window needs a sort and a limit, so the same body is reused for the subscription below.

```python
query = {"filter": {"any.type": "page"}, "sort": ["-modifiedAt", "id"], "limit": 20}
page = call("POST", f"/spaces/{SPACE}/objects/query", {**query, "includeTotal": True})
print("Pages:", page["total"], [r["any"]["name"] for r in page["records"]])


def display(records):
    def order(row):
        modified = datetime.fromisoformat(row["modifiedAt"]["$date"].replace("Z", "+00:00"))
        return (-modified.timestamp(), row["id"])
    print("Live:", [r["any"]["name"] for r in sorted(records, key=order)])
```

Expect `Pages: 1 ['Reading list']`. The renderer matches the query: newest modification first, then ascending object ID. Timestamps arrive as `{"$date":"…"}`, and a filter that compares one must use the same wrapper: `{"modifiedAt": {"$gte": {"$date": "2026-08-01T00:00:00Z"}}}`. A bare string does not error — it compares only within its own type bracket, so it matches nothing ([Data types](../database/data-types.html)).

## 3. Read a dataset

The same body shape reads any dataset of one object — chat messages, editor blocks, a runtime dataset you declared — via the sibling per-object path. The space's one chat is the root of the `general-chat` usecase; install it (adopt-or-install, idempotent), send a message, then read the history:

```python
setup = call("POST", "/catalog/general-chat/setup", {"spaceId": SPACE})
CHAT = setup["bundles"][0]["bundle"]["rootId"]
receipt = call("POST", f"/spaces/{SPACE}/objects/{CHAT}/chat/messages", {"text": "hello"})
print("Message:", receipt["recordIds"][0])

msgs = call("POST", f"/spaces/{SPACE}/query", {
    "objectId": CHAT, "dataset": "chat_messages", "sort": ["-_ver.id"], "limit": 50,
})
print("Messages:", len(msgs["records"]))

if msgs["records"]:
    oldest = msgs["records"][-1]["_ver"]["id"]
    older = call("POST", f"/spaces/{SPACE}/query", {
        "objectId": CHAT, "dataset": "chat_messages", "sort": ["-_ver.id"], "limit": 50,
        "filter": {"_ver.id": {"$lt": oldest}},
    })
    print("Older messages:", len(older["records"]))
```

Expect one message and no older messages in this new chat. Page history with an absolute cursor on `_ver.id` rather than `offset`, which floats under writes: the cursor is the oldest record on the current page, an edit does not move a message, and an empty page has nothing older.

Writes return the change, not the record: `{versionId, changeId, recordIds}`. Read it back through a query or let the subscription deliver it; stamp `versionId` to recognise your own delta on that server's stream — it is not a version to compare across devices.

## 4. Watch the pages change

`http.client` can read a POST response line by line. The generator closes the response and connection when it finishes or the caller exits early.

```python
def sse(path, body):
    url = urllib.parse.urlsplit(API + path)
    conn = http.client.HTTPConnection(url.hostname, url.port)
    res = None
    try:
        target = url.path + ("?" + url.query if url.query else "")
        conn.request("POST", target, body=json.dumps(body), headers={
            "content-type": "application/json", "accept": "text/event-stream",
        })
        res = conn.getresponse()
        if res.status != 200:
            raise response_error(res.status, res)
        event, data = "message", []
        for raw in res:
            line = raw.decode().rstrip("\r\n")
            if line.startswith("event:"):
                event = line[6:].strip()
            elif line.startswith("data:"):
                data.append(line[5:].removeprefix(" "))
            elif line == "":
                if data:
                    yield event, json.loads("\n".join(data))
                event, data = "message", []
        if data:
            yield event, json.loads("\n".join(data))
    finally:
        if res is not None:
            res.close()
        conn.close()
```


Print a complete rename command, then open the stream:

```python
rename = ["curl", "-fsS", f"{API}/spaces/{SPACE}/properties/{OBJ}/set/any",
          "-H", "content-type: application/json", "-d",
          json.dumps({"patch": {"name": "Reading list 2026"}})]
print("After the first Live output, paste this command into another terminal:")
print(shlex.join(rename), flush=True)

window = {}
try:
    with closing(sse(f"/spaces/{SPACE}/objects/query/subscribe", query)) as frames:
        for event, data in frames:
            if event == "snapshot":
                window = {r["id"]: r for r in data["records"]}
            elif event == "changes":
                for change in data:
                    for row in change.get("added", []) + change.get("updated", []):
                        window[row["id"]] = row["doc"]
                    for row in change.get("removed", []):
                        window.pop(row["id"], None)
            elif event == "closed":
                print("Subscription closed:", data["reason"])
                break
            else:
                continue
            display(window.values())
except KeyboardInterrupt:
    print("Stopped.")
```

Paste the command the program printed into another terminal. It already includes the real IDs; no shell variables need to be copied. Expect `Live: ['Reading list 2026']`. Ctrl-C stops the client and leaves its objects in the space.

This example ends on a terminal `closed` frame or EOF. A long-running client reads `GET /v1/auth`, reopens the POST and replaces its window with the next snapshot — there is no replay; [JavaScript](javascript.html) is that loop, [Subscriptions](../realtime/subscribe.html) the full contract.

> **Note.** Keep the subscription loop on its own thread or task; `res` blocks on the socket between frames, and the server sends a `: keepalive` comment every 25 s during silence so an idle stream never looks dead.

Next: [the tutorial](../tutorial/index.html) for properties and datasets, or [anyrt](anyrt.html), which runs this kind of code *inside* the database as a sandboxed program.
