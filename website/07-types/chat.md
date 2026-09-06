---
title: Chat
description: The messenger module — sending, editing, reacting, mentions, and account-private read tracking on the chat_messages collection.
order: 10
---
# Chat

The `chat` module turns an object into a conversation: one `chat_messages` collection, one record per message, edits and reactions that merge on every peer, and read state the SDK maintains for you. An object holds the collection while it carries a type whose part declares `{"module": "chat", "shared": true}` ([modules](index.html)); chat is shared-only, one conversation per object. You write through a handful of chat endpoints and read everything — including live updates and unread flags — through the ordinary query primitive.

## The model in four sentences

Messages are records on the chat object's `chat_messages` dataset, ordered by `_ver.id` (set at creation, never changed by edits). Writes go through the chat endpoints; all reads and liveness go through `/query` and `/query/subscribe`. Read state is tracked by the SDK, **private to the account** (synced across your devices, never visible to other members — no read receipts, by design) and **forward-only**: a message once read never becomes unread again. Everything a client renders is materialized into ordinary queryable fields — you never compute read state yourself.

## Finding the chat object

Most clients want "the chat for this space" — one well-known chat, not one per client. Register it as a [bundle](../collaboration/bundles.html) and use the root the server hands back:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/bundles \
  -H 'Content-Type: application/json' \
  -d '{"id": "general-chat/v1", "name": "General", "derived": true, "parts": [{"key": "chat", "datasets": [{"module": "chat", "shared": true}]}]}'
# → 200 { "bundle": { "rootId": "<chat object>", "derived": true, … }, "installed": true|false }
```

The `parts` declaration makes the root its own type with a chat part, so it accepts messages from the first write. The call is adopt-or-install, and `"derived": true` makes the root id a pure function of the bundle id — every client, on any device, for any member, online or not, computes the same `rootId`. Use it as `<objectId>` in every endpoint below. Do **not** `POST /objects` a fresh chat per client: a space would carry two or three parallel chats depending on who spoke first, and chat content cannot be merged across objects (`creator` and `createdAt` are stamped from the change envelope, so copying messages re-attributes and re-times them). Two consequences: the chat is permanent (a derived root cannot be deleted), and a space that already has a created chat root is adopted (`derived: false` in the reply), not migrated. Additional purpose-specific chats get their own bundle id.

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/spaces/:spaceId/objects/:objectId/chat/messages` | send a message |
| PATCH | `…/chat/messages/:msgId` | edit own message text |
| DELETE | `…/chat/messages/:msgId` | delete own message |
| POST | `…/chat/messages/:msgId/reactions/:emoji` | toggle own reaction |
| POST | `…/chat/read-all` | mark everything read |
| POST | `…/chat/messages/:msgId/read` | mark the message and everything above it read |
| POST | `…/chat/messages/:msgId/reactions-read` | mark the message's reactions read |

Every write returns the shared write result `{versionId, changeId, recordIds}` — never the message body.

## Message wire shape

This is what `/query` and `/query/subscribe` return for a `chat_messages` record:

```json
{
  "id":               "<change-derived id>",
  "creator":          "<accountId>",
  "createdAt":        {"$date": "2026-05-01T21:00:00.000Z"},
  "modifiedAt":       {"$date": "2026-05-01T21:00:00.000Z"},
  "replyToMessageId": "<msgId>",
  "text":             "**hi** _there_",
  "mentions":         ["<identity1>"],
  "attachments":      { "a1": { "type": "image", "link": "any://f/<spaceId>/<fileId>" } },
  "reactions":        { "👍": { "<identity1>": {"$date": "2026-05-01T21:00:05.000Z"} } },
  "agent":            { "name": "bao", "debugLink": "any://<spaceId>/<debugObjId>#turn_3", "done": true },
  "unread":           true
}
```

| Field | Who writes it | Notes |
|-------|---------------|-------|
| `creator`, `createdAt`, `modifiedAt` | server-derived | Instants (`{"$date": …}`). Equal on a never-edited message — detect edits by comparing them. Client attempts to set them are rejected. |
| `text` | client | Markdown, ≤ 32 KiB. Rendering is the client's job. |
| `replyToMessageId` | client, create-only | Soft reference (≤ 256 bytes); the target is not validated. |
| `mentions` | server-derived | Identities mentioned in `text` plus the replied-to author. Never accepted from a client. Absent when nobody is mentioned. |
| `attachments` | client, create-only | Map of short ids (`[A-Za-z0-9_-]{1,64}`) → `{type, link}`. `type` is open (`link`, `image` known — render unknown types as a plain link); `link` ≤ 2 KiB; ≤ 32 entries. |
| `reactions` | server-derived leaves | `emoji → {accountId → instant}`; same shape on read and write. |
| `agent` | client, create-only | Marks an agent-authored message (UI hint, not signature-verified). `name` required (≤ 256 B), `debugLink` optional (≤ 2 KiB), `done` required boolean. |
| `unread`, `unreadMention`, `unreadReactions` | SDK, local scope | Present (`true`) only while set; never synced to other members. |

`text` is required unless `attachments` is non-empty — an attachment-only send is valid. A message with neither is rejected with `400 chat.text_required`.

## Sending, editing, deleting, reacting

```bash
# send
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/$CHAT/chat/messages \
  -H 'Content-Type: application/json' \
  -d '{"text": "hello", "replyToMessageId": "abc"}'
# → 201 { "versionId": "…", "changeId": "…", "recordIds": ["<msgId>"] }

# edit (own only)
curl -X PATCH http://127.0.0.1:7001/v1/spaces/$SP/objects/$CHAT/chat/messages/$MSG \
  -H 'Content-Type: application/json' -d '{"text": "hello, world"}'

# delete (own only)
curl -X DELETE http://127.0.0.1:7001/v1/spaces/$SP/objects/$CHAT/chat/messages/$MSG

# toggle a reaction (no body)
curl -X POST http://127.0.0.1:7001/v1/spaces/$SP/objects/$CHAT/chat/messages/$MSG/reactions/👍
```

The same from the CLI: `any chat send $SP $CHAT --text "hello"`, `any chat edit …`, `any chat delete …`, `any chat react $SP $CHAT $MSG 👍`. `--file -` reads message text from stdin.

Edit replaces `text` and bumps `modifiedAt`; delete tombstones the record. Both answer `403 chat.not_author` for anyone but the author and `404 chat.not_found` for unknown ids — and the handler enforces the same rules on changes arriving from peers, so a forged edit from another device is rejected at apply time, not just at the HTTP door.

A reaction toggle is a `$set` (add) or `$unset` (remove) on the single leaf `reactions.<emoji>.<callerId>`, and only the change's signer can write into their own leaf. Because the leaf is unique per (emoji, identity), two members reacting simultaneously can never corrupt each other's reaction. The timestamp is server-derived; sort by it client-side if you want arrival order.

## Reading and live updates

Open a chat view by subscribing — the stream's `snapshot` frame *is* your initial load; a separate up-front query only opens a gap/duplicate race against the first `changes` frame:

```bash
curl -N -X POST http://127.0.0.1:7001/v1/spaces/$SP/query/subscribe \
  -H 'Content-Type: application/json' \
  -d '{"objectId": "'$CHAT'", "dataset": "chat_messages", "sort": ["-_ver.id"], "limit": 50}'
```

Subscribe **descending** with a `limit`. The window holds the top of the sort, so with `-_ver.id` new arrivals enter it and the oldest drops out as `removed`; ascending would pin the oldest 50 forever. New messages arrive in `added` (with the full body in `doc`), edits in `updated`, deletes and reaction-offs in `removed`. Always set a limit — an unbounded subscribe risks overflowing the mailbox.

`_ver.id` is the record's position in the sync DAG — logical order, not wall-clock time — stamped once at creation and never bumped by edits. Page into history with a one-shot query on the oldest id you hold:

```json
{ "objectId": "<chatId>", "dataset": "chat_messages",
  "sort": ["-_ver.id"], "filter": {"_ver.id": {"$lt": "<oldestVersion>"}}, "limit": 50 }
```

Repeat until a short page; reverse each page client-side for oldest-at-top. The same body without the filter, against `/query`, is the point-in-time read when you don't want live updates.

## Timestamps

`createdAt`, `modifiedAt` and every reaction leaf are instants: `{"$date": "2026-05-01T21:00:00.000Z"}`. Unwrap the one key and compare instants to instants. Tolerate a bare number there too — a peer on an older build materializes the same message as unix seconds, and the wire format deliberately does not gate on it, because refusing the change would stop messages syncing to protect a field's shape:

```js
const ms = v && typeof v === 'object' ? Date.parse(v.$date) : v * 1000;
```

## Read tracking

> **Why it matters.** Unread state in a hosted messenger is a server-side table the operator can read. Here it is computed by the SDK on your own device from the CRDT, synced only across your account's devices, and never encoded into the protocol at all — there is nothing for another member to observe.

The SDK maintains, per message, the local flags `unread`, `unreadMention` (the message's `mentions` contains you) and `unreadReactions` (only ever set on messages **you authored** — a reaction is a signal to the author). Per chat, it maintains counter properties on the chat object's row, nested under the type container:

| Row path | Scope | Meaning |
|----------|-------|---------|
| `chat.unreadCount` | local | unread messages |
| `chat.unreadMentions` | local | unread mentions of you |
| `chat.unreadReactionsCount` | local | unread reactions on your messages |
| `chat.notifyMode` | account | per-chat push preference: `all`, `mentions` or `none`; absent means inherit the space's `settings.notifyMode` (see [push](../notifications/push.html)) |

A counter is absent until first materialized — treat absent as 0, and read it at `chat.unreadCount`, never top-level (a top-level path reads 0 forever). Rules that follow:

- **Never count unread client-side.** Badge from the row; filter messages with `{"unread": true}`.
- **Your own messages are born read** on every one of your devices. Never call a read endpoint after sending.
- Edits never re-flag `unread`. The one exception is mentions: an edit whose new text mentions you sets `unreadMention`, and repeated re-edits collapse into one live mention entry.
- Reactions badge the message's author, nobody else. A reaction added and removed before you looked leaves no trace.

**Marking read — the viewport rule.** `…/:msgId/read` means "I have seen this message and everything above it" (in `_ver.id` order). Track the newest fully visible message; when it changes and stays visible for about a second, POST that one call. On opening a chat scrolled to the bottom, that clears the chat; reserve `read-all` for an explicit "mark as read" affordance — don't fire it merely because the chat opened. `…/:msgId/reactions-read` clears unread reactions on one message, which `/read` can never cover because a reaction is a change ordered after its target. Mark the visible messages read first: under the hood it also marks read the reaction's causal ancestry, i.e. unread messages the reactor had already seen. All three return `204`, are idempotent and forward-only, work offline, and sync across your devices — flags clear and counters drop as ordinary `updated` frames on your subscription, whichever device did the marking.

**The unread divider.** The first unread message is one index-backed query:

```json
{ "objectId": "<chatId>", "dataset": "chat_messages",
  "filter": {"unread": true}, "sort": ["_ver.id"], "limit": 1 }
```

Unread can appear mid-history: a message written offline by another member can slot between messages you already read once it syncs, so there can be more than one unread region and `unreadCount` can be nonzero while the bottom of the chat is read. The divider query finds the first region wherever it is.

## Mentions

A mention is a markdown link whose destination is a mention URI (see [links](links.html)):

```
Hey [Zarko](any://m/<spaceId>/<identity>), take a look
```

The server derives `mentions: ["<identity>", …]` on every message at write time from two sources: every `any://m/…` link in the text (deduped, first-occurrence order) and — because a reply is a ping — the replied-to message's creator. Clients never write the array; text is the source of truth, so a spoofed array can neither silent-ping nor suppress a real ping. It is capped at 64 identities, and the reply fold-in always survives the cap. "All my mentions" and "my unread mentions" are the same shapes as the unread queries: `{"mentions": "<myIdentity>"}` sorted `-_ver.id`, and `{"unreadMention": true}` sorted `_ver.id` with `limit: 1`. Self-mentions list you (an objective fact of the message) but never badge you.

## Chat list and notifications

Each chat object's row already carries its counters, so a chat list is a plain objects query — `{"sort": ["-chat.unreadCount"]}` sorts unread-first, and a thousand chats cost one query. Do **not** subscribe to every chat to detect new messages. One space-wide objects subscription covers them all:

```json
{ "filter": {"any.types": {"$in": ["<chat-declaring type ids>"]}}, "limit": 0 }
```

The type ids are the `owners` of `chat_messages` in `GET /v1/spaces/:spaceId/datasets` — every type whose part declares the chat module; resolve them once per space and match per element of `any.types` with `$in`.

Counter went up → new unread in that chat; fetch `{"unread": true}` sorted `-_ver.id` with a small limit and toast only messages above a last-notified `_ver.id` you keep locally. Counter went down → the user read it somewhere (this window, another window, another device) — dismiss that chat's notifications. Total live surface for a desktop client: one objects subscription per space, one `/query/subscribe` for the open chat.

> **Note.** `chat_messages` keeps no [version history](../database/version-history.html): the `/history` endpoints never list chat changes, and there is no per-message edit timeline — clients render live records only (an edit replaces `text`, a delete tombstones).

## Things not to do

- Don't call read endpoints on message receipt, on notification display, or unconditionally on chat open — only on actual visibility.
- Don't maintain your own read cursor or counters; the SDK's state is the durable, multi-device one.
- Don't infer other members' read state: there is none.
- Don't write `unread` or the counter properties — they are SDK-owned local fields and writes are rejected.
