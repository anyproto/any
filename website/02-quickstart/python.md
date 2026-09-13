---
title: Python
description: The create → query → subscribe loop with the standard library only — urllib for calls, http.client for the streaming SSE subscription.
order: 50
---
# Python

No third-party packages: `urllib.request` for ordinary calls, `http.client` to read the subscribe stream incrementally. Python 3.9+.

```python
import json, urllib.request, urllib.error, http.client

API = "http://127.0.0.1:7001/v1"

def call(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(API + path, data=data, method=method,
                                 headers={"content-type": "application/json"} if data else {})
    try:
        with urllib.request.urlopen(req) as res:
            return json.load(res) if res.status != 204 else None
    except urllib.error.HTTPError as e:
        err = json.load(e)["error"]            # uniform shape: {code, message, details?}
        raise RuntimeError(f"{e.code} {err['code']}: {err['message']}") from None
```

## 1. Create a space

```python
space = call("POST", "/spaces", {"name": "Notebook"})
SPACE = space["id"]                                # "bafyreig…"
```

## 2. Create an object

A document is an object carrying a type whose part declares the `editor` module — the built-in `page` for a plain body, or a document type of your own (registered as a bundle so every device agrees on one).

```python
obj = call("POST", f"/spaces/{SPACE}/objects", {
    "types": ["page"],
    "initialProperties": {"any": {"name": "Reading list"}},
})
OBJ = obj["objectId"]
```

## 3. Query

```python
page = call("POST", f"/spaces/{SPACE}/objects/query", {
    "filter": {"any.types": "page"},
    "sort": ["-modifiedAt"],
    "limit": 20,
    "includeTotal": True,
})
print(page["total"], [r["any"]["name"] for r in page["records"]])

from datetime import datetime
modified = datetime.fromisoformat(page["records"][0]["modifiedAt"]["$date"].replace("Z", "+00:00"))
```

Filters that compare a timestamp must use the same wrapper: `{"modifiedAt": {"$gte": {"$date": "2026-08-01T00:00:00Z"}}}`. A bare string does not error — it compares only within its own type bracket, so it matches nothing ([Data types](../database/data-types.html)).

## 4. Subscribe

`http.client` gives a response object whose `readline()` blocks until the next line arrives, which is all an SSE parser needs.

```python
def sse(path, body):
    conn = http.client.HTTPConnection("127.0.0.1", 7001)
    conn.request("POST", "/v1" + path, body=json.dumps(body),
                 headers={"content-type": "application/json", "accept": "text/event-stream"})
    res = conn.getresponse()
    if res.status != 200:
        raise RuntimeError(json.load(res)["error"]["code"])
    event, data = "message", []
    for raw in res:                                   # one line per iteration
        line = raw.decode().rstrip("\n")
        if line.startswith("event:"):
            event = line[6:].strip()
        elif line.startswith("data:"):
            data.append(line[5:].strip())
        elif line == "":                              # blank line ends a frame
            if data:
                yield event, json.loads("".join(data))
            event, data = "message", []
        # lines starting with ":" are keepalive comments — ignored

window = {}                                           # id → record

for event, data in sse(f"/spaces/{SPACE}/objects/query/subscribe",
                       {"filter": {"any.types": "page"}, "sort": ["-modifiedAt"], "limit": 20}):
    if event == "ready":
        continue
    if event == "snapshot":
        window = {r["id"]: r for r in data["records"]}
    elif event == "changes":
        for ch in data:                               # a batch omits the lists it has nothing for
            for r in ch.get("added", []) + ch.get("updated", []):
                window[r["id"]] = r["doc"]
            for r in ch.get("removed", []):
                window.pop(r["id"], None)
    elif event == "closed":
        print("closed:", data["reason"])              # reopen for a fresh snapshot
        break
    print(sorted(r["any"]["name"] for r in window.values()))
```

Rename the object from another shell and the loop prints the new name:

```bash
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/properties/$OBJ/set/any \
  -H 'content-type: application/json' -d '{"patch":{"name":"Reading list 2026"}}'
```

## Per-object datasets

The same body shape reads any dataset of one object — chat messages, editor blocks, a runtime dataset you declared — via the sibling per-object path. The space's one chat is the root of the `general-chat` usecase:

```python
setup = call("POST", "/catalog/general-chat/setup", {"spaceId": SPACE})
CHAT = setup["bundles"][0]["bundle"]["rootId"]

msgs = call("POST", f"/spaces/{SPACE}/query", {
    "objectId": CHAT, "dataset": "chat_messages",
    "sort": ["-_ver.id"], "limit": 50,
})
```

Page history with an absolute cursor rather than `offset`, which floats under writes:

```python
older = call("POST", f"/spaces/{SPACE}/query", {
    "objectId": CHAT, "dataset": "chat_messages",
    "sort": ["-_ver.id"], "limit": 50,
    "filter": {"_ver.id": {"$lt": msgs["records"][-1]["_ver"]["id"]}},
})
```

## Writes

Writes return the change, not the record:

```python
r = call("POST", f"/spaces/{SPACE}/objects/{CHAT}/chat/messages", {"text": "hello"})
r["recordIds"][0]                                    # the new message id
r["versionId"]                                       # stamp it to recognise your own delta
```

> **Note.** Keep the subscription loop on its own thread or task; `res` blocks on the socket between frames, and the server sends a `: keepalive` comment every 25 s during silence so an idle stream never looks dead.

Next: [anyrt](anyrt.html) runs this kind of code *inside* the database as a sandboxed program.
