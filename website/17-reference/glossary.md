---
title: Glossary
description: One-line definitions of every term the docs lean on — spaces, datasets, changes, derived objects, overlays, effects, traces, identities — each linked to the page that covers it.
order: 100
---
# Glossary

Short definitions, in roughly the order you meet them. Each links to the page that explains the concept in full.

## Data model

| Term | Definition |
|---|---|
| **Space** | The unit of sharing and encryption: a set of objects with one ACL and one read key, replicated between the members' devices through sync nodes that never hold the key. — [Spaces](../database/spaces.html) |
| **Tech space** | The per-account private space that holds the account's own bookkeeping — the space list, profile, settings, devices, identities directory — synced only between your devices. — [Space list](../realtime/space-list.html) |
| **Object** | A CRDT tree inside a space: one row of property values in the space's `objects` storage collection plus any per-object datasets (blocks, messages, runtime records). Exactly one type, any number of collections. — [Objects](../database/objects.html) |
| **Dataset** | A named storage collection of records that lives on an object — `chat_messages`, `editor_blocks`, or a runtime-defined one — read through `/query` and written through `/modify` or its module's routes. — [Runtime datasets](../database/runtime-datasets.html) |
| **Record** | One document in a dataset, addressed by id, mutated by per-path `$set` / `$unset` ops and tombstoned rather than erased. — [Writing data](../database/writing-data.html) |
| **Change / ChangeId** | One signed, content-addressed entry in an object's DAG; every write returns its `changeId`, which doubles as a version handle on every peer. — [Version history](../database/version-history.html) |
| **Version** | A point in an object's causal history named by a ChangeId — "everything this change could see", not a wall-clock moment. — [Version history](../database/version-history.html) |
| **Head-sync** | The periodic diff round in which a device exchanges tree heads with the responsible nodes and pulls what it lacks; `POST …/sync` forces one now. — [Sync status](../realtime/sync-status.html) |
| **CRDT** | The merge discipline that lets every device write offline and converge on the same state without a server deciding — per-path last-writer-wins on the DAG order. — [CRDT and consistency](../understanding/crdt-and-consistency.html) |
| **Derived object / space** | An object or space whose id is a pure function of a seed (and the space or account keys), so every device computes it offline and none can fork it — at the price of permanence. — [Derived objects](../database/derived-objects.html), [Derived spaces](../collaboration/derived-spaces.html) |
| **Bundle** | One installed thing in a space — a chat, an app's setup — registered under a permanent versioned id with a root object and deterministic children, so devices converge on one install. — [Bundles](../collaboration/bundles.html) |
| **Usecase** | A set of well-known `system:` bundles from the server's embedded catalog, plus the usecases it requires; one setup call installs or adopts them all and returns the type, collection and property ids to write with. — [Bundles](../collaboration/bundles.html#the-usecase-catalog) |
| **Type** | What an object **is** — one per object, in `any.type`: property definitions, parts and a layout; resolved by `xKey`, stored by id. — [Types and properties](../database/types-and-properties.html) |
| **Collection** | What an object is **filed under** — any number per object, in `any.collections`: property definitions only, no parts and no layout. `miniapp` and `bin` are built in. — [Collections](../database/collections.html) |
| **Storage collection** | The place records live in — `objects`, `editor_blocks`, `chat_messages`, `<typeId>_<key>` — and the `dataset` value on reads and writes. Not something an object is filed under. — [Data model](../database/data-model.html) |
| **Owner** | The type or collection whose namespace a property value lives in: `record[ownerId][propId]`. — [Types and properties](../database/types-and-properties.html) |
| **Part** | A display unit of a type — a body, a transcript, a task list — owning the storage collections a module serves. Types only; collections have none. — [Modules](../types/index.html) |
| **Module** | The server code that serves a part's dataset: `records` (the generic schema handler), `editor` or `chat`; a shared dataset is the module's canonical storage collection, a namespaced one is `<typeId>_<key>`. — [Modules](../types/index.html) |
| **xKey** | A type's or collection's stable programmatic handle (`book`, `wiki`), unique per space across both surfaces, unaffected by display-name renames; a property's xKey is its handle within its owner. — [Types and properties](../database/types-and-properties.html) |
| **propId** | The content-addressed id of a property definition; values are stored and validated at `record[ownerId][propId]`, never by xKey. — [Types and properties](../database/types-and-properties.html) |
| **Wiki tree** | The catalog usecase that gives a space its tree: a hidden `wiki` **collection** whose `parentId` / `pos` / `folder` columns place every object filed under it; `pos` is a client-allocated lexid. — [Objects](../database/objects.html) |
| **Scope (field)** | Where a value lives and who sees it: `synced` (every member), `account` (your devices), `local` (this device), `derived` (computed by a handler). — [System fields](../database/system-fields.html) |
| **Local store** | Device-local storage collections at `/v1/local` with the same query and aggregation power but no sync, no CRDT and no subscribe. — [HTTP API](http-api.html) |
| **CRDT version** | The account-wide data-model version mark; a server older than the mark refuses to open the account, or turns read-only when another device raises it. — [Versioning and re-index](../understanding/versioning-and-reindex.html) |

## Search

| Term | Definition |
|---|---|
| **Chunker** | A per-dataset adapter that turns records into index entries — editor blocks, chat text, property values, runtime-schema records — and streams deletions as tombstones. — [Indexing](../search/indexing.html) |
| **Scope (index)** | An open slug (`basic`, `chat`, `props`, `agent`, …) each index entry lands under, so a search can be narrowed to one kind of content. — [Indexing](../search/indexing.html) |
| **Embedder** | The model that turns text into vectors for semantic recall — local llama.cpp, Ollama, or an OpenAI-compatible API; optional, FTS works without it. — [Embedders](../search/embedders.html) |
| **Backlink** | An edge in the link index — a mention, link, card, embed or relation pointing at an object, record, value, identity or file — read from the target's side. — [Links](../types/links.html) |

## Identity and network

| Term | Definition |
|---|---|
| **Account / identity** | The key pair derived from a BIP-39 mnemonic; its public key is the identity that signs changes and appears in ACLs. — [Accounts](../auth/accounts.html) |
| **Standalone / managed** | Who owns a server: `standalone` is the user's (keys on disk, no sign-out over HTTP); `managed` is a host app's (account supplied on every launch; sign-out, switching and shutdown behind the control token). — [Accounts](../auth/accounts.html) |
| **Device** | One installation of an account, with its own device key and peerId; the tech-space `devices` dataset lists them. — [Devices](../auth/devices.html) |
| **peerId** | The network identity of one device, derived from its device key — what the sync nodes and LAN peers see. — [Devices](../auth/devices.html) |
| **Nodeconf** | The YAML describing a sync network — coordinator, sync and file nodes and the network id; the binary embeds production, and one setting points elsewhere. — [Networks](../operations/networks.html) |
| **Coordinator** | The network node that brokers space registration, deletion, invites and the inbox — it sees metadata, never content. — [Networks](../operations/networks.html) |
| **ACL** | A space's access-control log: a signed chain of records granting, changing and revoking member permissions and rotating the read key. — [ACL](../collaboration/acl.html) |
| **Invite / guest key** | A share token a joiner pastes back to request membership; a guest key is the same shape for public read-only access. — [Invites](../collaboration/invites.html) |
| **One-to-one space** | A direct space between exactly two identities, derived from both keys, with no invite handshake. — [One-to-one](../collaboration/one-to-one.html) |

## Programs and agents

| Term | Definition |
|---|---|
| **Program** | A Python module (`name@vN`) run inside the anyrt sandbox; it reaches the world only through recorded effects. — [Writing a program](../programs/writing-a-program.html) |
| **Effect** | One host syscall a program makes — an HTTP call, `time.now`, `config.get` — recorded in the trace with its kind and capability. — [Effects](../programs/effects.html) |
| **Overlay** | A repo space that supplies programs and skills under an alias (`agent:`, `std:`), joined read-only; the working space can shadow any of them by name. — [Modules and overlays](../programs/modules-and-overlays.html) |
| **Trace** | The append-only record of one run — every effect, cell and model turn — from which the run replays bit-exactly. — [Traces and replay](../programs/traces-and-replay.html) |
| **Cell** | One execution step inside a run — a unit of guest code the trace attributes effects and fuel to. — [Traces and replay](../programs/traces-and-replay.html) |
| **Trigger** | A record that fires a program on a cron schedule, once at a time, or on a chat event, pinned to one of your devices. — [Trigger schema](trigger-schema.html) |
| **Process** | A live progress view over the event bus for long-running work, with heartbeat, staleness expiry and cancel. — [Processes](../notifications/processes.html) |

Wire-level details for everything above: [HTTP API](http-api.html), [SSE streams](events.html), [Errors](errors.html).
