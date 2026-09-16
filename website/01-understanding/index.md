---
title: Understanding any
description: The three tiers — an encrypted local database, sync between devices and members, and a sandboxed runtime for programs and agents — and how data flows through them.
order: 0
---
# Understanding any

any is a reactive, local-first, end-to-end-encrypted database with Mongo-style queries, built-in chat and editor CRDTs, and a sandboxed Python runtime for jobs and agents that live inside your own data. This section explains the model; the quickstart gets you running in five minutes.

## The three tiers

| Tier | Component | What it does |
|------|-----------|--------------|
| **Runtime** | anyrt | Programs in wasm, scheduled jobs, agents — talks to the database over the same HTTP API. |
| **Sync** | any-sync | CRDT changes, ACLs, head-sync, p2p LAN; nodes relay ciphertext and keys never leave devices. |
| **Database** | any-store | Local document DB: queries, indexes, live windowed subscriptions, full-text + vectors. |

**Database.** Every device runs the whole database locally. Reads are indexed queries against a file on disk; writes commit locally and return immediately. The server process (`any run`) exposes it on `127.0.0.1:7001` as HTTP/JSON with Server-Sent Events for live updates.

**Sync.** A write becomes a signed, encrypted *change* in a per-object DAG. any-sync nodes store and relay those changes to the other members of the space; devices on the same LAN also sync directly over p2p. Convergence is guaranteed by the CRDT — there is no server-side "truth" that a device must defer to.

**Runtime.** anyrt runs Python programs inside a wasm cage. Programs can only touch the world through a recorded *effect boundary*, so every run is a replayable trace, and scheduled triggers are ordinary records in the database.

## How a write flows

```
client ──POST /v1/spaces/:id/modify──▶ any server
                                         │ 1. validate against dataset handler / schema
                                         │ 2. append a change to the object's DAG (local disk)
                                         │ 3. apply CRDT ops to any-store rows
                                         │ 4. emit windowed deltas to open subscriptions
                                         ▼
                                 {versionId, changeId, recordIds}   ← returned to the client
                                         │
                                         ▼ (background)
                              encrypt + sign → any-sync nodes / LAN peers → other members
                                                                          │
                                                                          ▼
                                                     their server applies the same change,
                                                     their subscriptions see the same delta
```

Steps 1–4 happen on your machine, online or not. The background leg runs whenever a peer is reachable. The reply to a write carries the change's ids, never the record body — you read state back through a query or a subscription, which is also where remote changes arrive.

## How a read flows

Reads never leave the device. `POST /v1/spaces/:id/query` runs a filter/sort/limit against the local store and returns a snapshot; `POST /v1/spaces/:id/query/subscribe` returns the same snapshot and then streams `added` / `updated` / `removed` deltas for as long as the connection stays open. See [Reading data](../database/reading-data.html) and [Subscriptions](../realtime/subscribe.html).

> **Why it matters.** Because the database is local, latency is disk latency, not network latency; because sync is a CRDT over an encrypted DAG, the nodes in between hold nothing they can read; and because the runtime lives on the same machine, an agent can work on your data without that data ever being uploaded to anyone.

## Vocabulary

| Term | Meaning |
|------|---------|
| **Account** | An identity derived from a BIP-39 mnemonic. One server process serves one account. |
| **Space** | The unit of sharing and permissions: an encrypted container of objects with its own ACL and members. |
| **Object** | A content-addressed DAG of changes. Holds property values plus zero or more datasets. |
| **Dataset** | A named storage collection of records on an object (`chat_messages`, `editor_blocks`, `agent_triggers`, …). |
| **Type** | What an object is — exactly one, in `any.type`: its properties, its layout, and the parts whose datasets the object takes. |
| **Collection** | What an object is filed under — any number, in `any.collections`: property definitions only. |
| **Module** | The server code that serves a kind of dataset — `editor` blocks, `chat` messages, generic `records` — for every type whose part declares it. |
| **Change** | One signed, encrypted write appended to an object's DAG. Its CID is the `changeId`. |
| **Program** | Python code stored in a space and executed by anyrt inside the effect boundary. |
| **Trigger** | A record describing when to run a program: cron, once, or on an event. |

<div class="cards">
<a href="local-first.html"><strong>Local-first</strong><span>Offline is the normal case; sync is how devices catch up.</span></a>
<a href="encryption.html"><strong>Encryption</strong><span>What nodes see, what they don't, and where the keys come from.</span></a>
<a href="architecture.html"><strong>Architecture</strong><span>any-store → any-sync → SDK → any → clients: what each layer owns, and the node roles.</span></a>
<a href="spaces-and-trees.html"><strong>Spaces and trees</strong><span>ACL plus object trees; changes, heads, head-sync, derived ids, the space lifecycle.</span></a>
<a href="record-crdt.html"><strong>The record CRDT</strong><span>Datasets, records, per-path ops, `_ver`, handlers, tombstones — with a worked merge.</span></a>
<a href="tech-space.html"><strong>The tech space</strong><span>The account's private space: the space list, devices, profile, account-scoped values, mirrors.</span></a>
<a href="versioning-and-reindex.html"><strong>Versioning and re-index</strong><span>Handler versions, rebuilding rows from the DAG, why there are no migrations, index generations.</span></a>
<a href="crdt-and-consistency.html"><strong>CRDTs and consistency</strong><span>Per-path last-writer-wins on DAG order, and what that means versus transactions.</span></a>
<a href="zen-of-any.html"><strong>The zen of any</strong><span>The invariants the API keeps, and why.</span></a>
<a href="dev-workflow.html"><strong>Developer workflow</strong><span>init, run, the CLI, the web UI, the data dir.</span></a>
<a href="best-practices.html"><strong>Best practices</strong><span>Eleven rules for a client that stays correct and cheap.</span></a>
<a href="programs-and-effects.html"><strong>Programs and effects</strong><span>Sandboxed Python, the effect boundary, replay, triggers as data.</span></a>
</div>
