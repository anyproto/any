---
title: Built-in types
description: The CRDT data types any ships with — chat, editor blocks, the page marker — and the any:// link grammar that ties records together.
order: 0
---
# Built-in types

any is a general database, but some shapes are so common — and so hard to get right as a CRDT — that the server ships them ready-made. A chat is a built-in type. A block-structured document is a built-in type. Both live in the same encrypted space data as everything else, sync with the same engine, and are read with the same query and subscribe primitives.

## What "built-in" means

A built-in type is a type registered by the server in every space, with a dataset whose record shape, validation and merge rules are enforced on every peer. The server keeps bespoke endpoints for **writes** only — send a message, patch a block — because the write side is where authorization and derivation happen (who may edit, what gets stamped). **Reads always go through the generic query primitive**: `POST /v1/spaces/:spaceId/query` with the type's dataset name, and `…/query/subscribe` for live updates.

| Type | Dataset | Writes via | Reads via |
|------|---------|-----------|-----------|
| `chat` | `chat_messages` | `…/objects/:objectId/chat/messages[…]` | `/query` with `"dataset": "chat_messages"` |
| `editor` | `editor_blocks` | `…/objects/:objectId/editor/blocks[…]` and `…/editor/markdown` | `/query` with `"dataset": "editor_blocks"` |
| `page` | — | none (a marker) | `{"filter": {"any.types": "page"}}` on the objects query |

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

Built-in types attach to ordinary objects. A document is an object with `page` in `any.types` and blocks in its `editor_blocks` dataset. A chat is an object carrying the `chat` type — and for "the chat of this space", the object is registered as a [bundle](../collaboration/bundles.html) with a derived root, so every device and member computes the same object id and the conversation can never fork.

<div class="cards">
<a href="chat.html"><strong>Chat</strong><span>A complete messenger on one CRDT dataset: send, edit, react, mentions, private read tracking, unread counters.</span></a>
<a href="editor.html"><strong>Editor</strong><span>Block-structured documents: atomic block writes plus a lossless markdown bridge for imports, exports and LLM edits.</span></a>
<a href="page.html"><strong>Page</strong><span>The built-in marker that says "this object is a document", and the fields a page is made of.</span></a>
<a href="links.html"><strong>Links</strong><span>The canonical any:// grammar for objects, records, mentions, spaces, property values and files.</span></a>
</div>
