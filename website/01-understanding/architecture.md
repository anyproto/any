---
title: Architecture
description: The layered stack under the HTTP API — any-store, the any-sync protocol and its node roles, the SDK's record CRDT, the any server, and the clients and runtime on top — and what each layer owns.
order: 15
---
# Architecture

any is five layers, each with one job: an embedded document database, a peer-to-peer sync protocol, an SDK that turns signed changes into queryable records, a local HTTP server, and whatever talks to that server. Knowing which layer owns a behaviour tells you where its guarantees come from — and where its limits are.

## The stack

| Layer | What it owns |
|-------|--------------|
| **Clients** | Web UI, CLI, JS / Python / mobile bindings, anyrt (programs and agents) — all speak HTTP/JSON + SSE. |
| **any** | HTTP façade on `127.0.0.1:7001`, CLI client, search and link index, event bus, markdown bridge, bundles engine and usecase catalog, local store. |
| **any-sync-sdk** | Space / Object / record CRDT, types, collections and properties, datasets and handlers, tech space, queries and subscribe. |
| **[any-sync](https://github.com/anyproto/any-sync)** | The protocol: spaces, ACLs, object trees (signed + encrypted change DAGs), head-sync, files, nodeconf, mDNS p2p. |
| **[any-store](https://github.com/anyproto/any-store)** | Embedded document DB: storage collections, Mongo filters, modifiers, indexes, transactions, FTS + vector indexes. |

Everything above the any-sync layer runs inside one process on your machine. Below it sits the network — and, as the next sections show, the network holds only ciphertext.

## any-store — the database

[any-store](https://github.com/anyproto/any-store) is an embedded document database. It stores anyenc documents (a binary JSON-like encoding) in storage collections, filters them with a MongoDB-style query language (`$eq`, `$in`, `$gt`, `$elemMatch`, `$regex`, `$and`/`$or`, …), mutates them with modifiers (`$set`, `$unset`, `$inc`, …), and picks indexes with a cost-based planner. It provides ACID transactions with a single writer and snapshot-isolated reads, plus BM25 full-text and vector indexes that the [search index](../search/index.html) is built on.

It has no notion of sync, peers, encryption or subscriptions. Every layer above it is what turns a local file into a shared database.

## any-sync — the protocol

[any-sync](https://github.com/anyproto/any-sync) is the open protocol for end-to-end-encrypted, local-first collaboration. Its unit of sharing is a **space**: a collection of objects with an access control list. Its unit of data is an **object tree**: a content-addressed DAG of changes, each one signed by its author and encrypted with the space's read key. Devices exchange changes; nobody exchanges "state". [Spaces and trees](spaces-and-trees.html) covers the mechanics; here is the map.

### Nodes and their roles

The infrastructure side is a set of nodes, each with a fixed job. The protocol is symmetric — a node runs the same code paths as a device, so every node is just another peer.

| Node type | nodeconf `types` | Responsibility |
|-----------|------------------|----------------|
| **Sync node** | `tree` | Stores and relays spaces: ACL records and object-tree changes, all ciphertext. Each space is served by several sync nodes for redundancy. |
| **File node** | `file`, `fileV2` | Stores encrypted file blocks (IPLD DAGs). Backs the durability tier of [files](../files/index.html). |
| **Consensus node** | `consensus` | Validates ACL changes: an ACL is a linked list of signed records, and a sync node asks a consensus node to confirm a new record is a conflict-free extension before accepting it. |
| **Coordinator node** | `coordinator` | Keeps the network configuration; registers spaces, tracks their status, runs the deletion log, stores invite files, and tells clients which sync nodes serve a space. |

The production configuration also lists naming and payment-processing nodes; any does not use them.

### What a network is

A **network** is a nodeconf: a YAML document with a network id (its public key), and a list of nodes — peer id, addresses, types. The `any` binary embeds the production nodeconf and joins it unless configured otherwise ([Networks](../operations/networks.html)).

Which sync nodes serve a given space is not negotiated — it is computed. Every space header carries a `replicationKey`; the nodeconf hashes it through a consistent-hash ring of partitions to pick the responsible nodes. A device asks the coordinator for the current configuration, computes the same answer, and connects to those nodes. Adding a node changes the configuration and reshards spaces between nodes.

### What nodes can see

Every change on the wire has a cleartext envelope — previous change ids, the ACL head it was written under, the read-key id, the author's public key, the signature — and an encrypted payload. Nodes verify the envelope: signature valid, author present in the ACL at that head, DAG links resolvable. They never hold a read key, so the payload is opaque to them ([Encryption](encryption.html)).

Two consequences are worth stating plainly. A sync node cannot serve a query, so **every query runs on a device**; and an ACL change needs a consensus node, so **changing membership requires network access** while every other write works offline.

### Two transports

Devices reach each other two ways, and both carry the same changes: through the network's sync nodes, and directly over the LAN, where any-sync discovers peers with mDNS and syncs the spaces both sides have access to. A device that has both acts as a bridge for one that has only the LAN ([Local-first](local-first.html)).

## any-sync-sdk — records out of changes

any-sync stores and delivers encrypted blobs; it does not know what is in them. The SDK is the layer that decides: it defines that a change is a batch of per-record ops on a named **dataset**, applies those ops to any-store rows through a per-field version gate, and exposes the result as queries and windowed subscriptions. It owns:

- **Spaces** as an API: create, join, invite, ACL operations, members, delete-and-offload.
- **Objects** with many datasets each; the DAG itself is hidden — callers see `Modify` / `Delete` / `Query`.
- **The record CRDT** — per-path last-writer-wins on DAG order, upsert, sticky tombstones, content-addressed record ids ([Record CRDT](record-crdt.html)).
- **Types, collections and properties** — the `objects` storage collection with one row per object, property definitions as records on the type and collection objects that own them, scopes on declarations.
- **Dataset handlers** — the SDK's own (spaceIndex, bundles, files, …), modules the host registers (any's chat and editor), and runtime-declared schemas — run identically on every peer at apply time.
- **The tech space** — the account's private space holding the space list, devices, profile and account-scoped values ([Tech space](tech-space.html)).
- **Versioning** — the version-driven re-index that rebuilds rows from the DAG when handler logic changes ([Versioning and re-index](versioning-and-reindex.html)).

Where encryption happens: here. The SDK's auth module derives account keys from the mnemonic; changes are signed and encrypted inside the SDK before any-sync ever sees them, and decrypted after any-sync hands them back.

## any — the local server

any wraps the SDK in a single binary: `any run` opens the SDK and serves HTTP/JSON on `127.0.0.1:7001`; every other `any <cmd>` is an HTTP client for that server. Endpoints map 1:1 onto SDK methods, and the server adds only what the SDK deliberately leaves to consumers: the search and link index over the change feed, the ephemeral event bus, the markdown bridge, the bundles engine and usecase catalog, the device-local store, process reporting. The rules it holds itself to are on [The zen of any](zen-of-any.html).

```bash
curl http://127.0.0.1:7001/v1/health
# { "status": "ok", "version": "any v0.1.2 (commit 1a2b3c4, built 2026-09-09)", "account": "A8tR…", "bootstrapping": false, … }
```

The loopback address is the trust boundary: no TLS, no per-client authentication, and a refusal to bind anything else. A server a host app spawns in managed mode additionally gates logout, account switch and shutdown behind a control token ([Security model](../operations/security-model.html)).

## Clients and anyrt

Everything above the server is a client of it — the web UI, the CLI, the language bindings, and **anyrt**, the sandboxed runtime that executes Python programs and agents. anyrt talks to the same HTTP API as any other client, so a program or an agent reads and writes your data on the machine; anything it sends out — a model call, a connector request — goes through its logged effects ([Programs and effects](programs-and-effects.html)).

> **Why it matters.** In a hosted backend the database, the auth, the business logic and the sync all live on a server you rent. Here the sync network is a relay for ciphertext, and the database, the auth and the logic run on your device. Content reaches an outside party only through an outside provider — the online embedder of the default `index.embedder: auto`, an agent's model — and only what that provider is sent. That is why the API can promise offline reads and writes, why membership is enforced by signatures instead of sessions, and why the design choices on the following pages look the way they do.

## Further reading

- [Spaces and trees](spaces-and-trees.html) — the any-sync data model and lifecycle.
- [Record CRDT](record-crdt.html) — what the SDK does with a change.
- [Tech space](tech-space.html) — the account's private space.
- [Versioning and re-index](versioning-and-reindex.html) — how rows survive code changes without migrations.
