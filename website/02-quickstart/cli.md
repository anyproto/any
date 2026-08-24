---
title: CLI
description: The any command is a thin HTTP client — one subcommand per endpoint, pretty JSON on stdout — shown here for the create → query → subscribe loop.
order: 30
---
# CLI

`any <cmd>` builds a request, sends it to the running server, and prints the JSON reply. It never opens the database itself, so anything the CLI does, `curl` can do, and vice versa.

## Global flags and exit codes

```
--addr <host:port>     # default 127.0.0.1:7001
--timeout <duration>   # default 30s (ignored for streams)
--verbose              # print the HTTP exchange to stderr
```

| Exit | Meaning |
|------|---------|
| `0` | success |
| `1` | user error or a 4xx reply |
| `2` | server error (5xx) |
| `3` | cannot reach the server — `start it with any run in another terminal` |

## Lifecycle

```bash
any init                       # create the account (prints the mnemonic once)
any run                        # start the server in this terminal
any status                     # GET /v1/health
any auth status                # authorization state + local accounts
any stop                       # POST /v1/shutdown
any version                    # binary + server versions
```

## Spaces

```bash
any space derived              # well-known per-account spaces (name, spaceId, created)
any space get $SPACE           # GET /v1/spaces/:id
any space update $SPACE --name "Notebook" --description "first space"
any space query --limit 20     # windowed snapshot of the account's space list
any space subscribe            # live space-list stream, one JSON frame per line
any space sync $SPACE          # force a head-sync round now
any space delete $SPACE --yes  # irreversible
```

Creating a regular space has no dedicated subcommand — use the API directly ([curl](curl.html)):

```bash
SPACE=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces \
  -H 'content-type: application/json' -d '{"name":"Notebook"}' | jq -r .id)
```

## Objects and reads

Object creation and the generic snapshot query are likewise `curl` calls; the CLI's read surface is the live one:

```bash
OBJ=$(curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/objects \
  -H 'content-type: application/json' \
  -d '{"types":["page"],"initialProperties":{"any":{"name":"Reading list"}}}' | jq -r .objectId)

# cross-object live window: the space's objects collection
any query-subscribe $SPACE --properties \
  --filter '{"any.types":"page"}' --sort='-modifiedAt' --limit 20 --total

# per-object dataset: the blocks of one document
any query-subscribe $SPACE $OBJ --dataset editor_blocks --sort nav.pos --limit 200
```

Output is one JSON object per SSE frame — `{"event": "...", "data": ...}` — so it pipes through `jq`:

```bash
any query-subscribe $SPACE --properties --sort='-modifiedAt' --limit 20 \
  | jq -c 'select(.event=="changes") | .data[] | {added: [.added[].id], updated: [.updated[].id]}'
```

Cancel with Ctrl-C; `--timeout` does not apply to streams.

## Built-in types

The chat and editor handlers have full CLI coverage:

```bash
any editor edit $SPACE $OBJ --old '- [ ] Dune' --new '- [x] Dune'   # PATCH …/editor/markdown
any editor blocks create $SPACE $OBJ --type paragraph --text 'hello'
any chat send $SPACE $CHAT --text 'hi there'
any query-subscribe $SPACE $CHAT --dataset chat_messages --sort='-_ver.id' --limit 50
any chat react $SPACE $CHAT $MSG 👍
```

## Everything else

| Group | Commands |
|-------|----------|
| Types | `any type create/list`, `any type property list/add/patch/remove`, `any type dataset …` |
| Data | `any aggregate`, `any upsert`, `any datasets`, `any search` |
| Sharing | `any members …`, `any invite …`, `any join`, `any acl …`, `any one-to-one …` |
| Account | `any account`, `any identities …`, `any devices …` |
| Files | `any file attach/list/get/download/status/…` |
| Live | `any sync-status …`, `any events publish/subscribe`, `any process list/cancel` |
| Diagnostics | `any debug space/object/p2p` |

The full surface is in the [CLI reference](../reference/cli.html).

> **Note.** Endpoints without a CLI subcommand are deliberate, not missing: the CLI mirrors the API 1:1 where a flag surface makes sense, and defers to `curl` for bodies that are really JSON documents (object creation, ad-hoc snapshot queries, `/modify`).

Next: drive the same API from [JavaScript](javascript.html) or [Python](python.html).
