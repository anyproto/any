---
title: Best practices
description: Eleven call patterns that keep a client correct and cheap — where writes go, how reads page, how subscriptions recover, and how to budget the live surface.
order: 60
---
# Best practices

None of these are new endpoints. They are the patterns that make a client correct on a CRDT and cheap on a local store, condensed from what has broken in real clients.

## 1. Writes go through the module's handler

Chat and editor storage collections are written only through `…/chat/messages` and `…/editor/:collection/blocks` — the handler stamps `creator` / `createdAt` / `modifiedAt`, enforces author-only edit/delete, and keys reactions per identity. For documents pick the route by change shape: targeted edit → `PATCH …/editor/editor_blocks/markdown` with `{edits:[{oldText,newText}]}` (a stale quote fails with `markdown.no_match` instead of clobbering concurrent edits); full rewrite → `PUT`; tail growth → `POST …/markdown/append`. Never `GET → string-replace → PUT`.

Every write returns `{versionId, changeId, recordIds}`, never the record. Stamp `versionId` on the paths you touched so you recognise your own change when it arrives on the stream.

## 2. Preflight-validate against the object's owners

An object's `any.type` decides which storage collections it accepts: one lives on an object only while a part of its type declares it, so an `editor_blocks` write to an object whose type is not a document type is `400 dataset.not_declared` — and no write sets a type for you. Set the type deliberately, at create (`{"type": "page"}`) or with `POST …/properties/:objectId/type/:typeId`, and check the row before writing; `GET /v1/spaces/:id/datasets` lists each storage collection's declaring `owners`.

Property values are admitted only under an **owner** the object has — its type or one of its collections filed through `POST …/properties/:objectId/collections/:collectionId` — and are checked against the declared `kind` (`400 property.kind_mismatch`) and the `xFormat` slug (`400 property.format_violation`); read `GET …/types/:id/properties` or `GET …/collections/:id/properties` once and validate before you write rather than after a 400.

## 3. Reads go through query / subscribe

- **Prefer `query`.** Open a `subscribe` only when the UI renders changes live.
- **Always set `limit`.** An unbounded read is a bug: it can produce a huge snapshot or overflow a subscribe mailbox, and drift detection is off when `limit == 0`.
- **Page on a cursor, not `offset`.** Offsets float under writes. Page with `{"_ver.id": {"$lt": "<last>"}}` and the same sort — indexed, absolute, and correct while the storage collection mutates.
- **Recency is `-modifiedAt`**, creation order `-createdAt` — author's clock, display quality only.
- **Timestamps are `{"$date": …}`** in filters too; a bare value compares only within its own type bracket, so it silently returns nothing.
- **Aggregate server-side.** Counts and top-N go through `…/aggregate`, `$match` first ([Aggregation](../database/aggregation.html)).

## 4. Chat: newest-first, subscribe first

Find the chat through the catalog (`POST /v1/catalog/general-chat/setup`, the chat is the returned `bundle.rootId`) so every client lands on the one object. Open the view with a **subscribe** — its `snapshot` *is* the initial load; a separate query first opens a gap/dup race. Sort `-_ver.id` everywhere: the window holds the top of the sort, so descending is the only direction where new messages enter it. Page history with `{"_ver.id": {"$lt": oldest}}` ([Chat](../types/chat.html)).

## 5. Hold a window; recover by resubscribing

A subscription is a moving window, not a feed to accumulate. Keep the window in memory, apply `added` / `updated` / `removed`, and keep no second store to reconcile. Every `closed` reason is terminal — `drifted` (more than `driftBudgetPercent` of the window left without replacement, default 30) and `overflow` (mailbox of `mailboxCapacity` filled, default 256) included. The fresh snapshot already reflects current state; rebuilding it from a delta is the expensive path the server refuses. Window size costs the client memory, not the server ([Subscriptions](../realtime/subscribe.html)).

## 6. Search: hybrid, then read `vectorStatus`

Default `mode: "hybrid"`; pin `fts` for exact ids and error strings. Check `vectorStatus` before trusting an empty result: `used` / `unavailable` (embedder down — retry may differ) / `disabled` (never on this server) / `skipped`. Hits carry `{scope, objectId, dataset, recordId, data, score}`, not records — hydrate through `/query`. Scores compare only within one response ([Search](../search/index.html)).

## 7. Direct chats: derive by identity, approve incoming

`POST /v1/spaces/one-to-one {"otherIdentity"}` — both sides compute the same space id, no invite. Incoming requests are `GET /v1/spaces?status=one_to_one_pending`; accept or decline by row id. Classify on `spaceType == "any.onetoone"` ([One-to-one](../collaboration/one-to-one.html)).

## 8. Members carry roles; the directory carries names

`GET /v1/spaces/:id/members` is the roster *with* permissions — the only source of roles. `GET /v1/identities` resolves a display name for any account id across spaces and carries no rights. Profiles are encrypted: tolerate empty names and let `updated` frames fill them ([Members and roles](../collaboration/members-and-roles.html)).

## 9. Files: attach and move on, render by URL

`POST …/files` returns 201 once the row is durable in the CRDT; the network backup always runs in the background, so the receipt says `durable: false` for anything but an inline file or one deduplicated against an already backed-up file. Do not block UI on `durable`. A member's incoming file becomes fetchable when its row gains `networkSign` on the files/query subscribe; `409 file.not_available` before that is "wait", not "error". Point `<img>` / `<video>` straight at `…/files/:id/content` — correct mime, Range support ([Files](../files/index.html)).

## 10. Subscribe to views, not data

The expensive thing is a *wide* live surface, not a large history. One subscription per open view — the visible list, the open document or chat. Never one per object ("watch all chats" is the canonical anti-pattern). Aggregates such as unread counts ride the row as materialized properties so the list subscription you already hold covers them. Durable consumers (indexers, notifiers) keep their own cursor and treat events as a wake-up only. After any disconnect, rebuild from a snapshot, never from the missed gap.

## 11. Mobile push: cache keys, decrypt without the server

A push arrives when `any` is probably not running. Keep an append-only `{encKeyId → encKey}` cache per space in the OS keystore from `SpaceInfo.push`, refresh it on every foreground and from the space-list stream, and on a cache miss show a generic "New message" ([Push](../notifications/push.html)).

> **Why it matters.** Most of these rules exist because the store is *on the device*. Reads are cheap, so query instead of caching; the server never has more truth than your last snapshot, so recover from snapshots; and every open window is memory on the machine the user is holding, so budget the live surface by what is on screen.

> **Note.** The full text of every rule, with the reasoning and the worked examples, is the source for this page: the client recommendations in the server's docs. When this page and a wire-shape reference disagree, the reference wins ([HTTP API](../reference/http-api.html)).
