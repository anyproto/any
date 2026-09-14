---
title: CLI
description: The any command is a thin HTTP client — one subcommand per endpoint, pretty JSON on stdout — shown here for the create → query → subscribe loop.
order: 30
---
# CLI

`any <cmd>` builds a request, sends it to the running server, and prints the JSON reply. It never opens the database itself, so anything the CLI does, `curl` can do, and vice versa.

## Global flags and exit codes

```
--addr <host:port>     # default: the address the account's running server recorded, else 127.0.0.1:7001
--timeout <duration>   # default 30s (ignored for streams)
--verbose              # print the HTTP exchange to stderr
--control-token <tok>  # managed servers only; prefer ANY_CONTROL_TOKEN
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
any stop                       # signal the server holding the account lock
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
# a document: an object carrying the built-in page type
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
  | jq -c 'select(.event=="changes") | .data[] | {added: [.added[]?.id], updated: [.updated[]?.id]}'
```

A `changes` batch omits the lists it has nothing for, hence the `?`. Cancel with Ctrl-C; `--timeout` does not apply to streams.

## Editor and chat

The `editor` and `chat` modules have full CLI coverage. The space's one chat is the `general-chat` usecase's root, set up once per space:

```bash
any editor edit $SPACE $OBJ --old '- [ ] Dune' --new '- [x] Dune'   # PATCH …/editor/editor_blocks/markdown
any editor blocks create $SPACE $OBJ --type paragraph --text 'hello'

CHAT=$(any catalog setup general-chat $SPACE | jq -r '.bundles[0].bundle.rootId')
any chat send $SPACE $CHAT --text 'hi there'
any query-subscribe $SPACE $CHAT --dataset chat_messages --sort='-_ver.id' --limit 50
any chat react $SPACE $CHAT $MSG 👍
```

## Everything else

| Group | Commands |
|-------|----------|
| Types | `any type create/list/update`, `any type property list/add/patch/remove/option`, `any type part list/add/patch/remove`, `any type part dataset …` |
| Apps | `any catalog list/get/setup`, `any bundle ensure/list/get/resolve/child` |
| Data | `any aggregate`, `any upsert`, `any datasets`, `any search`, `any backlinks`, `any links`, `any local …` |
| Sharing | `any members …`, `any invite …`, `any join --token …`, `any acl …`, `any one-to-one …` |
| Account | `any auth login/logout/status`, `any account set-metadata/redeem`, `any identities …`, `any devices …`, `any push …` |
| Files | `any file attach/list/get/download/status/…` |
| Live | `any sync-status …`, `any events publish/subscribe`, `any process list/cancel` |
| Diagnostics | `any debug space/object/p2p` |

The full surface is in the [CLI reference](../reference/cli.html).

> **Note.** Endpoints without a CLI subcommand are deliberate, not missing: the CLI mirrors the API 1:1 where a flag surface makes sense, and defers to `curl` for bodies that are really JSON documents (object creation, ad-hoc snapshot queries, `/modify`).

Next: drive the same API from [JavaScript](javascript.html) or [Python](python.html).
