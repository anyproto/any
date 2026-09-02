# 20 — Push notifications

Mobile push for chat (SYN-47), interoperating with the same
`anytype-push-server` deployment anytype-heart uses — topic vocabulary,
payload shape, and crypto are byte-compatible, so an `any`-backed mobile
shell and an Anytype app notify each other. `any` owns the account
policy (which topics, when to sync, the chat hooks); the SDK's
`pushclient` component owns crypto + transport (`SDK.Push()`, the
`space.PushAPI` interface — SDK branch `cheggaaa/syn-47-push-client`).

## Model — sender-pushes, E2E-encrypted

The push server never sees plaintext. On every chat write the
**sender's** server builds the recipient topics, encrypts the payload,
signs the ciphertext with the account key, and calls `Notify`. The
server verifies the caller's any-sync secure-channel identity plus
per-topic signatures, then fans opaque ciphertext out to the FCM/APNs
tokens of accounts subscribed to matching topics. Receive-side
decryption is a mobile-client concern (iOS NSE / Android extension) —
`any` delivers the key material clients must cache for it
(§ Receiver-side keys) and owns the wire contract.

Both keys are **derived from ACL state, never stored** (the SDK
re-derives on demand; see the `PushAPI` doc comments in the SDK's
`space/push.go`):

| Key | Derivation | Properties |
|---|---|---|
| space push key (Ed25519) | SLIP-10 from the ACL's first metadata key | identical for every member; signs topics + space registration; its pubkey (base58) is the server's space identifier (`spaceKey`) |
| payload enc key (AES) | SLIP-21 from the space's current read key | rotates with ACL read-key rotation; `keyId = hex(sha256(key))` routes decryption on the receiver |

The push node is a **direct out-of-band peer** — `{peerId, addrs}` from
config, not from the nodeconf (see § Config).

Like `/search`, this is a **consumer-side exception** to the "endpoints
map 1:1 onto SDK methods" invariant: the chat notify hooks are a side
effect of the chat handlers (not an SDK feature), and the subscription
sync loop is `any`-side policy. The exception is deliberate — a
`Changes()`-feed trigger would fire on *remote* messages too and
double-push (the remote sender already pushed); the handler hook is
sender-scoped by construction.

## Topic vocabulary (heart-compatible)

The sender publishes the superset per message; subscribers pick their
granularity. `groupId = sha256hex(chatObjectId)` everywhere (the
server-side collapse key — notifications group per chat).

```
chats                                        space-wide "all messages"
chats/<sha256hex(chatObjectId)>              per-chat "all messages"
chats/<sha256hex(chatObjectId)>/<identity>   per-chat mention
<identity>                                   bare identity (bulk mentions)
```

Topic strings are signed by the SDK with the space push key; `any`
supplies only the strings (`internal/push/topics.go`).

## Payload wire shape (heart-compatible — field names verbatim)

The pre-encryption JSON (`internal/push/chatpush.go`, pinned by
`TestChatPayload_HeartWireFormat`):

```json
{ "spaceId":     "spc_…",
  "spaceUxType": 0,
  "spaceType":   0,
  "senderId":    "A…",
  "type":        1,
  "newMessage": {
    "chatId":         "obj_…",
    "msgId":          "msg_…",
    "spaceName":      "Project",
    "chatName":       "general",
    "senderName":     "alice",
    "text":           "…(truncated to 1024 runes)",
    "hasAttachments": false,
    "attachments":    [] } }
```

- `type: 1` (new chat message) is the only loud payload v1 emits.
- `spaceUxType` / `spaceType` are heart's enums; `any` doesn't carry
  either, so both stay `0` (best-effort until a mapping exists).
- `attachments` entries are `{"layout": 0}` stubs — `any`'s chat
  attachments have no layout notion.
- Read notifications are **silent** (data-only, no payload): the server
  targets only the caller's own-identity topic, waking the account's
  other devices to refresh badges.

## Receiver-side keys — decrypting on mobile while `any` is down

A push arrives when the app — and therefore `any` — may not be running
(iOS Notification Service Extension, Android
`FirebaseMessagingService`). The extension cannot ask a dead server to
decrypt, so clients **cache the per-space key material natively** and
decrypt on their own. `any` delivers it as the `push` object on space
info (the heart pattern: heart's clients read the same values off the
`spacePushNotificationKey` / `spacePushNotificationEncryptionKey`
space-view details — encodings are byte-compatible, so existing mobile
decrypt code ports as-is):

```json
{ "id": "spc_…",
  "push": {
    "spaceKey": "<base64 proto-marshalled ed25519 priv>",
    "encKey":   "<base64 raw AES-256 key>",
    "encKeyId": "<hex sha256 of the raw key bytes>" } }
```

Where to read it:
- `GET /v1/spaces` and `GET /v1/spaces/:id` — `push` is a plain row
  field, so list rows carry it too (unlike the derive-based ids).
- `POST /v1/spaces/query/subscribe` — the raw rows stream the same
  `push` object; a **read-key rotation shows up as a row update**
  (new `encKey`/`encKeyId`), no extra stream needed.

Omitted until the SDK's mirror has run for that space — e.g. a joiner
whose access is still pending has no read key and gets `push` only
after the owner's accept lands.

**Cache contract (per space, in the OS keystore):**

1. Maintain an **append-only** map `{encKeyId → encKey}`. Never evict
   on rotation: a payload encrypted before the rotation still arrives
   carrying the old `keyId`, and old keys stay valid for old
   ciphertext forever.
2. Store it where the notification-handling process can read it —
   iOS: a keychain item in an access group shared with the NSE,
   `kSecAttrAccessibleAfterFirstUnlock` (pushes arrive before first
   unlock otherwise fail); Android: Keystore-wrapped storage readable
   from the messaging service (e.g. `EncryptedSharedPreferences`).
3. Refresh on every app foreground while `any` runs: `GET /v1/spaces`,
   upsert every `push` you see; hold the space-list subscribe stream
   while the app is open so rotations land immediately.
4. `spaceKey` is carried for heart parity (topic identity /
   server-side registration); decryption needs only `encKey`.

**Handling an incoming push:**

1. Read the message's `keyId` and ciphertext (field names are the
   push-server transport contract — same as heart's; see
   anytype-push-server).
2. Look up `encKey` by `keyId` in the cache. **Miss ⇒ show a generic
   notification** ("New message") — fresh install or a rotation you
   haven't cached yet; the real content syncs when the app opens.
3. Decrypt: AES-256-GCM, the 12-byte nonce is **prefixed** to the
   ciphertext, no AAD (any-sync `crypto.AESKey` format —
   `open(key, nonce=ct[:12], data=ct[12:])`).
4. Parse the payload JSON (§ Payload wire shape) — `spaceId`, `chatId`,
   `senderName`, `text` are all inside the plaintext; route the
   notification tap from those.

Security note: `encKey` is a **one-way SLIP-21 derivation from the
read key** — holding it decrypts push payloads only, never space data.
That is why exposing it over the localhost API (and parking it in the
OS keystore) is acceptable while the read key itself never leaves the
SDK.

## Triggers (sender-scoped hooks)

Fired by the chat handlers after a successful write — asynchronous,
best-effort, never block or fail the HTTP response
(`internal/server/handlers_chat.go` → `internal/push/chatpush.go`):

- **send** — read the record back for the server-derived `mentions`
  (spoof-proof, reply fold-in included; `docs/16-chat.md`), then notify
  the full topic superset above.
- **edit** — diff mentions before/after; notify only **newly added**
  mentions (bare identity + per-chat mention topics; never the
  broadcast topics — an edit is not a new message for the room).
- **read / read-all** — silent own-identity notification with the
  chat's `groupId`.

## Settings — who gets notified

Two account-private knobs, both `all | mentions | none`:

| Knob | Home | Write | Read |
|---|---|---|---|
| per-space default | `settings.notifyMode` on the tech-space `spaces` row | `PATCH /v1/spaces/:spaceId/settings` | `SpaceInfo.settings` on `GET /v1/spaces[/:id]`; live via `POST /v1/spaces/query/subscribe` (raw rows) |
| per-chat override | `chat.notifyMode` account-scoped property on the chat object | `POST /v1/spaces/:s/properties/:chatObjectId/set/chat` | the objects `/query` row (same read clients already do for unread badges) |

**Effective mode: `chat.notifyMode ?? settings.notifyMode ?? "all"`.**
Absent or out-of-vocabulary values mean "inherit" (heart's default is
All). Both knobs are account-private and sync across the account's own
devices; other members never see them.

The subscription sync loop maps modes onto topics with heart's
bulk-vs-per-chat branch (`internal/push/topics.go`):

- **No chat in the space carries a valid override** → bulk topics from
  the space mode: `all` → `[chats, <identity>]`; `mentions` →
  `[<identity>]`; `none` → nothing.
- **Any chat overrides** → per-chat topics for *every* chat (bulk and
  per-chat don't mix — `chats` would override a muted chat). Per chat,
  effective mode `all` → `chats/<sha>` **and** `chats/<sha>/<identity>`
  (mention-adding *edits* publish only the mention topics — no room
  re-notify — so an "all" chat must hold its own mention topic too;
  multi-topic matches of one message are the norm, the push server
  dedups per device); `mentions` → `chats/<sha>/<identity>`; `none` →
  skip.

The loop reconciles on space-list events (debounced), on a 5-minute
tick, and on token changes; local writes to either knob (the settings
`PATCH`, a `chat.notifyMode` property set) also kick it directly, so
this device's mode changes converge immediately. **Remote-origin
writes — another device flipping a mode, a peer creating a chat —
converge on the next 5-minute tick**: the loop has no cross-peer
change feed, the tick is the explicit convergence bound.
`SubscribeAll` is a **full replace** (server semantics), diffed
locally via a desired-state hash. Owned spaces and 1-1s are
`RegisterSpace`d first (best-effort — a space whose registration fails
is skipped with a warning and keeps its topics in the payload).

If a space's chat enumeration fails mid-reconcile, the loop is
**fail-safe, never fail-open**: it reuses that space's last-known-good
modes, or — with no known-good state — omits the space from the round
entirely (retried next kick/tick) rather than fall back to bulk
topics, which would silently unmute muted chats.

## Endpoints

Account-scoped, outside the `:spaceId` group, behind the `/v1` auth
guard. All return `409 push.disabled` when no push node is configured
(`deps.push == nil` — the `/search` `index.disabled` pattern). Catalog
entry: `docs/03-api.md` § Push notifications.

| Method | Path | Purpose |
|--------|------|---------|
| POST   | `/v1/push/token` | register this device's `{platform: ios\|android, token}` (204) |
| GET    | `/v1/push/token` | local registration state `{registered, platform?}` — no push-node round trip |
| DELETE | `/v1/push/token` | revoke (local delete wins even if the node is unreachable; 204) |
| GET    | `/v1/push/subscriptions` | the account's server-held topic set — raw `{spaceKey, topic}` rows, unsigned |

Plus the settings write (works with push disabled — it's a generic
client-settings surface):

| Method | Path | Purpose |
|--------|------|---------|
| PATCH  | `/v1/spaces/:spaceId/settings` | per-key `{set, unset}` of the account-private settings object (204) |

`spaceKey` in the subscriptions rows is the base58 space push public
key — the push server's space identifier, **not** a spaceId; the
mapping is client-side via the derived key. Signatures are never
returned (they can't round-trip; the client re-signs from the derived
key on every `SubscribeAll`).

Desktop/headless `any` is **send-only**: the push server's platform
enum is `{ios, android}` — there is nothing to register on desktop and
nothing to receive; the token endpoints exist for the mobile shells
embedding `any.aar` / the xcframework.

## CLI

```
any push token set --platform ios|android --token TOKEN
any push token revoke
any push token status
any push subscriptions
any space settings <spaceId> --set notifyMode=mentions      # per-space default
any space settings <spaceId> --unset notifyMode             # back to "all"
```

Per-chat override rides the existing properties surface (no new CLI):
`POST /v1/spaces/:s/properties/:chatObjectId/set/chat` with
`{"notifyMode": "none"}`.

## Config

```yaml
# Push-notification node (docs/20-push.md). A DIRECT out-of-band peer
# ({peerId, addrs} here, not in the nodeconf). Naming a peer is the
# opt-in; with none resolved, every /v1/push endpoint returns 409
# push.disabled and no background loops run.
push:
  enabled: null            # tristate: null = enabled iff peerId set;
                           # false disables even with a peer configured
  peerId: ""               # the push node's peer id
  addrs: []                # dial addresses, e.g. ["quic://host:port"]
```

**The production node is the packaged default, paired with the network.**
When a config names neither `peerId` nor `addrs` AND leaves the network
unset — the packaged production nodeconf, see docs/05-config.md — `any`
fills in the production push node (`config.ProdPushPeerId` /
`ProdPushAddr`, the same deployment heart uses). So an unconfigured
binary has working push, exactly as it has working sync.

The pairing is deliberate: the push node is not part of the nodeconf, so
nothing else would stop a server on staging or local infra from pushing
through the production node. Point the network anywhere — `nodeconfPath`,
inline `nodeconf`, or `ANY_NETWORK_NODECONF_PATH` — and the default drops
out; that host supplies its own push node or gets none. This is also what
keeps the test suite, which always names a nodeconf, off the production
push server. A half-configured push node (one field of the two) is left
alone rather than completed with mismatched production values.

Env overrides: `ANY_PUSH_ENABLED`, `ANY_PUSH_PEER_ID`,
`ANY_PUSH_ADDRS` (comma-separated); `ANY_PUSH_ENABLED=false` still beats
the packaged default. `addrs` entries take the same forms nodeconf uses —
`quic://host:port` or bare `host:port`. The production node is yamux-only,
hence the explicit `yamux://` scheme in its default address.

### Embedded servers (any.aar / xcframework)

The embedded path (SYN-83) reads no config.yaml and no env — the host
passes the push node explicitly at start:

- **Android (gomobile)**: `mobile.StartWithPush(dataDir, listenAddr,
  nodeconfYAML, pushPeerId, pushAddrs)` — `pushAddrs` comma-separated,
  same format `ANY_PUSH_ADDRS` parses. Plain `Start` keeps push off.
  `StartWithMode(…, mode, controlToken)` adds the ownership mode
  (`02-server.md` § Modes).
- **iOS (c-archive)**: `AnyLibStart(dataDir, listenAddr,
  nodeconfYAML, pushPeerId, pushAddrs)` — same semantics; empty
  strings keep push off. There is no separate `…WithPush` variant;
  `AnyLibStartWithMode(…, mode, controlToken)` adds the ownership mode.
- **Go hosts**: `embedded.Start(embedded.Options{…, PushPeerId,
  PushAddrs})`.

Empty push options follow the same pairing rule as the CLI: a host that
passes neither a push node nor a `nodeconfYAML` lands on the production
pair, so plain `Start` on the default network now has push. Passing
either one opts out of the default for both.

Enablement stays config-driven: non-empty peer id + addrs fill
`cfg.Push` and the tristate activates on its own; empty strings change
nothing (push endpoints return `409 push.disabled`). The push peer
pairs with the nodeconf choice, so peer id and addrs should come from
wherever the network does; `any` ships no push default.

`nodeconfYAML` is optional: `""` selects the embedded **production**
nodeconf compiled into the binding, so a host on production needs no
vendored copy of the conf and moves networks with an AAR/xcframework
bump. Pass YAML text only to override (staging, local infra) — see
`docs/05-config.md` § Network.

The device token persists at `<account-dir>/push-token.json` and is
re-registered in the background on boot; it is **never** a dataset
(device-local by definition).

## Errors

- `409 push.disabled` — no push node configured (or the SDK opened
  without one). The one push-specific code; see `docs/06-errors.md`.
- Delivery is best-effort: `Notify` retries 6×10s in the background
  (breaking early when the server reports no valid topics) and HTTP
  responses never wait on the push node.

## Deferred (not in v1)

- **Reactions push** — no notification on reactions.
- **ACL / invite push** — heart keeps these as in-app notifications,
  never pushed; same here.
- **Desktop receive** — send-only (platform enum above).
- **`RemoveSpace`** — subscription cleanup rides the `SubscribeAll`
  full replace; registered space keys linger server-side (harmless,
  and heart behaves the same).
- **Real-infra e2e** — the gated e2e (below) needs a reachable push
  server; CI wiring + the staging peer address are pending infra.

## Local e2e recipe

The e2e test (`internal/e2e/push_test.go`) skips unless a push server
is reachable — it never stands up the server's Redis/Mongo deps itself:

```bash
# 1. Run anytype-push-server locally (its repo ships a docker-compose
#    with the Redis + Mongo deps; note its peerId from the config).
# 2. Point the test at it:
ANY_PUSH_E2E_PEER_ID=<peerId> \
ANY_PUSH_E2E_ADDRS=quic://127.0.0.1:1234 \
go test -run TestE2E_Push ./internal/e2e/
```

Assertions stop at the DRPC-visible surface (token round-trip, chat
send with a mention succeeds with hooks armed, `GET
/v1/push/subscriptions` converges to the expected bulk topics) —
FCM/APNs delivery is fire-and-forget and not observable from here.

For a dev server, the same two values go into `config.yaml` (`push:`)
or `ANY_PUSH_PEER_ID` / `ANY_PUSH_ADDRS`.

## Implementation map

- `internal/push/` — the service: token persistence (`token.go`),
  desired-topic reconcile + sync loop (`topics.go`, `push.go`), chat
  hooks + heart payload (`chatpush.go`).
- `internal/server/handlers_push.go` — token/subscriptions endpoints;
  `handlers_settings.go` — the settings PATCH;
  `handlers_chat.go` — hook call sites; `engine.go` / `sdk.go` —
  wiring (service constructed only when `config.Push.Active()`).
- SDK: `space/push.go` (`PushAPI`), `internal/pushclient/` (crypto +
  DRPC transport), config threading via `sdkconfig.Push`.
