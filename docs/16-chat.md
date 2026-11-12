# Chat client guide

How to build a messenger UI on the chat API: rendering, liveness, and
— the part that's easy to get wrong — read tracking. Endpoint shapes
live in `03-api.md § Chat`; this doc is about using them correctly.

## Finding the chat object for a space

Every space has a well-known chat — the general chat — installed by
the server's catalog. Set the usecase up and use the root the server
hands back:

```
POST /v1/catalog/general-chat/setup
{ "spaceId": "<spaceId>" }
→ 200 { "usecase": "general-chat", "bundles": [ { "usecase": "general-chat",
        "id": "system:general-chat/v1",
        "bundle": { "rootId": "<chat object>", "derived": true, ... },
        "installed": true|false, "typeId": "<chat object>", "miniapp": { ... } } ] }
```

The install is a bundle whose root declares its own type with a part
that declares the `chat` module (`03-api.md` § Parts and modules) — a
declaring root hosts its own records, so the root holds `chat_messages`
from the first write — and is filed under the built-in `miniapp`
collection, so the chat shows in the space sidebar next to the other
apps (`03-api.md` § Collections). Use `rootId` as the
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
- **Other chats are ordinary objects.** Any type may declare a part
  `{"module": "chat"}` — alone, or next to an editor part — and every
  object of that type is its own conversation, created with `POST
  …/objects` like any object. Only the general chat is derived and
  shared by construction; a user chat is one object among its type's,
  deletable, and one device's duplicate is a second chat — so is an
  object created with the general chat's own hidden type, which is why
  the general chat is the setup's `rootId`, never "the object of type
  `general_chat`". The module admits one transcript per object, and a
  copy (`from`) of a chat-bearing object starts with an empty one.

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
(`new Date(v.$date)`), and compare instants to instants. They come
from the change envelope's clock, which is second-resolution, so they
are for display: a message edited within the second it was sent
carries equal stamps. Show an "edited" marker off `_ver` instead —
`_ver.text != _ver.id` means the text changed after creation
(`_ver.id` is the creation marker, `_ver.<path>` advances with every
write to that path).

## What the SDK maintains for you

Per message (local fields on the record, visible in query results and
subscribe frames):

| field | meaning |
|---|---|
| `unread: true` | the message itself is unread. Absent (not `false`) once read — filter with `{"unread": true}`. |
| `unreadMention: true` | unread mention of you — the message's derived `mentions` contains your identity (a text `any://u/…` link or a reply to your message; see § Mentions). Sets alongside `unread` on new messages, and on its own when a message that mentions you is edited. |
| `unreadReactions: true` | someone reacted to this message and you haven't seen it. Only ever sets on messages **you authored** — a reaction is a signal to the message's author, so it badges only them. |

Per chat (properties on the chat object's row, present in any
object query — this is your chat list). The values live under the
module's namespace on the row — read them at `chat.<property>`, NOT
top-level:

| row path | scope | meaning |
|---|---|---|
| `chat.unreadCount` | local | unread messages |
| `chat.unreadMentions` | local | unread mentions |
| `chat.unreadReactionsCount` | local | unread reactions |
| `chat.notifyMode` | account | per-chat push preference: `all` \| `mentions` \| `none`. Client-written via `POST /v1/spaces/:s/properties/:chatObjectId/set/chat`, synced across the account's devices, invisible to other members. Not enum-enforced — treat absent/garbage as "inherit the space-level `settings.notifyMode`" (default `all`). Consumed by the push subscription sync loop (`internal/push`; full push contract in `docs/20-push.md`). |

A counter is absent from the row until the SDK first materializes it
— treat absent as 0. There is no top-level `unreadCount`; reading that
path always yields 0.

Rules that follow:

- **Never count unread client-side.** Badge from the row's
  `chat.unreadCount`; filter messages with `{"unread": true}`. The SDK
  keeps both consistent, including across devices and after deletes.
- **Your own messages are born read** — on every one of your devices
  (read state is per *account*, not per device). Never call a read
  endpoint after sending. The one exception is an agent's terminal
  message (`agent.done: true`): it is signed with your key but you have
  not seen it, so it counts unread on every device of yours until read,
  in every space. Its `done: false` bubbles stay born read, so the
  unread divider lands on that final message and the run's earlier
  bubbles sit above it as read.
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
message. A reaction is a change ordered *after* its target message, so
`…/:msgId/read` (which cuts at the message's own `_ver.id`) never
covers a reaction on that same message; only this route, or a *newer*
message advancing the read cursor past the reaction, clears its
`unreadReactions` badge. Call it when the user has actually seen the
reaction (e.g. the reacted message — almost always one they authored —
scrolled into view). It is a no-op (still `204`, never `404`) on a
message with no unread reactions.

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
sit below unseen — those get marked read too.

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
counters drop without any local action.

## The unread divider and "jump to first unread"

First unread message (the divider position / scroll target):

```
POST /v1/spaces/:spaceId/query
{ "objectId": "<chatId>", "dataset": "chat_messages",
  "filter": {"unread": true},
  "sort": ["_ver.id", "id"], "limit": 1 }
```

Then load a window of messages around its `_ver.id` (range filters on
`_ver.id`, same as normal pagination) and place the "New messages"
divider above it. The newest unread (for a "↓ new messages" jump-down
pill) is the same query with `"sort": ["-_ver.id", "-id"]`.

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

A mention is a markdown link whose destination is an identity URI
(docs/19-links.md § `u`; build it with `anyuri.BuildIdentity`). Every
`any://` reference in a message — the text's links and mentions, each
attachment's `link`, the agent group's `debugLink` — also lands in
the link index, so "which messages mention this member" and "which
chats link this page" are backlinks reads (`GET /v1/backlinks?target=`,
`GET …/objects/:o/backlinks`; docs/03-api.md § Links and backlinks).
Mentions in the text:

```
Hey [Zarko](any://u/<identity>), take a look
```

The server derives a `mentions: ["<identity>", …]` array on every
message at change materialization — **clients never write it** (it's
`ScopeDerived`; a direct write is rejected, and text is the source of
truth, so a spoofed array can neither silent-ping nor suppress a real
ping). Two sources, one array:

- every `any://u/…` link in the text (deduped, first-occurrence
  order; the legacy `any://m/<spaceId>/<identity>` form counts too);
- **replies count as mentions**: the replied-to message's creator is
  folded in at write time, so "all pings for me" is one field check —
  reply-derived entries are not distinguished from text mentions.

Edits re-derive the array from the new text. A message that mentions
nobody carries no `mentions` field at all. The array is capped at 64
identities (post-dedup; a defense against link-stuffing), and the
reply fold-in always survives the cap: when it's hit, the last text
mention yields the slot, so stuffing a reply with links can't squeeze
the replied-to author out of their ping.

Recipes — all index-backed (`mentions` is a sparse multikey index with
a `_ver.id` tiebreak; use the equality form, not `$exists`):

```
# All my mentions, newest first:
POST /v1/spaces/:spaceId/query
{ "objectId": "<chatId>", "dataset": "chat_messages",
  "filter": {"mentions": "<myIdentity>"},
  "sort": ["-_ver.id", "-id"], "limit": 20 }

# My unread mentions (divider / jump target — same shape as unread):
{ …, "filter": {"unreadMention": true}, "sort": ["_ver.id", "id"], "limit": 1 }
```

Badge from the row's `chat.unreadMentions`, exactly like
`chat.unreadCount`. Self-mentions and self-replies list you in
`mentions` (it's an objective fact of the message — filter them out
client-side if you don't want them, but keep messages carrying `agent`)
but never badge you: your own writes are born read. An agent's terminal
message is the exception, so
an agent that mentions you bumps `chat.unreadMentions` too — including
through the reply fold-in, when it replies to one of your messages.

Rendering the mention chip — resolving the current display name via
`GET /v1/identities`, falling back to the link text snapshot — is
described in docs/19-links.md § `u` — identities.

## Chat list

Query the chat objects as usual; each row already carries
`chat.unreadCount` / `chat.unreadMentions` /
`chat.unreadReactionsCount`, so sorting "unread first" or badging is
a plain filter/sort on the list query (nested paths work everywhere:
`{"sort": ["-chat.unreadCount", "-id"]}`), and live updates arrive through
the normal objects subscription. No per-chat calls, no chat opens — a
thousand chats cost one query.

## Desktop notifications (no per-chat subscriptions, no notification service)

Do NOT subscribe to every chat to detect new messages — each
subscription holds a record window and mailbox, and puts event-build
work on every apply. There is no notification service either: a
desktop client builds OS notifications itself over the existing HTTP
surface. The SDK already funnels "something notify-worthy happened"
into the counters on the row of whichever object hosts the chat, so
ONE bounded space-wide subscription covers every chat with something
unread:

```
POST /v1/spaces/:spaceId/objects/query/subscribe
{ "filter": {"$and": [
    {"$or": [{"chat.unreadCount":          {"$gt": 0}},
             {"chat.unreadMentions":       {"$gt": 0}},
             {"chat.unreadReactionsCount": {"$gt": 0}}]},
    {"any.collections": {"$nin": ["bin", "template"]}}]},
  "sort":   ["-modifiedAt", "-id"],
  "limit":  1000 }
```

How the counters move through that window:

- They are local-scope values on the hosting row — an ordinary chat
  object, the general chat's root, a user type's own row — so the
  filter needs no list of chat-declaring types. The bin clause is the
  one every ordinary list carries: a binned chat leaves the window.
- A counter is absent until it first turns non-zero, and absent never
  matches `$gt: 0`: a chat nobody wrote to never enters the window.
- A counter write never bumps `modifiedAt`.
- A counter write re-evaluates the filter. 0 → N arrives as `added`,
  a change between non-zero values as `updated`, and every counter
  back at 0 as `removed` with reason `filtered-out`.

Keep `limit` above the number of chats with something unread; the
window then holds every one of them.

Each frame's `added` / `updated` entries carry the full row and the
ops. A frame that touches one of the three counters — a row `added`,
a counter moving up or down, a row `removed` — says the unread set of
that chat changed: something arrived, something left, or both. Do two
things on every such frame.

**Ask the feed what arrived:**

```
GET /v1/spaces/:spaceId/read-state/unread?cursor=<cursor>&limit=100

{ "changes": [ { "objectId":  "<chatId>",
                 "dataset":   "chat_messages",
                 "recordIds": ["<msgId>"],
                 "tags":      ["message"],
                 "changeId":  "<changeId>" } ],
  "cursor": "<cursor>" }
```

Each change is one thing that became unread on this device after the
cursor and still is, oldest arrival first. `objectId` is the chat,
`recordIds` the message it touched, `tags` the reason:

| tags | what happened |
|---|---|
| `message` | a new message |
| `message`, `mention` | a new message that mentions you |
| `mention` | a message that mentions you was edited |
| `reaction` | someone reacted to your message |

- Fetch the messages by id (`POST /query` on the chat,
  `{"id": {"$in": [...]}}`) for the notification text, and keep the
  returned cursor.
- A page as long as the limit in effect — yours, at most 1000 — may
  have more behind it: call again.
- Keep one notification per message. A change for a message whose
  notification is showing updates that notification.
- An edit of a message that mentions you is a change every time: a
  typo fix counts, and so does an edit of a message that mentioned you
  from the start.
- A `reaction` change names the message only. The feed does not say
  who reacted or with which emoji.

The feed covers every chat of the space, so three kinds of change are
dropped, not shown:

- the chat is open and visible — the user is looking at it;
- the chat is in `bin` or `template` (`any.collections` on its row):
  the subscription leaves those out, the feed does not;
- the chat is muted: its notify mode is `none`, or it is `mentions`
  and the change carries no `mention` tag. The mode is
  `chat.notifyMode` when that is `all`, `mentions` or `none`,
  otherwise the space's `settings.notifyMode`, otherwise `all`.

**Check what left.** A notification is over when its message is
deleted or carries none of `unread`, `unreadMention` and
`unreadReactions`. Re-check a chat's notifications when one of its
counters fell. `removed` ends all of them — reason `filtered-out`:
nothing unread any more, or the chat moved to `bin` or `template`;
`deleted`: the chat is gone — and so does the space going away.
Cross-device dismissal falls out for free: a read on another device
clears the flags and lowers the counters here.

The counters already encode the semantics, so no client-side filtering
is needed: your own messages never bump them (born read), an agent's
`done: true` reply under your key does, an edit moves only
`chat.unreadMentions` (up when its text mentions you, down when it
drops an edit-added mention), deleting an unseen message drops them.
"Your agent replied" rides this same subscription in every space — no
per-chat streams.

The cursor is all the client keeps to know what is new — one per
space, held in memory:

- Make the first call without one, before subscribing. It returns no
  changes and a cursor at the present, so nothing that was already
  unread on this device is shown.
- Send back the cursor the last call returned. It is opaque, and the
  server keeps nothing between calls.
- Keep one call in flight per space. A frame that arrives meanwhile
  means one more call when it returns.
- Don't persist it across app starts: a stored cursor shows everything
  that arrived while the app was closed.
- It stays valid across a resubscribe and a server restart. When the
  account's local store was rebuilt, the server answers an old cursor
  as it answers none.

The feed orders changes by arrival on this device, which the chat's own
order cannot do: a message another member wrote offline merges between
messages you already have, and an edit-added mention or a reaction
creates no message at all. What that means at the edges:

- **A device that is catching up lists history.** Freshly linked, or
  back online, it applies old changes as they reach it. What the
  account has not read is listed. What it read on another device is
  listed too until that read position arrives here; the counters then
  fall and the check above ends those notifications. How many of them
  to show is the client's call; the cursor moves on either way.
- **Flags and counters trail the feed.** The feed lists a change as
  soon as it is applied. The flags and counters follow in a debounced
  pass (50 ms, plus the pass itself), so a check made in between finds
  an unread message without a flag.
- **A still counter fires no frame.** When something leaves a chat's
  unread set in the same pass as something enters it and the counter
  ends where it was, no frame fires. The arrival is listed by the next
  call, whenever another frame causes one.

Boot policy: make the first feed call, subscribe, and take the
subscription's snapshot as badge state — every chat in it has
something unread, and a chat absent from it has nothing. Then call the
feed again: it returns what arrived in between. Don't toast the
offline gap. On any `closed`, resubscribe; the new snapshot replaces
the badge set. Then check every notification that is showing and call
the feed with the cursor you hold: it returns what became unread while
the stream was down. Space add/remove:
`POST /v1/spaces/query/subscribe`.

Total live surface for a desktop client: one objects subscription per
space, one `/query/subscribe` for the currently OPEN chat, plus one
feed cursor per space in memory. Nothing per closed chat, nothing
stored, nothing server-side.

(In-process consumers with SDK access call `ReadState().UnreadSince`
directly. `ReadState().Subscribe` pings them after every read-state
change.)

## Things not to do

- An agent never calls the read endpoints on a chat it serves. Its
  server is one of the owner's devices, so a read-all after posting
  would clear its own `done: true` reply on every device.
- Don't call read endpoints on message receipt, on notification
  display, or unconditionally on chat open — only on actual
  visibility.
- Don't subscribe to all chats to build notifications or badges — see
  § Desktop notifications; subscriptions are for the open chat only.
- Don't decide what is new from `_ver.id`. It is a message's place in
  the chat, not when it arrived: a message merged into mid-history
  sorts below ones already notified. The unread feed is the only
  source of "became unread".
- Don't maintain your own read cursor or counters in client storage;
  the SDK's state is the durable, multi-device one. A client cache of
  rendered flags is fine — it gets corrected by events.
- Don't infer read state of OTHER members: there is none. Read state
  is private; the protocol carries no read receipts.
- Don't write the flag fields or counter properties. They are
  SDK-owned local-scope values: a synced write to them is rejected,
  and a local-scope write is overwritten by the SDK's next
  materialization pass. Mark read through the read endpoints.

## Current limitations

- A freshly linked device lands on the account's real read state
  (your other devices' synced read positions apply, so a chat your
  phone shows unread is unread here too). Only when no device ever
  published read state for a chat — or when its read positions
  haven't synced yet at first open — does it start with everything
  read and converge at the next mark.
- `chat_messages` has **no version history** (`SkipHistory`): the
  `/history` endpoints never list chat changes, and there is no
  per-message edit timeline or "view original" — clients render live
  records only (an edit replaces `text`, a delete tombstones).
