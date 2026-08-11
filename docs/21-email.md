# Email — synced mail as a dataset

The built-in `email` type stores mail mirrored from an external
provider (gmail first) as **one record per message on a per-address
mailbox object's `email_messages` dataset** — the chat model applied
to mail. This document covers the data model, the write surface, and
the sync-rig recipe. Endpoint reference: `03-api.md` § Email.

## Why a dataset, not objects

An object-per-email scheme pays, for every synced message: a full
object create (its own CRDT tree), a nav row, a type attach, and —
when the body is smuggled through `editor_blocks` for indexing — an
editor attach plus a block create. The objects list and nav tree fill
with mail; every label flip is an object write; version history
records all the churn.

Mail is exactly the shape datasets exist for: many small,
mostly-immutable records with one shared lifecycle. As a dataset,
one sync page lands as ONE DAG change, a label flip is a per-path
`$set`, the mailbox is a single object, and a live inbox is a
standard `/query/subscribe` window.

## Model

- **One mailbox object per synced address**, derived from the seed
  `any/email-mailbox/v1/<normalized address>` (trimmed, lowercased) —
  the same deterministic `Objects().Derive` mechanic as the agent
  brain and the per-space general chat. Every device and every
  re-bootstrapped rig computes the same object id: no discovery
  query, no create race. `GET /v1/spaces/:spaceId/email/mailbox`
  exposes the id; the `email` type is attached on first
  materialization.
- **Record id = provider message id** (`[A-Za-z0-9_-]`, ≤ 64 bytes —
  gmail ids fit as-is). The id is the idempotency key: re-ingesting
  an already-stored page creates nothing.
- **Write-once + mutable allow-list.** A provider message's content
  never changes; its label state does. `labelIds` and `historyId`
  are the only mutable fields (author-only, `modifiedAt` bumped);
  everything else rejects modification.
- **`SkipHistory`.** The provider is the source of truth; label churn
  must not accrete version-history rows. The DAG keeps everything.
- **Threads are data, not structure.** Records carry `threadId`; a
  thread view is `{"filter": {"threadId": …}, "sort": ["internalDate"]}`,
  thread listing is an `/aggregate` `$group`. There is deliberately no
  object per thread — most threads are one-message marketing mail, so
  object-per-thread rebuilds the explosion the dataset removes.

## Record shape

See `03-api.md` § Email for the full wire shape. The load-bearing
fields:

| field | class | notes |
|---|---|---|
| `threadId` | synced, required | provider thread id |
| `internalDate` | synced, required | unix ms, provider receipt time — THE sort key |
| `from` / `to` / `cc` / `bcc` / `replyTo` | synced | parsed `{name?, address}` objects, not raw header strings |
| `subject`, `date`, `snippet` | synced | `date` is the RFC header value, display-only |
| `bodyText` | synced | filtered text ONLY — raw HTML / RFC822 is not stored; ≤ 1 MiB sanity bound |
| `labelIds` | synced, **mutable** | whole-array LWW |
| `historyId` | synced, **mutable** | provider change frontier for this message |
| `attachments` | synced | `[{filename, mime, size, fileId?}]` — bytes via files v2 |
| `messageIdHeader`, `inReplyTo`, `references` | synced | RFC-822 threading, for non-gmail providers |
| `creator`, `createdAt`, `modifiedAt` | derived | server-stamped; `createdAt` is ingest time |
| `participants` | derived | normalized addresses from from+to+cc, multikey-indexed |

`participants` is server-derived like chat's `mentions` — a client
payload carrying it is rejected, so the index stays trustworthy. bcc
is excluded (not visible on the message to other mailbox readers).

Chronological order is **`internalDate`**, never `_ver.id`: our sync
order is meaningless for mail (a backfill ingests newest-first, an
incremental sync oldest-first).

`labelIds` is a whole array, not a per-label map, because the multikey
index makes `{"labelIds": "INBOX"}` cheap and one sync writer per
mailbox is the documented assumption. If multi-writer label editing
ever becomes real, the escape hatch is the reactions-style per-leaf
map — a dataVersion bump, not a redesign.

## Indexes

- `(internalDate)` — newest-first inbox paging
- `(threadId, internalDate)` — thread view
- `(labelIds, internalDate)` multikey — label filters
- `(participants, internalDate)` multikey, sparse — per-correspondent

## Reads

The standard dataset read path — no bespoke read endpoints:

```
POST /v1/spaces/:spaceId/query
{ "objectId": "<mailboxId>", "dataset": "email_messages",
  "filter": {"labelIds": "INBOX"}, "sort": ["-internalDate"], "limit": 50 }
```

Live inbox: same body on `…/query/subscribe` — new mail arrives in
`added`, label flips in `updated`, expunges in `removed`. Subscribe
descending with a `limit` so new arrivals enter the window (the chat
rule, `03-api.md` § Chat).

Unread badge without transferring rows:
`{"filter": {"labelIds": "UNREAD"}, "limit": 0, "includeTotal": true}`.

## Search

The email chunker indexes `subject + bodyText` under the dedicated
scope `email` (subject doubles as the BM25F title field). Addressing,
labels, and attachment names are not indexed — `participants` filters
cover the "mail from X" case exactly.

Mail is a **semantic-search** target: `email` is a regular embedded
scope (only `props` is FTS-only), so email docs are marked pending,
embedded by the embed loop, and participate in `hybrid` / `vector`
`/search` modes with no extra wiring:

```
POST /v1/spaces/:spaceId/search
{ "query": "that invoice from the coworking space", "scopes": ["email"] }
```

Known limit (shared with every scope): the local embedder truncates
input to `index.local.contextSize` tokens (default 2048), so a very
long email embeds its head; FTS still covers the full text. See
`13-index.md`.

## Sync-rig recipe

1. `GET /v1/spaces/:s/email/mailbox?address=user@example.com` →
   mailbox object id. Cacheable forever (deterministic).
2. Backfill / incremental sync: page the provider, then
   `POST …/email/messages` with up to 256 messages per call. The
   reply's `created` / `updated` / `unchanged` lists say what
   happened; `rejections` lists what didn't land. **Rejections are
   deterministic refusals** (each carries a `code`: `tombstoned`,
   `not_author`, `immutable_field`, `invalid`) — re-sending the same
   message yields the same refusal forever, so log them and advance
   the cursor over them like any handled message; holding the cursor
   for a rejection wedges the sync permanently. `tombstoned` on a
   message you didn't delete is the one worth alerting on (see
   § Tombstones below).
3. Label-only history rounds: either re-ingest the affected messages
   (the diff writes only the changed labels) or
   `PATCH …/email/messages/:id` per message.
4. Provider-side deletes: `DELETE …/email/messages/:id`.
5. Persist your sync frontier in `email_sync_state` on the same
   mailbox object via the generic modify route, one record per sync
   source:

   ```
   POST /v1/spaces/:spaceId/modify
   { "objectId": "<mailboxId>", "dataset": "email_sync_state",
     "records": [{ "id": "<deviceId-or-source>", "upsert": true,
       "ops": [{ "op": "$set", "path": "",
         "value": {"historyId": "8023406", "syncedAt": 1786466013} }] }] }
   ```

   The dataset is a raw store (no validation, no history) — the rig
   owns the shape. Keying records per source keeps concurrent
   multi-device injection from contending on one record; the message
   upserts themselves are id-idempotent, so two devices ingesting the
   same mail converge regardless.

Attachments: attach bytes to the mailbox object via files v2
(`POST …/objects/:mailboxId/files?name=…`), then record the returned
fileId in the message's `attachments` manifest on ingest.

## Tombstones

Deleting a record consumes its id forever: the CRDT tombstones it,
and a later ingest of the same provider message id is rejected with
`code: "tombstoned"`. This is the intended semantic — record id =
provider id is the idempotency contract, and a delete means *this
provider message is permanently suppressed in this mailbox*. For
provider-side expunges that is exactly right (the provider won't
resurface the message). The corollary: **`DELETE` is not a cleanup
tool** — deleting records the provider still holds makes those
messages unsyncable into this mailbox object for good (a fallback
re-list will meet the tombstone and must advance over it, per the
recipe above). If a mailbox is damaged that way at scale, the reset
is a new mailbox object, not id gymnastics.

## Contacts

Contact/correspondent *entities* stay out of the built-in. The
`participants` index answers the query-side need ("mail with X")
without per-email link maintenance; a rig that wants contact pages
keeps them as its own user type and filters mail by address.

## Deliberately out of scope

- **Raw HTML / RFC822 storage** — filtered text only. Re-fetch from
  the provider if ever needed.
- **Push notifications on new mail** — the push model (20-push.md) is
  sender-initiated, and the "sender" of an email record is the
  account's own sync rig; there is no remote member to push from.
- **Send/compose** — this is a mirror, not an MUA. Outbound mail goes
  through the provider's API on the rig side.
