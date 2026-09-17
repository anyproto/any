---
title: Python
description: "A runnable Python client using only the standard library: create a page, read chat records, and watch live changes."
order: 50
---
# Python

Use the standard library to create a page, send a chat message, and keep a live window of pages. No third-party packages are needed.

**Before you start:** leave the authorized server from [Install](install.html) running. <a href="../assets/examples/client.py" download>Download client.py</a>, or copy the Python blocks below in order. Run `python3 client.py` with Python 3.9 or newer. Each run creates a new space. The subscription comes last because reading its socket blocks until another frame arrives.

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

Every object has one required `type`. `page` is the built-in document type. Optional `collections` file it under additional property groups; values are keyed by their owner under `initialProperties` ([Objects](../database/objects.html)).

## 2. Read the space

A query returns a snapshot. Use both a sort and a limit when the result will become a live window.

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

Expect `Pages: 1 ['Reading list']`. The renderer matches the query: newest modification first, then ascending object ID. Timestamps arrive as `{"$date":"…"}`. Use that wrapper in timestamp filters too; a bare string is a different data type and will not match an instant ([Data types](../database/data-types.html)).

## 3. Read a dataset

A space query lists objects. A per-object query reads rows inside one object, such as chat messages. Install the space's general chat, then send a message before reading its history:

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

Expect one message and no older messages in this new chat. The `_ver.id` creation marker is an absolute pagination cursor on this server; edits do not move a message to the start. An empty page has no cursor. Avoid `offset` for a history that can receive new messages while you page.

Dataset writes return `{versionId, changeId, recordIds}`, not the record. Read through a query or subscription. `versionId` identifies the write on that server's stream; do not compare it across devices.

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

This example ends on a terminal frame or EOF. A long-running application must check the account, reconnect, and replace its window with the next snapshot. [JavaScript](javascript.html) demonstrates that recovery loop; [Subscriptions](../realtime/subscribe.html) gives the full contract. Put the blocking stream on a separate thread or task if your application needs to do other work concurrently.

Next: [the tutorial](../tutorial/index.html) for properties and datasets, or [anyrt](anyrt.html) to run a program beside the database.
