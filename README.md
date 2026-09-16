# Any

**A local-first database for apps and agents.**

Any runs a local document database server on each user's device. Apps connect
through a local HTTP API to store data, run queries and subscribe to changes.
Reads and writes to local data work offline.

Devices exchange end-to-end encrypted changes through peer-to-peer (P2P)
connections or sync nodes. CRDTs merge those changes automatically when
devices reconnect. Sync nodes store and relay encrypted content without
being able to read it.

The `any` binary includes the database server and CLI. Its companion runtime,
[anyrt](https://github.com/anyproto/anybao), connects as a client to run programs
and agents whose code and state live in the same database as the data they
work with. The agent harness is designed for long-lived sessions, with
persistent memory and direct access to structured data.

## What's included

- **Document queries and live updates** — Mongo-style queries and aggregation
  through [any-store](https://github.com/anyproto/any-store), with subscriptions
  that keep application views current.
- **Encrypted sync and sharing** — P2P sync, CRDT merging, identity and
  access control for shared spaces.
- **Full-text and semantic search** — keyword, vector and hybrid search,
  with HNSW and IVF-SQ vector indexes. Embeddings can run locally through
  llama.cpp.
- **Collaboration primitives** — a collaborative block editor, encrypted
  P2P chat and distributed counters. The
  [any-sync-sdk](https://github.com/anyproto/any-sync-sdk) also supports custom
  CRDT types.
- **Programs and agents** — `anyrt` runs CPython in a Wasmtime sandbox, with
  recorded effects, deterministic replay and a compute budget called fuel.
- **Scheduled and event-triggered work** — cron schedules, one-time jobs
  and event-triggered runs through `anyrt`.

The sync layer builds on [any-sync](https://github.com/anyproto/any-sync),
used on our production infrastructure for years across millions of spaces.
The sync protocol has been
[audited by Cure53](https://sync.any.org/#trust-and-maturity).

## What you can build

Build knowledge bases, agent memory systems, custom harnesses, research tools,
business apps or just personal tools for fun. Any is a general-purpose
database; you choose the models and control your data and agent memory.

It is especially useful when people and agents work on shared data across
devices. Each device can write independently and merge changes when it syncs.
[Field-level updates](website/04-database/writing-data.md) let concurrent
edits to different properties survive, and the
[version history API](docs/03-api.md#version-history) lets you inspect object
changes. Concurrent writes to the same property follow the
[CRDT merge rules](website/01-understanding/crdt-and-consistency.md).

For an example, see [any-ui](https://github.com/anyproto/any-ui), an agentic
knowledge-base app built with Any's editor, chat and collaboration primitives.

> [!WARNING]
> **Developer preview.** Use a separate account for experiments. APIs and data
> formats can change without migration; do not use an account containing
> data you cannot afford to lose.

## Quickstart

Build Any from source, start a local server and create your first page. This
walkthrough uses a dedicated data directory and **local embeddings**.

You need Git, Go 1.26.2 or newer, `make`, a C toolchain, `curl` and `jq`.
For release packages and platform-specific setup, see
[Installation](website/02-quickstart/install.md).

### 1. Build

```sh
git clone https://github.com/anyproto/any.git
cd any
make build
```

This writes `bin/any` with full-text and vector search enabled, and attempts
to download the llama.cpp libraries for local embeddings. If the download
fails, retry with `make llamacpp`.

### 2. Create an account and start the server

The server joins the **production any-sync network** by default. To use
another network, configure it before starting the server and give it a
separate data directory. See [Networks](website/02-quickstart/networks.md).

From the repository directory:

```sh
export ANY_DATA_DIR="$HOME/.any-demo"
export ANY_INDEX_EMBEDDER=local

./bin/any init
./bin/any run
```

On a fresh data root, `init` creates an account and prints its recovery
phrase. Save it so you can restore the account on another device. Running
`init` again lists the existing accounts; it does not replace them.

Wait for `LISTENING 127.0.0.1:7001` and leave the server running. The local
embedding model downloads on first use; you can create and query data while
it downloads.

### 3. Create a document

In a **second terminal**, create a space and a page inside it. A space groups
data and apps under the same membership and access permissions:

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

The response's `records` array contains your Reading list page. Subscribe
to the same query to receive updates as its results change:

```sh
curl -fsSN "$API/spaces/$SPACE/objects/query/subscribe" \
  -H 'Content-Type: application/json' \
  -d '{"filter":{"any.type":"page"},"sort":["-modifiedAt"],"limit":20}'
```

The stream sends `ready`, a `snapshot` of the current records, then `changes`
as the results update. Press Ctrl-C to close the subscription. Press Ctrl-C
in the first terminal to stop the server.

Continue with the [tutorial](website/03-tutorial/index.md) to add properties,
datasets and apps to your space.

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

## Data and access

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

See [Server lifecycle](docs/02-server.md) for account modes and storage,
and [Configuration](docs/05-config.md) for file, environment, and flag precedence.

## Documentation

- [Tutorial](website/03-tutorial/index.md) — objects, properties, datasets, and apps.
- [Programs and agents](website/02-quickstart/anyrt.md) — set up the companion runtime.
- [Client guide](docs/29-client-model.md) — types, property IDs, and the catalog.
- [HTTP API](docs/03-api.md) and [CLI](docs/01-cli.md) — request shapes and commands.
- [Live queries](docs/04-events.md) — snapshots, updates, and reconnect behavior.
- [Technical overview](docs/00-overview.md) — architecture and the complete docs index.

The standalone setup runs `anyrt` alongside the server. Application hosts,
including the Any desktop app, can also embed the runtime.

## License

We plan to release Any under an open-source license. The specific license
is **TBD**.
