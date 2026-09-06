# Chat client guide

How to build a messenger UI on the chat API: rendering, liveness, and
— the part that's easy to get wrong — read tracking. Endpoint shapes
live in `03-api.md § Chat`; this doc is about using them correctly.

## Finding the chat object for a space

A space has **one chat** — the general chat — installed by the
server's catalog. Set the usecase up and use the root the server
hands back:

```
POST /v1/catalog/general-chat/setup
{ "spaceId": "<spaceId>" }
→ 200 { "usecase": "general-chat", "bundles": [ { "id": "system:general-chat/v1",
        "bundle": { "rootId": "<chat object>", "derived": true, ... },
        "installed": true|false, "typeId": "<chat object>" } ] }
```

The install is a bundle whose root is its own type with a part that
declares the `chat` module (`03-api.md` § Parts and modules), so the
root holds `chat_messages` from the first write. Use `rootId` as the
`<objectId>` in every endpoint below. The call is adopt-or-install and
idempotent: the first caller installs, every later caller — any
member, any device, online or not — gets the same root, because the
root is **derived**: its id is a function of the space and the bundle
id, computed offline. That is what makes the chat unforkable, which
it has to be — chat content cannot be merged across objects (`creator`
and `createdAt` are stamped from the change envelope, so copying
messages into another object re-attributes and re-times every one of
them). Two sides of a 1-1 install their own copy on the first attempt
and meet on the one object.

Two consequences:

- **The chat is permanent.** A derived root cannot be deleted, so the
  bundle id stays bound to it for the space's lifetime.
- **There is no other chat.** The `chat` module is reserved to the
  server: a client part, dataset or bundle naming it is `400
  dataset.module_reserved`, and the general-chat root is the only
  object that may carry its type — creating or attaching another
  object with it is `400 type.reserved_carrier`. `POST /objects` never
  makes a chat. Per-collection chats (a client's own chat types) wait
  on per-collection push topics and read tracking (`07-roadmap.md`).

## The model in four sentences

Messages are records on the chat object's `chat_messages` dataset,
ordered by `_ver.id` (set at creation, never changed by edits). Writes
go through the chat endpoints; ALL reads and liveness go through
`/query` and `/query/subscribe`. Read state is tracked by the SDK,
**private to the account** (synced across the account's devices, never
visible to other members — no read receipts, by design), and
**forward-only**: a message once read never becomes unread again.
Everything a client renders is materialized into ordinary queryable
fields — you never compute read state yourself.

## Timestamps

`createdAt` / `modifiedAt` and every reaction leaf
(`reactions.<emoji>.<accountId>`) are instants:
`{"$date": "2026-05-01T21:00:00.000Z"}`. Unwrap the one key
(`new Date(v.$date)`), and compare instants to instants — an edited
message is one whose `modifiedAt` is later than its `createdAt`.

**Tolerate a number there.** The stamps are derived on each device from
the change envelope, so a peer running an older build materializes the
same message as unix seconds, and rows a device wrote before it upgraded
read that way until the SDK's re-index reaches them. The wire
`DataVersion` deliberately does not gate this — bumping it would park
every chat change on peers that don't know the new version, i.e. stop
messages syncing to protect a field's shape. So a client that may talk
to mixed builds accepts both forms:

```js
const ms = v && typeof v === 'object' ? Date.parse(v.$date) : v * 1000;
```

## What the SDK maintains for you

Per message (local fields on the record, visible in query results and
subscribe frames):

| field | meaning |
|---|---|
| `unread: true` | the message itself is unread. Absent (not `false`) once read — filter with `{"unread": true}`. |
| `unreadMention: true` | unread mention of you — the message's derived `mentions` contains your identity (a text `any://m/…` link or a reply to your message; see § Mentions). Sets alongside `unread` on new messages, and on its own when an edit adds a mention of you. |
| `unreadReactions: true` | someone reacted to this message and you haven't seen it. Only ever sets on messages **you authored** — a reaction is a signal to the message's author, so it badges only them. |

Per chat (properties on the chat object's row, present in any
object query — this is your chat list). Like every type-declared
property, the values live under the type's container on the row —
read them at `chat.<property>`, NOT top-level:

| row path | scope | meaning |
|---|---|---|
| `chat.unreadCount` | local | unread messages |
| `chat.unreadMentions` | local | unread mentions |
| `chat.unreadReactionsCount` | local | unread reactions |
| `chat.notifyMode` | account | per-chat push preference: `all` \| `mentions` \| `none`. Client-written via `POST /v1/spaces/:s/properties/:chatObjectId/set/chat`, synced across the account's devices, invisible to other members. Not enum-enforced — treat absent/garbage as "inherit the space-level `settings.notifyMode`" (default `all`). Consumed by the push subscription sync loop (`internal/push`; full push contract in `docs/20-push.md`). |

A counter is absent from the row until the SDK first materializes it
— treat absent as 0. (A top-level `unreadCount` never exists; probing
that path reads 0 forever and looks exactly like "counters are
broken".)

Rules that follow:

- **Never count unread client-side.** Badge from the row's
  `chat.unreadCount`; filter messages with `{"unread": true}`. The SDK
  keeps both consistent, including across devices and after deletes.
- **Your own messages are born read** — on every one of your devices
  (read state is per *account*, not per device). Never call a read
  endpoint after sending.
- Edits never re-flag a message's `unread`. The one exception is
  mentions: an edit whose new text mentions you sets `unreadMention`
  (a fresh ping is a ping), and repeated re-edits collapse into ONE
  live mention entry, never N. An edit that removes your mention
  clears an edit-added `unreadMention`; it cannot clear the `unread`
  of the original message. Deleting an unread message silently drops
  it from flags and counters.
- **Reactions badge the message's author, nobody else.** Someone
  reacting to a third party's message never sets your
  `unreadReactions` or bumps your `chat.unreadReactionsCount` — the
  reaction still syncs and renders, it just isn't *your* unread.
- A reaction added and removed before you looked leaves no trace.

## Marking read: when and how

Three endpoints:

```
POST /v1/spaces/:spaceId/objects/:objectId/chat/read-all
POST /v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/read
POST /v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/reactions-read
```

`…/:msgId/read` means **"I have seen this message and everything above
it"** — it covers the message and everything ordered before it
(`_ver.id` order, i.e. the order you render). It does NOT cover newer
unread activity targeting older messages (a fresh reaction on a
message above the line stays unread — correct: the user hasn't seen
it).

`…/:msgId/reactions-read` clears the unread **reaction(s)** on that one
message. It exists because a reaction is a change ordered *after* its
target message, so `…/:msgId/read` (which cuts at the message's own
`_ver.id`) can never cover a reaction on that same message — without
this route, seeing a reaction never clears its `unreadReactions` badge;
only a *newer* message advancing the read cursor past the reaction
would. Call it when the user has actually seen the reaction (e.g. the
reacted message — almost always one they authored — scrolled into
view). It is a no-op (still `204`, never `404`) on a message with no
unread reactions.

**Scope caveat — it also advances message read state.** Under the hood
this marks the reaction change read, and marking a change read covers
its whole causal ancestry — so it *also* clears unread **messages** the
reactor had already seen when they reacted (everything causally before
the reaction). Messages that arrived *after* the reaction stay unread
(they aren't ancestors), which is what still separates it from
`read-all`. In the target case — a reaction on an already-read message —
the ancestry holds no unread messages, so only the reaction clears. When
unread messages coexist, mark the visible ones read first (the viewport
`/read` rule below) so the only thing this can clear beyond the reaction
is messages the user has already seen. Concretely: don't jump the user
to an old reaction and fire `reactions-read` while newer unread messages
sit below unseen — those get marked read too. It is **not** a way to
dismiss a reaction while preserving unread messages the reactor had
already seen.

**When to call it — viewport rule.** Mark read what the user has
actually seen, nothing more:

- While the user is in the chat and messages are visible, track the
  **newest fully-visible message**. When it changes, debounce (~1s of
  it staying visible) and POST `…/:thatMsgId/read`. One call covers
  the whole visible run and everything above — never call per message.
- On opening a chat **scrolled to the bottom** (the common case), the
  newest message is visible → after the debounce, that call clears the
  chat. `read-all` is equivalent here; reserve explicit `read-all` for
  a "mark as read" affordance (chat list context menu, "mark all"
  button) — do NOT fire it merely because the chat was opened. If the
  user lands mid-history (via the unread divider or a link), only the
  visible boundary gets marked.
- The endpoints are idempotent and forward-only: re-sending an older
  boundary is a harmless no-op. Don't bother deduplicating beyond the
  debounce.
- Works offline: the mark applies locally at once (flags/counters
  update immediately) and syncs to your other devices when
  connectivity returns.

**What you'll observe after marking**: `updated` frames on your
subscription for each message whose `unread`/`unreadReactions` flag
clears, and an update of the chat row's counter properties. Drive the
UI from those events, same as any other change — don't locally
predict-and-patch.

**Marks from your other devices** arrive the same way: flags clear and
counters drop without any local action. Handle it identically (it is
literally the same event flow).

## The unread divider and "jump to first unread"

First unread message (the divider position / scroll target):

```
POST /v1/spaces/:spaceId/query
{ "objectId": "<chatId>", "dataset": "chat_messages",
  "filter": {"unread": true},
  "sort": ["_ver.id"], "limit": 1 }
```

Then load a window of messages around its `_ver.id` (range filters on
`_ver.id`, same as normal pagination) and place the "New messages"
divider above it. The newest unread (for a "↓ new messages" jump-down
pill) is the same query with `"sort": ["-_ver.id"]`.

**Unread can appear mid-history.** Ordering is collaborative: a
message written offline by another member can slot *between* messages
you already read once it syncs. The SDK marks it unread even though
newer messages below it are read — so there can be more than one
unread region, and `unreadCount` can be nonzero while the bottom of
the chat is read. Don't assume "unread ⇒ at the end":

- the divider query above finds the first region wherever it is;
- a badge like "N unread ↑" when the first unread is above the
  viewport is honest and cheap (`unreadCount` + the first-unread
  position);
- when the user scrolls a mid-history unread message into view, the
  normal viewport rule marks it — but note the boundary call covers
  *everything above it*, which is what the user now has seen anyway.

The unread filter is index-backed (`unread` is sparse-indexed with a
`_ver.id` tiebreak), so these queries stay cheap no matter how long
the history is.

## Mentions

A mention is a markdown link whose destination is a mention URI
(docs/19-links.md; build it with `anyuri.BuildMention`):

```
Hey [Zarko](any://m/<spaceId>/<identity>), take a look
```

The server derives a `mentions: ["<identity>", …]` array on every
message at change materialization — **clients never write it** (it's
`ScopeDerived`; a direct write is rejected, and text is the source of
truth, so a spoofed array can neither silent-ping nor suppress a real
ping). Two sources, one array:

- every `any://m/…` link in the text (deduped, first-occurrence
  order);
- **replies count as mentions**: the replied-to message's creator is
  folded in at write time, so "all pings for me" is one field check —
  reply-derived entries are not distinguished from text mentions.

Edits re-derive the array from the new text. A message that mentions
nobody carries no `mentions` field at all. The array is capped at 64
identities (post-dedup; far above anything real — a defense against
link-stuffing), and the reply fold-in always survives the cap: when
it's hit, the last text mention yields the slot, so stuffing a reply
with links can't squeeze the replied-to author out of their ping.

Recipes — all index-backed (`mentions` is a sparse multikey index with
a `_ver.id` tiebreak; use the equality form, not `$exists`):

```
# All my mentions, newest first:
POST /v1/spaces/:spaceId/query
{ "objectId": "<chatId>", "dataset": "chat_messages",
  "filter": {"mentions": "<myIdentity>"},
  "sort": ["-_ver.id"], "limit": 20 }

# My unread mentions (divider / jump target — same shape as unread):
{ …, "filter": {"unreadMention": true}, "sort": ["_ver.id"], "limit": 1 }
```

Badge from the row's `chat.unreadMentions`, exactly like
`chat.unreadCount`. Self-mentions and self-replies list you in
`mentions` (it's an objective fact of the message — filter with
`creator != me` if you don't want them) but never badge you: your own
writes are born read.

Notes: pre-existing messages carry no `mentions` until they're edited
(derivation happens at write time; stored history is never rewritten).
Rendering the "magic" mention chip — resolving the current display
name via `GET /v1/identities`, falling back to the link text snapshot
— is described in docs/19-links.md § Mentions.

## Chat list

Query the chat objects as usual; each row already carries
`chat.unreadCount` / `chat.unreadMentions` /
`chat.unreadReactionsCount`, so sorting "unread first" or badging is
a plain filter/sort on the list query (nested paths work everywhere:
`{"sort": ["-chat.unreadCount"]}`), and live updates arrive through
the normal objects subscription. No per-chat calls, no chat opens — a
thousand chats cost one query.

## Desktop notifications (no per-chat subscriptions, no server help)

Do NOT subscribe to every chat to detect new messages — each
subscription holds a record window and mailbox, and puts event-build
work on every apply. There is also no notification service: a desktop
client (e.g. a Tauri wrapper with its own local store) builds OS
notifications itself, entirely over the existing HTTP surface. The
trick is that the SDK already funnels "something notify-worthy
happened" into the chat rows' counter properties — so ONE space-wide
subscription covers every chat:

```
POST /v1/spaces/:spaceId/objects/query/subscribe
{ "filter": {"any.types": {"$in": [<chat-declaring type ids>]}}, "limit": 0 }
```

The type ids are the `owners` of `chat_messages` in
`GET /v1/spaces/:spaceId/datasets` — every type in the space whose
part declares the chat module; resolve them once per space (re-read
when a part is added) and match per element of `any.types` with `$in`.

Each frame's `updated` entries carry the full row and the ops — watch
for `chat.unreadCount` / `chat.unreadMentions` changes:

- **Counter went up** → new unread in that chat. Fetch what to show:
  `POST /query` on that chat, `{"unread": true}`, sort `-_ver.id`,
  small limit. Keep a last-notified `_ver.id` per chat in the client's
  local store and toast only messages above it — that marker is your
  entire notification cursor.
- **Counter went down** → the user read it here, in another window, or
  on another device — dismiss that chat's OS notifications. Cross-
  device dismissal falls out for free.
- The counters already encode the semantics, so no client-side
  filtering is needed: your own messages never bump them (born read),
  edits never bump them, deleting an unseen message drops them.

Boot policy: on start, take the subscription's snapshot as badge
state — don't toast the offline gap (the last-notified markers make
this automatic). On SSE overflow/drift, resubscribe; the snapshot
frame re-establishes badges and the markers keep toasts deduplicated.
Space add/remove: re-list `/spaces` on its own events or a slow poll.

Total live surface for a desktop client: one objects subscription per
space, one `/query/subscribe` for the currently OPEN chat, plus a
last-notified marker per chat in local storage. Nothing per closed
chat, nothing server-side.

(In-process consumers with SDK access — the server itself, native
wrappers — can use the per-space read-state feed instead:
`ReadState().Subscribe` + `ChangedSince(cursor)` yields dirty objects;
`UnreadSnapshot(objectId)` is the per-object pull to diff against.
Same semantics as the HTTP recipe, one hop closer to the engine.)

## Things not to do

- Don't call read endpoints on message receipt, on notification
  display, or unconditionally on chat open — only on actual
  visibility.
- Don't subscribe to all chats to build notifications or badges — see
  § Desktop notifications; subscriptions are for the open chat only.
- Don't maintain your own read cursor or counters in client storage;
  the SDK's state is the durable, multi-device one. A client cache of
  rendered flags is fine — it gets corrected by events.
- Don't infer read state of OTHER members: there is none. Read state
  is private; the protocol carries no read receipts.
- Don't treat `unread` as a boolean field to write — the flag fields
  and counter properties are SDK-owned (local scope, handler-only);
  writes to them are rejected.

## Current limitations

- A freshly linked device lands on the account's real read state
  (your other devices' synced read positions apply, so a chat your
  phone shows unread is unread here too). Only when no device ever
  published read state for a chat — or in the rare case its read
  positions haven't synced yet at first open — does it start with
  everything read and converge at the next mark.
- `chat_messages` has **no version history** (`SkipHistory`, SYN-86):
  the `/history` endpoints never list chat changes, and there is no
  per-message edit timeline or "view original" — clients render live
  records only (an edit replaces `text`, a delete tombstones). The
  DAG still retains every change, so this is revertible with a
  backfill if a history UX ever materializes.
