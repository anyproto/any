---
title: Core concepts
description: Understand the local server, the data model, encrypted sync, and the companion runtime before building with Any.
order: 0
---
# Understand Any

Any gives your application a document database on each user’s device. Your app reads and writes through a local HTTP API, receives live query updates, and shares data with other devices through encrypted sync.

You can use the database on its own. Add the companion **anyrt** runtime when you need programs, agents or scheduled work alongside that data.

## The three pieces

| Piece | Responsibility | Runs where? |
|---|---|---|
| **Any** | Stores and queries data; exposes HTTP, CLI and live subscriptions. | On the user’s device. |
| **Sync** | Exchanges encrypted changes, checks permissions and merges concurrent edits. | Between devices, directly on the LAN and through sync nodes. |
| **anyrt** | Executes programs and agent sessions; manages effects, traces and schedules. | On a device running the companion runtime. |

The `any run` process listens on `127.0.0.1:7001`. Other `any` commands call that server. A browser app, a script, and anyrt use the same database API.

## A small vocabulary

Start with these five terms. The [data model](../database/data-model.html) connects them with an example.

| Term | Meaning |
|---|---|
| **Space** | A container for shared data, with its own members and permissions. |
| **Object** | A document or other entity, with an ID, property values and content datasets. |
| **Type** | What an object is. Every object has exactly one type, such as `page` or a custom `person` type. |
| **Collection** | What an object is filed under. An object can belong to several collections, each adding property definitions. |
| **Dataset** | Records associated with an object: document blocks, chat messages, or a schema you declare. |

A collection used to organize objects is different from a **storage collection**, the underlying name passed as `dataset` when querying records. The distinction matters in the [dataset guide](../database/runtime-datasets.html).

## What happens when you write

1. Your client sends a request to the local Any server.
2. The server validates the operation and records the change locally.
3. Local queries and subscriptions reflect the resulting state.
4. Sync exchanges the encrypted change with reachable peers.
5. Other devices apply it and update their own queries.

Ordinary database writes work while disconnected. Their receipt identifies the change; it does not prove that another device has received it. Use [sync status](../realtime/sync-status.html) to observe convergence.

## What happens when you read

A query returns the state currently available on this device. A **subscription** returns an initial snapshot, then changes to that result window. It is the same query model with a connection kept open.

Local state can be behind another device while changes are still arriving. File content that is not cached may need a peer, and external model or connector calls have their own network requirements. See [Local-first](local-first.html).

## Where programs and agents fit

Programs are stored in spaces and executed by anyrt. The runtime mediates operations such as database calls, model requests and network access through **effects**: host operations whose results can be recorded for inspection and replay.

An agent harness adds conversation handling, tools, memory and scheduling. Persistent memory is data you can query; it is distinct from the bounded context sent to a model on each turn. Start with [Programs](../programs/index.html) or [Agents](../agents/index.html).

## Privacy boundaries

Space content is encrypted before sync. Sync nodes cannot read that content, but can see routing and membership metadata. The database and search index on the device are readable by software with access to their files.

The [installation guide](../quickstart/install.html) explicitly selects local embeddings. Without an override, the standalone server defaults to `auto`, which tries its configured online embedding endpoint before falling back to a local model. A model or connector also receives whatever content your application sends to it. The [encryption guide](encryption.html) and [security model](../operations/security-model.html) explain these boundaries precisely.

## Choose your next step

<div class="cards">
<a href="../quickstart/install.html"><strong>Run Any</strong><span>Install the binary, create an account, and start the local server.</span></a>
<a href="../database/data-model.html"><strong>Model your data</strong><span>See how objects, types, collections, properties and records fit together.</span></a>
<a href="local-first.html"><strong>Understand sync</strong><span>Learn what works offline and what a successful write means.</span></a>
<a href="architecture.html"><strong>Go deeper</strong><span>Follow the database, SDK, sync and client layers.</span></a>
</div>

For the underlying merge model, read [CRDTs and consistency](crdt-and-consistency.html), [the record CRDT](record-crdt.html), and [spaces and trees](spaces-and-trees.html). For client implementation, use [best practices](best-practices.html), [developer workflow](dev-workflow.html), and [the API principles](zen-of-any.html). [The tech space](tech-space.html), [versioning and re-indexing](versioning-and-reindex.html), and [programs and effects](programs-and-effects.html) cover the remaining internals.
