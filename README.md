# Any

- local-first, e2e encrypted multiplayer database with HTTP interface
- sync engine on top of [any-sync](https://github.com/anyproto/any-sync), which has been battle-tested on our infra for many years and millions of spaces and passed a security audit by Cure53.
- mongo query language support, including aggregation framework via [any-store](https://github.com/anyproto/any-store)
- full-text search, semantic search with HNSW/ivfsq index types, hybrid search.
- local vector embedding via llama.cpp.

Any combines a document database, live queries, search and end-to-end
encrypted peer-to-peer (P2P) sync in a **local server that runs on
users' devices**. Apps connect through an HTTP API and can read and write
local data offline. The server and CLI ship together as the `any` binary.

Its companion runtime, [anyrt](https://github.com/anyproto/anybao), lets programs
and agents live in the same database as the data they work with. The agent
harness is designed for long-lived sessions, with persistent memory and
direct access to the database's structured data. `any-rt` compliments `any` with: 
- isolated CPython programs on top of Wasmtime with deterministic traces and fuel control
- CRON-like triggers, event subscriptions

[Quickstart](#quickstart) · [Documentation](#documentation) · [Example app](https://github.com/anyproto/any-ui)

## What's included

- **Database and live queries** — Mongo-style filters, aggregation pipelines
  and query subscriptions, powered by
  [any-store](https://github.com/anyproto/any-store).
- **Identity and sharing** — accounts, shared spaces, member permissions
  and end-to-end encrypted P2P sync.
- **Search and embeddings** — full-text, vector and hybrid search, with
  HNSW and IVF-SQ indexes. Run embeddings on the device through llama.cpp.
- **Chat and collaborative editing** — built-in block-editor and chat CRDT
  modules, with distributed counters. Extend the model with custom CRDT
  types through [any-sync-sdk](https://github.com/anyproto/any-sync-sdk).
- **Program execution** — CPython in a Wasmtime sandbox through `anyrt`,
  with recorded effects, deterministic replay, WebAssembly instruction
  budgets (fuel) and time limits.
- **Scheduling** — cron schedules, one-time jobs and event-triggered runs
  through `anyrt`.

## Built for collaboration

Any is a general-purpose document database with collaboration built in.
People and agents can work independently on shared data, even while offline.

Collaboration takes more than accepting simultaneous writes. A writer
working from an older copy can overwrite someone else's changes.
Applications need to reconcile independent edits and keep a history of
how their data evolved. Central transactions alone do not provide those
behaviors across disconnected devices.

In Any, each device writes to its local database. Conflict-free replicated
data types (CRDTs) merge changes as devices sync. Devices that have received
the same changes converge on the same state.

[Field-level updates](website/04-database/writing-data.md) preserve concurrent
edits to different properties. Concurrent writes to the same property resolve
to one value according to the [CRDT merge rules](website/01-understanding/crdt-and-consistency.md),
and the [version history API](docs/03-api.md#version-history) lets you inspect
object changes.

Devices sync directly or through sync nodes, which store and relay encrypted
content without being able to read it.

The sync layer builds on [any-sync](https://github.com/anyproto/any-sync),
used on our production infrastructure for years across millions of users.
The sync protocol has been
[audited by Cure53](https://sync.any.org/#trust-and-maturity).

## What you can build

Build knowledge bases, agent memory systems, custom harnesses, research tools,
business apps or just personal tools for fun. You choose the models and
control your data and agent memory.

See [any-ui](https://github.com/anyproto/any-ui) for an agentic knowledge-base
app that brings together Any's editor, chat and collaboration features.

> [!WARNING]
> **Developer preview.** Use a separate account for experiments. APIs and data
> formats can change without migration; do not use an account containing
> data you cannot afford to lose.

## Quickstart

Create a page, query it and subscribe to updates. This walkthrough builds
Any from source and uses a dedicated data directory with **local embeddings**.

You need Git, Go 1.26.2 or newer, `make`, `curl` and `jq`.
For release packages and platform-specific setup, see
[Installation](website/02-quickstart/install.md).

### 1. Build

```sh
git clone https://github.com/anyproto/any.git
cd any
make build
```

The build writes `bin/any`, enables full-text and vector search, and attempts
to download the llama.cpp libraries for local embeddings. Retry a failed
library download with `make llamacpp`.

### 2. Create an account and start the server

The server joins the **production any-sync network** by default. To use another network, configure it before starting the server and give it a separate data directory. See [Networks](website/02-quickstart/networks.md).

From the repository directory:

```sh
export ANY_DATA_DIR="$HOME/.any-demo"
export ANY_INDEX_EMBEDDER=local

./bin/any init
./bin/any run
```

`init` creates an account in the new data directory and prints its recovery
phrase. Save the phrase to restore the account on another device. If this
directory already contains accounts, `init` lists them instead.

Wait for `LISTENING 127.0.0.1:7001` and leave the server running. The local
embedding model downloads on first use; database reads and writes are
available while it downloads.

### 3. Create a document

In a **second terminal**, create a space and a page inside it. A space holds
data and apps shared with the same members, each with their own permissions:

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

Your Reading list page appears in the response's `records` array. Subscribe
to the same query to receive updates:

```sh
curl -fsSN "$API/spaces/$SPACE/objects/query/subscribe" \
  -H 'Content-Type: application/json' \
  -d '{"filter":{"any.type":"page"},"sort":["-modifiedAt"],"limit":20}'
```

The stream sends `ready`, followed by a `snapshot` of the current records
and `changes` as the query results update. Press Ctrl-C to close the
subscription; press it in the first terminal to stop the server.

Continue with the [tutorial](website/03-tutorial/index.md) to add properties, datasets and apps to your space.

## Embedding modes

The quickstart selects `local`. The standalone server defaults to `auto`
unless you override it in configuration, environment variables or flags.

| Mode | Where embedding inputs go |
| --- | --- |
| `local` | A local llama.cpp worker. Model files may be downloaded, but document text and queries are embedded on the device. |
| `auto` | The configured online embedding endpoint first, with the local model as fallback. |
| `none` | No embeddings; full-text search remains available in builds that include it. |

See [Embedders](website/10-search/embedders.md) for model settings, Ollama,
and other compatible providers. Agent language-model calls use separate
provider settings.

## Data, access and recovery

- **Local API:** the server binds to loopback and does not authenticate
  callers. Local processes can access the API as the active account.
- **Account storage:** standalone mode stores the recovery phrase and device
  key in `wallet.key`. Set `ANY_WALLET_PASSKEY` before creating the account
  to encrypt its wallet, and supply the same passkey when starting the server.
- **Second devices:** restore from the recovery phrase with
  `./bin/any init --mnemonic-stdin`. Do not copy `wallet.key`: that also copies the
  device identity and interferes with sync.
- **Data location:** the example uses `~/.any-demo/`; the normal default is
  `~/.any/`. Treat the directory as private account data.

See [Server lifecycle](docs/02-server.md) for account modes and storage, and [Configuration](docs/05-config.md) for file, environment, and flag precedence.

## Documentation

- [Tutorial](website/03-tutorial/index.md) — objects, properties, datasets, and apps.
- [Programs and agents](website/02-quickstart/anyrt.md) — set up the companion runtime.
- [Client guide](docs/29-client-model.md) — types, property IDs, and the catalog.
- [HTTP API](docs/03-api.md) and [CLI](docs/01-cli.md) — request shapes and commands.
- [Live queries](docs/04-events.md) — snapshots, updates, and reconnect behavior.
- [Technical overview](docs/00-overview.md) — architecture and the complete docs index.

You can run `anyrt` alongside the server or embed it in an application host,
as the Any desktop app does.

## License

We plan to release Any under an open-source license. The specific license
is **TBD**.
