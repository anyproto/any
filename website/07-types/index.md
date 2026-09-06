---
title: Modules
description: The CRDT modules any ships with — chat and editor blocks — the type parts that declare them, and the any:// link grammar that ties records together.
order: 0
---
# Modules

any is a general database, but some shapes are so common — and so hard to get right as a CRDT — that the server ships them ready-made as **modules**. A chat is the `chat` module. A block-structured document body is the `editor` module. Both live in the same encrypted space data as everything else, sync with the same engine, and are read with the same query and subscribe primitives.

## Types, parts and modules

A module is not a type. A **type** is properties plus **parts** — the display units a client renders for an object of that type — and each part owns one or more datasets that a module serves. A type declares "my objects have a body" with a part whose dataset names the editor module; "my objects are a conversation" with a part naming the chat module. The record shape, validation and merge rules of that collection are the module's, enforced on every peer; the server keeps bespoke endpoints for **writes** only — send a message, patch a block — because the write side is where authorization and derivation happen (who may edit, what gets stamped). **Reads always go through the generic query primitive**: `POST /v1/spaces/:spaceId/query` with the collection name, and `…/query/subscribe` for live updates.

```bash
# a document type: one part sharing the editor module
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/types/$PAGE/parts \
  -H 'Content-Type: application/json' \
  -d '{"key": "body", "datasets": [{"module": "editor", "shared": true}]}'
```

| Module | Collection | Writes via | Reads via |
|------|---------|-----------|-----------|
| `chat` | `chat_messages` (shared) | `…/objects/:objectId/chat/messages[…]` | `/query` with `"dataset": "chat_messages"` |
| `editor` | `editor_blocks` (shared), or `<typeId>_<key>` for a part with its own editor | `…/objects/:objectId/editor/:collection/blocks[…]` and `…/editor/:collection/markdown` | `/query` with `"dataset": "<collection>"` |
| `records` | `<typeId>_<key>` | generic `/modify` and `/upsert` | `/query` with `"dataset": "<collection>"` — [Runtime datasets](../database/runtime-datasets.html) |

A **shared** dataset is the module's canonical collection: every type that shares the editor contributes to the same body, so an object carrying two document-ish types has one body, not two. A **namespaced** dataset (`<typeId>_<key>`) belongs to one type — a meeting type's `notes` next to its body. Chat is shared-only. An object holds a collection only while it carries a type whose part declares it: a write without one is `400 dataset.not_declared`, and no write attaches a type for you.

The `any://` link grammar is not a type, but it is the glue between them: a mention in a chat message, an image in a document and a citation of a property value are all `any://` URIs in markdown link destinations.

> **Why it matters.** A hosted backend gives you a `messages` table and lets you figure out concurrent edits, reactions and unread counts yourself — on a server you trust with plaintext. Here every message is an encrypted CRDT record: two members reacting at the same instant cannot corrupt each other, an edit made offline merges when the device reconnects, and unread state is computed on-device and never leaves your account.

## One read path, one wire shape

Because reads are generic, everything you know about [reading data](../database/reading-data.html) applies to built-in types unchanged: filters, sorts, paging, `includeTotal`, and the windowed [subscribe](../realtime/subscribe.html) stream with its `snapshot` → `changes` frames. A chat client is "a subscription sorted by `-_ver.id` with a limit"; a document view is "a subscription sorted by `nav.pos`".

Every write endpoint returns the same small result instead of the record body:

```json
{ "versionId": "…", "changeId": "…", "recordIds": ["<record id>"] }
```

`recordIds[0]` is the id the server derived for a newly created record. Read the record back through the query path — or, better, let your open subscription deliver it.

## Where the objects come from

Modules attach to ordinary objects through their types. A document is an object carrying your page type — a user type with an editor part — and blocks in `editor_blocks`. A chat is an object carrying a type with a chat part — and for "the chat of this space", the object is registered as a [bundle](../collaboration/bundles.html) with a derived root and the chat part declared on it, so every device and member computes the same object id and the conversation can never fork. There is no built-in `page`, `editor` or `chat` type: what used to need one — every client minting its own type and racing into parallel definitions — is solved by registering the type as a bundle instead.

<div class="cards">
<a href="chat.html"><strong>Chat</strong><span>A complete messenger on one CRDT dataset: send, edit, react, mentions, private read tracking, unread counters.</span></a>
<a href="editor.html"><strong>Editor</strong><span>Block-structured documents: atomic block writes plus a lossless markdown bridge for imports, exports and LLM edits.</span></a>
<a href="page.html"><strong>Page</strong><span>How a document type is declared — a user type with an editor part — and the fields a page is made of.</span></a>
<a href="links.html"><strong>Links</strong><span>The canonical any:// grammar for objects, records, mentions, spaces, property values and files.</span></a>
</div>
