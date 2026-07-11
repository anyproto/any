# Task: push notifications (SYN-47)

Linear: [SYN-47](https://linear.app/anyorg/issue/SYN-47/push-notifications)
· branch `cheggaaa/syn-47-push-notifications`

**Status: IMPLEMENTED through M5** (2026-07-11) — SDK `pushclient` +
`settings` subtree (M1, branch `cheggaaa/syn-47-push-client`),
`internal/push` service + config + token/settings endpoints (M2),
per-chat `notifyMode` + effective-mode sync loop (M3), chat
send/edit/read hooks (M4), CLI + docs (`docs/20-push.md`) + gated e2e
(`internal/e2e/push_test.go`, `ANY_PUSH_E2E_PEER_ID`/`_ADDRS`) (M5).
**Remaining:** the staging/production push-node address (infra
hand-off, config-only) + running the e2e against real infra. The
research notes below are the design record; contract claims were
verified against real code, file:line cited.

Bases:
- `any` side: this branch
  (`cheggaaa/syn-72-chat-server-derived-mentions-field-text-parse-reply-fold-in`,
  worktree `~/projects/any-syn75`) — needs the server-derived `mentions`
  field on `chat_messages` (SYN-72).
- SDK side: `cheggaaa/syn-72-handler-record-getter`
  (worktree `~/projects/any-sync-sdk-syn72`).
- Infra: the existing `../anytype-push-server` (same deployment heart
  uses). No server-side changes needed.

## Model (how heart does it — we interop, not reinvent)

**Sender-pushes, E2E-encrypted.** The push server
(`anytype-push-server`, DRPC `pushproto.Push`) never sees plaintext: the
*sender's* node builds recipient topics, encrypts the payload with a
space-derived AES key, signs the ciphertext with the account key, and
calls `Notify`. The server verifies the caller's any-sync secure-channel
identity + per-topic signatures, then fans out opaque ciphertext to
FCM/APNs tokens of accounts subscribed to matching topics. Receive-side
decryption is entirely a mobile-client concern (NSE / Android
extension) — `any` owns only the wire contract.

**Dialing.** The push server is a DIRECT peer (`{peerId, addr}` from
client config), *not* in nodeconf. heart:
`core/pushnotification/pushclient/client.go` —
`peerService.SetPeerAddrs(cfg.PeerId, cfg.Addr)` + `pool.GetOneOf` +
`peer.DoDrpc`. Auth = the secure channel's verified account identity
(`peer.CtxPubKey(ctx).Account()` server-side, `push/push.go:63` etc.).

### Crypto (heart `space/internal/components/aclobjectmanager/pushnotificationkeys.go:16-33`)

Both keys are **derived from ACL state, never stored** (heart's
spaceView relations are just a cache):

| Key | Derivation | Properties |
|---|---|---|
| space push key (Ed25519) | `privkey.DeriveFromPrivKey("m/99999'/1'", aclState.FirstMetadataKey())` (SLIP-10) | identical for **every member** (any-sync `unpackAllKeys` walks the read-key chain to the root, so joiners derive it too); signs topics + `CreateSpace`/`RemoveSpace`; its pubkey raw bytes = the server's space identifier |
| payload enc key (AES) | `crypto.DeriveSymmetricKey(aclState.CurrentReadKey().Raw(), "m/SLIP-0021/anytype/space/key")` (SLIP-21) | rotates with ACL read-key rotation; `keyId = hex(sha256(encKeyRaw))` routes decryption on the receiver |

⇒ the push key is a **per-space shared secret**, not account-private.
`any` stores nothing; the SDK re-derives on demand.

### Topic vocabulary (heart `core/block/chats/service.go:593-604`; byte-identical for interop)

Sender publishes the superset per message:
1. `chats` — space-wide "all messages"
2. `chats/<sha256hex(chatObjectId)>` — per-chat "all"
3. `chats/<sha256hex(chatObjectId)>/<mentionIdentity>` — per-chat mention
4. `<mentionIdentity>` — bare identity (bulk mentions-mode subscribers)

`groupId = sha256hex(chatObjectId)` (collapses notifications per chat).
Subscribe-side mode mapping (heart `topics.go:102-133`): mode **all** →
topics 1+bare-identity; **mentions** → bare-identity only; per-chat
overrides use the per-chat variants. Topic sig =
`pushKey.Sign([]byte(topicString))`.

### Payload (heart `core/block/chats/chatpush/push.go:9-32`; keep JSON field names verbatim)

```json
{"spaceId":"…","spaceUxType":0,"spaceType":0,"senderId":"…","type":1,
 "newMessage":{"chatId":"…","msgId":"…","spaceName":"…","chatName":"…",
   "senderName":"…","text":"…(truncated 1024)","hasAttachments":false,
   "attachments":[{"layout":0}]}}
```

Message sig = `accountKey.Sign(ciphertext)`; ciphertext =
`encKey.Encrypt(jsonPayload)`.

### Silent / read path

On mark-read, heart calls `NotifyRead` → `NotifySilent` to the sender's
**own identity topic** with the chat's groupId
(`core/block/chats/service.go:813,949` — hooked in the read RPC
handlers, *not* a state feed) — wakes the account's other devices to
clear badges. Server drops silent topics ≠ caller's own account
(`push/push.go:140`).

### Push-server quirks (verified, bake into the client)

- `Subscribe` ≡ `SubscribeAll` — **both full-replace**
  (`SetAccountTopics`, `push.go:101,260`). Do set-math client-side;
  heart only ever calls `SubscribeAll` after diffing.
- `ExistedSpaces` is a no-op today (`spacerepo:80-82`,
  `return spaceIds, nil`, TODO re 1-1 spaces) — `CreateSpace` is
  effectively unenforced, but implement register-before-notify for
  forward-compat. heart registers when `isOwner || isOneToOne`
  (`topics.go:66-81`).
- `CreateSpace`/`RemoveSpace` **always return Ok** (`push.go:186,204`
  `return nil`) — `ErrSpaceExists` is unobservable.
- `Subscriptions` returns topics **without signatures**
  (`push.go:242-246`) — can't round-trip; re-sign from the derived key.
- Loud `Notify` sets `IgnoreAccountId = sender` (`push.go:166`) — your
  own devices are never loud-notified by your own send.
- Platform enum is `{IOS, Android}` only — desktop/headless `any` is
  send-only (nothing to register, nothing to receive).
- heart retry loop: 6 attempts × 10s, break on `ErrNoValidTopics`
  (`service.go:277-290`); token registration + subscription re-sync
  every 5 min or on wake (`service.go:161-182`).

**Push is chat-only in heart.** ACL/invite events are in-app
notifications, never pushed — deliberately out of scope here too.

## Decisions (locked 2026-07-11)

1. **v1 triggers: message create + mention-adding edits.** No
   reactions push, no ACL/invite push (net-new, deferred).
2. **Per-space default mode lives on the tech-space `spaces` row**, in
   a new client-writable `settings` subtree (`settings.notifyMode`).
   Rationale (revised 2026-07-11 — replaced the earlier general-chat
   anchor, which overloaded the general chat's own per-chat
   preference): the tech space is per-account (owner-only ACL), so a
   row field is account-private + device-synced **by construction**;
   it is heart parity (the row is our spaceView analog, where heart
   keeps `SpacePushNotificationMode`); raw rows already flow through
   `POST /v1/spaces/query[/subscribe]` so reads + live cross-device
   updates need **zero new read surface**; and it works on
   pending/deleted rows (mute a pending 1-1 before accepting — an
   in-space object can't). Verified safe: the spaceIndex→row mirror is
   per-field (`OnSpaceMetadataUpdated`, techspace `service.go:779-805`
   — dedup + name/description/icon/spaceType only), and the row
   already mixes SDK-owned fields of different scopes guarded by
   `SpaceIndexHandler` (Type pinned, AclHeadId ScopeLocal, …) — a
   guarded `settings` subtree is the established pattern. Per-chat
   overrides stay on each chat object. Effective mode:
   `chat.notifyMode ?? spacesRow.settings.notifyMode ?? "all"` (heart
   default is All — enum 0, also the relation-removed fallback).
3. **Device-token endpoints ship in v1** (`any` backs mobile shells via
   `any.aar`/xcframework; the shell POSTs its FCM/APNs token to the
   embedded server).
4. **e2e runs a local `anytype-push-server`** (Redis+Mongo, no real
   FCM); assertions at the DRPC level (topics/signatures/keyId) since
   delivery is fire-and-forget. Staging `{peerId, addr}` comes from
   infra later — config-only change.

## Settings storage

The account-values carrier is the unlock: **account-scoped property
values on `objects` rows work today** (SDK
`internal/spaceimpl/properties.go:86-92` routes `ScopeAccount` →
`setAccount`; tech-space `account_values` carrier = account-private,
synced across own devices, mirrored inline for read-your-writes).
Account-scoped *dataset record fields* are NOT wired
(`space/modify.go:38-40`) — don't try.

| Datum | Scope | Home |
|---|---|---|
| per-chat mode `all/mentions/none` | account | `notifyMode` property on the chat object |
| per-space default | account-private via tech-space ACL | `settings.notifyMode` on the tech-space `spaces` row (new writable `settings` subtree, SDK-guarded) |
| device token (FCM/APNs) | device-local | file in the per-account data dir (`internal/push`); re-register on boot; **never** a dataset |
| push/enc keys | shared per-space | derived on demand inside the SDK; never stored |

Per-chat: `notifyMode` must be **statically declared** on the built-in
`chat` type (registered types reject runtime `AddProperty`, SDK
`types.go:109-111`) — next to the ScopeLocal unread counters
(`internal/chat/chat.go:203-206`), `Scope: handler.ScopeAccount`,
string kind, enum validated `any`-side. Write via existing
`POST /v1/spaces/:s/properties/:chatObjectId/set/chat`, read rides the
objects `/query` row clients already fetch for unread badges;
cross-device updates arrive on the existing `/query/subscribe` stream
via the account mirror.

Per-space: new SDK surface `Spaces().SetSettings(ctx, spaceId, patch)`
— per-path $set/$unset restricted by `SpaceIndexHandler` to the
`settings.` subtree (SDK-owned fields stay guarded; `settings` is an
abstract Dynamic map, push claims `settings.notifyMode`). Wire:
`PATCH /v1/spaces/:spaceId/settings` — deliberately separate from
`PATCH /v1/spaces/:spaceId`, which writes the **member-replicated**
spaceIndex (name/description/icon); mixing account-private and
member-visible writes on one endpoint is a trap. Reads: the raw rows
from `POST /v1/spaces/query[/subscribe]` carry `settings` verbatim
(passthrough — no new read endpoint); optionally surface on
`SpaceInfo` too.

Better than heart: no `ForceMute/Mention/AllIds` chatId id-lists on a
space row — each chat is an object, the override lives on it.

## Design

### SDK: new `pushclient` component → `SDK.Push()` (M1, critical path)

On the `cheggaaa/syn-72-handler-record-getter` base. Raw space keys
never cross the SDK boundary — surface is keyed by `spaceId`:

- `SetToken(ctx, platform, token)` / `RevokeToken(ctx)`
- `RegisterSpace(ctx, spaceId)` / `RemoveSpace(ctx, spaceId)` — SDK
  derives the push key, signs `accountSignature =
  pushKey.Sign([]byte(accountId))`
- `SubscribeAll(ctx, []SpaceTopics)` / `Subscriptions(ctx)` — SDK signs
  each topic string with the per-space derived key
- `Notify(ctx, spaceId, topics []string, cleartext []byte, groupId)` /
  `NotifySilent(ctx, spaceId, groupId)` — SDK encrypts (derived AES
  key), stamps `keyId`, account-signs ciphertext, DRPCs

Internals: config field `{peerId, addr}` (seeded from `any` config via
`sdkconfig`, like `Files`); dial via `peerservice.SetPeerAddrs` + pool
(the `internal/files/broker` dial template); dep on the public
`anytype-push-server/pushclient/pushapi` module (no codegen); port
heart's two derivation functions verbatim; ACL state reached like
`internal/spaceimpl/payloads.go:432-450` (`handle.Inner().Acl().AclState()`
→ `FirstMetadataKey`/`CurrentReadKey`). Boundary: `any` supplies topic
strings + cleartext payload + groupId (chat-domain knowledge); SDK owns
crypto + transport (chat-agnostic).

### `any`: `internal/push` service (M2)

Structural twin of `internal/indexer` — per-account, SDK-consuming,
background-looping:

- **Subscription sync loop:** `Spaces().Subscribe` spawns/drops
  per-space workers; each rebuilds the desired topic set from effective
  `notifyMode` over the space's chat objects, diffs against local
  state, `SubscribeAll` (full replace); `RegisterSpace` when
  owner/1-1; re-sync on settings change + 5-min ticker. Token
  registered on boot/change.
- **Notify queue:** async, 6×10s retry, break on `ErrNoValidTopics` —
  HTTP responses never block on the push server.
- **Wiring:** `push` field on engine + deps (`engine.go`,
  `handlers_meta.go`), constructed in `bootEngine` after the indexer,
  closed before `sdk.Close()`. Config `push.{enabled,peerId,addr}` /
  `ANY_PUSH_{ENABLED,PEER_ID,ADDR}` (`internal/config`); disabled ⇒
  `deps.push == nil` ⇒ 409 `push.disabled` (indexer pattern).

### Triggers (M4) — sender-scoped by construction

- `chatSend` (`internal/server/handlers_chat.go:27`): after
  `chat.Send`, read the record back (`dataset=chat_messages`, id =
  `res.RecordIds[0]`) for the server-derived `mentions` array
  (`FieldMentions`, spoof-proof, reply-fold-in included, cap 64) →
  build the 4 topic kinds + `chatpush.Payload` JSON → enqueue
  `Push().Notify`.
- `chatEdit`: diff mentions before/after; push only to **newly added**
  mentions (per decision 1), per-chat + bare identity topics.
- `chatRead` / `chatReadAll` (`handlers_chat.go:276,303`): enqueue
  `NotifySilent(own identity, groupId)` — heart hooks the read RPC the
  same way; no state feed needed.
- **Why not a `Changes()` background loop:** it fires on *remote*
  messages too → double-push (the remote sender already pushed). The
  handler hook is sender-scoped for free. (Consumer-side side effect —
  same blessed invariant exception as `/search`; document in the new
  `docs/NN-push.md` + a CLAUDE.md Status item.)

### HTTP + CLI (M2/M5)

Account-scoped routes outside the `:spaceId` group (beside
`/ui/commands`, `/sync-status/subscribe`), behind the `/v1` auth guard:

- `POST /v1/push/token` `{platform: ios|android, token}` → SetToken
- `DELETE /v1/push/token` → RevokeToken
- `GET /v1/push/token` → local status (registered? platform?)
- `GET /v1/push/subscriptions` → Subscriptions (unsigned topics)

CLI: `any push token set/revoke/status`, `any push subscriptions`.
Settings need no CLI (existing `any properties set`).

## Milestones

**M0 (S)** — wire-compat spike: run local push-server, replay a heart
`Notify`, pin the exact byte encodings (identity string in
`CreateSpace.accountSignature`; raw-vs-proto pubkey for `Topic.spaceKey`
— server unmarshals raw Ed25519, `push.go:208,308`).
**M1 (L)** — SDK `pushclient` component (above) **+ the S-sized
techspace addition**: `settings` subtree on the spaces-row schema,
handler guard, `Spaces().SetSettings`. Critical path.
**M2 (M)** — `any` `internal/push` + config + engine wiring + token
endpoints + `PATCH /v1/spaces/:id/settings`.
**M3 (S)** — `notifyMode` account-scoped prop on chat type +
effective-mode resolution (`chat.notifyMode ??
spacesRow.settings.notifyMode ?? "all"`) + sync-loop consumption. Docs.
**M4 (M)** — chatSend/chatEdit/chatRead hooks (above).
**M5 (M)** — CLI + `docs/NN-push.md` + CLAUDE.md status + e2e against
local push-server (DRPC-level assertions).

M3 ∥ M4 after M2. Real staging address: config-only, any time after M0.

## Risks

- **[spike, M0] signature byte encodings** — wrong raw-vs-proto pubkey
  encoding → `ErrInvalidTopicSignature`; resolve by replaying heart.
- **[SDK] new subsystem** — files-v2/identities-scale addition; agree
  the `PushAPI` surface before building.
- **version drift** — push-server pins `any-sync v0.11.9`, SDK is on
  `v0.13.0-alpha.5`; handshake credential format stable across the
  range, re-verify on any signature mismatch.
- **e2e observability** — FCM delivery is fire-and-forget; CI asserts
  at the DRPC/repo level only.
