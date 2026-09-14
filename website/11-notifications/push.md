---
title: Push notifications
description: End-to-end-encrypted mobile push for chat — how the sender pushes, the topic vocabulary, notifyMode settings, the keys receivers cache, and configuration.
order: 10
---
# Push notifications

Mobile push for chat is **sender-pushes, end-to-end encrypted**: the device that writes a message builds the recipient topics, encrypts the payload, signs it with the account key, and hands ciphertext to a push node that forwards it to FCM / APNs. The push node never sees plaintext, and the receiving phone decrypts with a key it cached natively — so notifications render even while any is not running.

## Model

On every chat write, the sender's server calls the push node's `Notify` with signed topics and an encrypted payload. Two keys are involved, both **derived from ACL state on demand and never stored**:

| Key | Derived from | Role |
|---|---|---|
| space push key (Ed25519) | the ACL's first metadata key | identical for every member; signs topics and space registration; its base58 public key is the push node's identifier for the space (`spaceKey`) |
| payload encryption key (AES-256) | the space's current read key (one-way derivation) | encrypts payloads; rotates with the read key; `encKeyId = hex(sha256(key))` tells the receiver which key to use |

Because the encryption key is a one-way derivation, holding it decrypts push payloads only — never space data. That is what makes it safe to hand to the OS keystore.

Sending has no endpoint. It is a sender-scoped side effect of the chat write handlers, asynchronous and best-effort — an HTTP response never waits on the push node, and delivery retries in the background.

| Chat write | What gets pushed |
|---|---|
| send | the full topic superset below, with the message payload; mentions come from the server-derived `mentions` array, so they cannot be spoofed |
| edit | only **newly added** mentions (mention topics, never the room-wide ones — an edit is not a new message) |
| read / read-all | a silent, data-only notification to the account's own identity topic, waking its other devices to refresh badges |

Reactions, ACL changes and invites are not pushed.

## Topics and payload

The sender publishes every applicable topic; each subscriber picks its granularity. `groupId = sha256hex(chatObjectId)` is the collapse key, so notifications group per chat.

```
chats                                        space-wide "all messages"
chats/<sha256hex(chatObjectId)>              per-chat "all messages"
chats/<sha256hex(chatObjectId)>/<identity>   per-chat mention
<identity>                                   bare identity (bulk mentions)
```

The pre-encryption payload:

```json
{ "spaceId": "spc_…", "spaceUxType": 0, "spaceType": 0, "senderId": "A…", "type": 1,
  "newMessage": {
    "chatId": "obj_…", "msgId": "msg_…",
    "spaceName": "Project", "chatName": "general", "senderName": "alice",
    "text": "…(truncated to 1024 runes)",
    "hasAttachments": false, "attachments": [] } }
```

`type: 1` (new chat message) is the only loud payload. The vocabulary, payload and crypto are byte-compatible with the Anytype apps' push, so an any-backed mobile shell and an Anytype client notify each other through the same push deployment.

## Who gets notified: `notifyMode`

Two account-private knobs, each `all` / `mentions` / `none`, resolved as **`chat.notifyMode ?? settings.notifyMode ?? "all"`**. Both sync across the account's own devices and are invisible to other members.

| Knob | Write | Read |
|---|---|---|
| per-space default | `PATCH /v1/spaces/:spaceId/settings` | `SpaceInfo.settings` on `GET /v1/spaces[/:id]`; live on the [space list stream](../realtime/space-list.html) |
| per-chat override | `POST /v1/spaces/:s/properties/:chatObjectId/set/chat` with `{"patch": {"notifyMode": "none"}}` | `chat.notifyMode` on the chat object's row from `/objects/query` |

```bash
curl -X PATCH http://127.0.0.1:7001/v1/spaces/SPACE/settings \
  -H 'Content-Type: application/json' \
  -d '{"set":{"notifyMode":"mentions"}}'          # → 204
any space settings SPACE --set notifyMode=mentions
any space settings SPACE --unset notifyMode        # back to "all"
```

Settings keys are single-level scalars; at least one `set` or `unset` entry is required (`400 request.missing_field`), and a key may not appear in both (`400 request.invalid_field`). The PATCH works on any row the account knows — including a pending one-to-one, so you can mute it before accepting — and with push disabled.

A background loop reconciles the account's server-held topics from these modes: with no per-chat override in a space it holds bulk topics (`all` → `chats` + `<identity>`, `mentions` → `<identity>`, `none` → nothing); once any chat overrides, it holds per-chat topics for *every* chat in that space, since a bulk `chats` topic would override a muted chat. The loop runs on space-list events, on a 5-minute tick, on token changes, and immediately after a local write to either knob; a mode flipped on *another* device converges on the next tick. If a space's chats cannot be enumerated mid-reconcile the loop reuses last-known-good modes or skips the space — it never falls back to bulk topics and silently unmutes.

## Endpoints

Account-scoped, behind the `/v1` auth guard. Every route answers `409 push.disabled` when no push node is configured.

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/push/token` | register this device's `{platform: "ios" \| "android", token}` → `204` |
| GET | `/v1/push/token` | local state `{registered, platform?}` — no push-node round trip |
| DELETE | `/v1/push/token` | revoke; the local delete wins even if the node is unreachable → `204` |
| GET | `/v1/push/subscriptions` | `{subscriptions: [{spaceKey, topic}]}` — the account's server-held topic set, unsigned |

```bash
curl -X POST http://127.0.0.1:7001/v1/push/token \
  -H 'Content-Type: application/json' \
  -d '{"platform":"android","token":"<FCM token>"}'
any push token set --platform android --token TOKEN
any push token status
any push subscriptions
```

The platform enum has no desktop entry: desktop and headless servers are **send-only**, and the token endpoints exist for the mobile shells embedding `any.aar` / the xcframework. Re-POST on token rotation, DELETE on logout. The token persists at `<account-dir>/push-token.json` and is re-registered in the background on boot. `spaceKey` in the subscriptions rows is the base58 space push public key — the push node's space identifier — not a space id.

## Receiver-side keys

A push arrives when the app, and the server inside it, may not be running. The notification extension therefore needs the key material cached natively. any exposes it as the `push` field on every space row:

```json
{ "id": "spc_…",
  "push": { "spaceKey": "<base64 ed25519 key>",
            "encKey":   "<base64 raw AES-256 key>",
            "encKeyId": "<hex sha256 of the key bytes>" } }
```

Read it from `GET /v1/spaces` (list rows carry it) and keep `POST /v1/spaces/query/subscribe` open while the app is in the foreground — a read-key rotation shows up as a row update with a new `encKey` / `encKeyId`. The field is absent until the space's key mirror has run, e.g. for a joiner whose access is still pending.

The cache contract:

1. **Append-only map `{encKeyId → encKey}` per space**, in the OS keystore, readable by the notification process (iOS: a keychain access group shared with the extension, accessible after first unlock; Android: Keystore-wrapped storage readable from the messaging service). Never evict on rotation — a late payload carries the old `keyId`.
2. **Refresh while alive**: upsert every `push` you see on each foreground, and hold the space-list stream while open.
3. **On push**: read the message's `keyId`; a cache miss shows a generic "New message" (fresh install or an uncached rotation — the content syncs when the app opens); a hit decrypts with AES-256-GCM, 12-byte nonce prefixed to the ciphertext, no AAD, and renders from the payload JSON.

`spaceKey` is carried for parity with the Anytype clients; decryption needs only `encKey`.

> **Why it matters.** The phone never asks a server what a notification says. The push provider sees a topic and ciphertext; the decryption key came from the space's own ACL over the encrypted sync channel and lives in the device keystore.

## Configuration

The push node is a **direct, out-of-band peer** named in config, not part of the network's node list:

```yaml
push:
  enabled: null        # null = enabled iff peerId is set; false disables even with a peer
  peerId: ""
  addrs: []            # e.g. ["quic://host:port"]
```

Environment overrides: `ANY_PUSH_ENABLED`, `ANY_PUSH_PEER_ID`, `ANY_PUSH_ADDRS` (comma-separated). An unconfigured binary on the packaged production network gets the production push node automatically; naming any other network (`nodeconfPath`, inline `nodeconf`, or `ANY_NETWORK_NODECONF_PATH`) drops that default, so a staging or local setup never pushes through production and supplies its own node or none. A half-configured node (only one of the two fields) is left alone. Embedded servers pass the node explicitly at start (`StartWithPush` on Android, the trailing `pushPeerId` / `pushAddrs` arguments of `AnyLibStart` on iOS, `embedded.Options{PushPeerId, PushAddrs}` in Go). See [Configuration](../operations/configuration.html) and [Networks](../operations/networks.html).

> **Note.** `409 push.disabled` is the only push-specific error code. Delivery is fire-and-forget: `Notify` retries six times at 10 s intervals in the background and stops early when the node reports no valid topics.
