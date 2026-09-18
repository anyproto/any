# Any

- local-first, e2e encrypted multiplayer database with HTTP interface
- sync engine on top of [any-sync](https://github.com/anyproto/any-sync), which has been battle-tested on our infra for many years and millions of spaces and passed a security audit by Cure53.
- mongo query language support, including aggregation framework via [any-store](https://github.com/anyproto/any-store)
- full-text search, semantic search with HNSW/ivfsq index types, hybrid search.
- local vector embedding via llama.cpp.

Any runs a local server on each user's device. Applications connect to it
through a localhost HTTP API to store data, run queries, and subscribe to
changes. Devices sync through end-to-end encrypted peer-to-peer (P2P)
connections.

Its companion runtime, [anyrt](https://github.com/anyproto/anybao), lets programs
and agents live in the same database as the data they work with. The agent
harness is designed for long-lived sessions, with persistent memory and
direct access to the database's structured data. `any-rt` compliments `any` with: 
- isolated CPython programs on top of Wasmtime with deterministic traces and fuel control
- CRON-like triggers, event subscriptions

Reads and writes to local data work offline. When devices reconnect, their
changes merge automatically using CRDTs. Devices exchange changes directly
or through sync nodes, which store and relay encrypted content without
being able to read it.

This repository packages the database server and CLI as one `any` binary.
The agent runtime runs alongside the server, acting as a client.

The database, search, identity, and sync are built in. You choose the models
and control your data and agent memory.

## What you can build

Any is a general-purpose database -- it doesn't constrain which applications
can be built on top of it.

However, some of the features are way easier to build on top of Any.

One of the strongest of such features is multiplayer collaboration. It is quite hard
to solve the problem of [agentic] concurrent write consistency due to the central
transaction architecture of databases: concurrent actors rewrite each other's data without history.

We give convenient tooling to organize conflict-free data access with CRDT eventual
consistency guarantees, a [changes history API](docs/03-api.md#version-history)
and a [safe model of mutating object properties](https://github.com/anyproto/any-sync-sdk/blob/main/docs/06-data-structure.md#object-properties)
in collaborative environments.

We have support for custom CRDT types (via
[any-sync-sdk](https://github.com/anyproto/any-sync-sdk)) and some complex CRDT implementations,
such as block-based editor and chat with distributed counters support -- i.e. we provide
collaborative block-based editor and local-first, encrypted p2p chat primitives out of the box.

One of the examples of apps which shows editor, chat and collaborative features is [any-ui](https://github.com/anyproto/any-ui)
-- modern agentic knowledge-base tool.

> [!WARNING]
> **Developer preview.** Use a separate account for experiments. APIs and data
> formats can change without migration; do not use an account containing
> data you cannot afford to lose.

## Quickstart

This example builds from source and creates a notebook in a dedicated data
directory. It uses **local embeddings**.

You need Git, Go 1.26.2 or newer, `make`, `curl`, and `jq`. For release packages
and platform-specific prerequisites, see [Installation](website/02-quickstart/install.md).

### 1. Build

```sh
git clone https://github.com/anyproto/any.git
cd any
make build
```

The binary is written to `bin/any`. This build includes full-text and vector
search and attempts to fetch the llama.cpp libraries needed for local
embeddings. If that download fails, retry with `make llamacpp`.

### 2. Create an account and start the server

Unless configured otherwise, the server joins the **production any-sync
network**. To use a different network, choose it before starting the account
and use a separate data directory. See [Networks](website/02-quickstart/networks.md).

From the repository directory:

```sh
export ANY_DATA_DIR="$HOME/.any-demo"
export ANY_INDEX_EMBEDDER=local

./bin/any init
./bin/any run
```

`init` creates an account in the new data directory and prints its recovery
phrase. Save it: restoring on another device requires that phrase. Repeating
`init` in this directory keeps the existing account and lists it instead.

Wait for `LISTENING 127.0.0.1:7001`, then leave the server running. The local
embedding model downloads separately on first use of a fresh model cache;
you can create and query data while it downloads.

### 3. Create a document

In a **second terminal**, create a space—a collection of objects shared with
the same members—and a page inside it:

```sh
API=http://127.0.0.1:7001/v1

SPACE=$(curl -fsS "$API/spaces" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Notebook"}' | jq -er '.id')

OBJECT=$(curl -fsS "$API/spaces/$SPACE/objects" \
  -H 'Content-Type: application/json' \
  -d '{"type":"page","initialProperties":{"any":{"name":"Reading list"}}}' \
  | jq -er '.objectId')

printf 'Created page: %s\n' "$OBJECT"
```

The `page` type gives the object an editor document. `any.name` is its title.

### 4. Query and subscribe

In that same terminal, query the pages in your space:

```sh
curl -fsS "$API/spaces/$SPACE/objects/query" \
  -H 'Content-Type: application/json' \
  -d '{"filter":{"any.type":"page"},"sort":["-modifiedAt"],"limit":20}' | jq
```

The response's `records` array includes your Reading list page. To keep
that query live, open the matching subscription:

```sh
curl -fsSN "$API/spaces/$SPACE/objects/query/subscribe" \
  -H 'Content-Type: application/json' \
  -d '{"filter":{"any.type":"page"},"sort":["-modifiedAt"],"limit":20}'
```

The stream sends `ready`, then a `snapshot` of the current records, followed
by `changes` as the results change. Ctrl-C closes the subscription. To stop
the server, press Ctrl-C in the first terminal.

Continue with the [objects tutorial](website/03-tutorial/objects.md) to work
with objects and add your own properties.

## Embedding modes

The quickstart explicitly selects `local`. The server's built-in default is
`auto`; configuration files or environment variables can override it.

| Mode | Where embedding inputs go |
| --- | --- |
| `local` | A local llama.cpp worker. Model files may be downloaded, but document text and queries are embedded on the device. |
| `auto` | An online embedding provider first, with the local model as fallback. |
| `none` | No embeddings; full-text search remains available in builds that include it. |

See [Embedders](website/10-search/embedders.md) for model settings, Ollama,
and other compatible providers.

## Data and access

- **Local API:** the server binds to loopback and does not authenticate
  callers. Local processes can access the API as the active account.
- **Account storage:** standalone mode stores the recovery phrase and device
  key in `wallet.key`. Set `ANY_WALLET_PASSKEY` before initializing an account
  to encrypt its wallet; supply the same passkey when starting the server.
- **Second devices:** restore from the recovery phrase with
  `./bin/any init --mnemonic-stdin`. Do not copy `wallet.key`: that also copies the
  device identity and interferes with sync.
- **Data location:** the example uses `~/.any-demo/`; the normal default is
  `~/.any/`. Treat the directory as private account data.

See [Server lifecycle](docs/02-server.md) for account modes and storage,
and [Configuration](docs/05-config.md) for file, environment, and flag precedence.

## Documentation

- [Tutorial](website/03-tutorial/index.md) — objects, properties, datasets, and apps.
- [Programs and agents](website/02-quickstart/anyrt.md) — set up the companion runtime.
- [Client guide](docs/29-client-model.md) — types, property IDs, and the catalog.
- [HTTP API](docs/03-api.md) and [CLI](docs/01-cli.md) — request shapes and commands.
- [Live queries](docs/04-events.md) — snapshots, updates, and reconnect behavior.
- [Technical overview](docs/00-overview.md) — architecture and the complete docs index.

The server builds on [any-sync-sdk](https://github.com/anyproto/any-sync-sdk).
For programs and agents, see the separate
[anyrt runtime](website/02-quickstart/anyrt.md), which can run alongside this
server or inside the Any desktop application.

## License

Any is released under the **MIT** license.
