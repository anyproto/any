# Chat client guide

How to build a messenger UI on the chat API: rendering, liveness, and
— the part that's easy to get wrong — read tracking. Endpoint shapes
live in `03-api.md § Chat`; this doc is about using them correctly.

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

## What the SDK maintains for you

Per message (local fields on the record, visible in query results and
subscribe frames):

| field | meaning |
|---|---|
| `unread: true` | the message itself is unread. Absent (not `false`) once read — filter with `{"unread": true}`. |
| `unreadMention: true` | unread mention of you. (Never set today; arrives with the mentions feature. Wire your badge logic now.) |
| `unreadReactions: true` | someone reacted to this message and you haven't seen it. |

Per chat (local properties on the chat object's row, present in any
object query — this is your chat list):

| property | meaning |
|---|---|
| `unreadCount` | unread messages |
| `unreadMentions` | unread mentions |
| `unreadReactionsCount` | unread reactions |

Rules that follow:

- **Never count unread client-side.** Badge from `unreadCount`; filter
  messages with `{"unread": true}`. The SDK keeps both consistent,
  including across devices and after deletes.
- **Your own messages are born read** — on every one of your devices
  (read state is per *account*, not per device). Never call a read
  endpoint after sending.
- Edits never re-flag a message. Deleting an unread message silently
  drops it from flags and counters.
- A reaction added and removed before you looked leaves no trace.

## Marking read: when and how

Two endpoints:

```
POST /v1/spaces/:spaceId/objects/:objectId/chat/read-all
POST /v1/spaces/:spaceId/objects/:objectId/chat/messages/:msgId/read
```

`…/:msgId/read` means **"I have seen this message and everything above
it"** — it covers the message and everything ordered before it
(`_ver.id` order, i.e. the order you render). It does NOT cover newer
unread activity targeting older messages (a fresh reaction on a
message above the line stays unread — correct: the user hasn't seen
it).

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

## Chat list

Query the chat objects as usual; each row already carries
`unreadCount` / `unreadMentions` / `unreadReactionsCount`, so sorting
"unread first" or badging is a plain filter/sort on the list query,
and live updates arrive through the normal objects subscription. No
per-chat calls, no chat opens — a thousand chats cost one query.

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
{ "filter": {"type": "chat"}, "limit": 0 }
```

Each frame's `updated` entries carry the full row and the ops — watch
for `unreadCount` (later `unreadMentions`) changes:

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

- `unreadMention` never sets yet (no mentions feature); the field,
  counter, and index are already in place.
- A freshly linked device lands on the account's real read state
  (your other devices' synced read positions apply, so a chat your
  phone shows unread is unread here too). Only when no device ever
  published read state for a chat — or in the rare case its read
  positions haven't synced yet at first open — does it start with
  everything read and converge at the next mark.
