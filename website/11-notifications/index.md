---
title: Notifications
description: Getting a user's attention when any is not on screen — end-to-end-encrypted mobile push for chat, and the process helper for reporting long-running work.
order: 0
---
# Notifications

Choose between two mechanisms, depending on whether you need to notify a person or show progress for running work. Mobile push delivers chat messages to a phone even when the app — and the server inside it — is not running, without the push infrastructure ever seeing plaintext. The process helper reports long-running work (agent runs, index passes, imports) as live progress that any device of the account, or any member of a space, can watch and cancel.

## Push: the sender encrypts, the receiver decrypts

There is no notification server that reads your messages. When you send a chat message, *your* server derives the recipient topics, encrypts the payload with a key derived from the space's read key, signs it, and hands opaque ciphertext to the push node. The push node verifies signatures and fans ciphertext out to the FCM / APNs tokens subscribed to those topics. On the phone, the notification extension looks up the space's key in its native keystore and decrypts locally.

```
sender's any ──encrypt+sign──▶ push node ──ciphertext──▶ FCM / APNs ──▶ phone
                                   │                                     │
                            sees only topics                      decrypts with the
                            and ciphertext                        cached space key
```

any owns the account policy — which topics to hold, when to re-sync subscriptions, the chat hooks that fire on send / mention / read — and delivers the key material clients must cache. Who gets notified is a two-level `notifyMode` setting (`all` / `mentions` / `none`) per space and per chat, private to the account.

Push payloads stay encrypted through the push infrastructure. The receiving device needs the cached space key to decrypt them. This is separate from agent model calls or connector requests, which may send readable content to the configured provider.

## Processes: progress over the event bus

Long-running operations broadcast `process.*` events on the [event bus](../realtime/event-bus.html). The server folds them into an in-memory last-event-wins view behind `GET /v1/processes`, with heartbeats and staleness expiry so a crashed owner disappears on its own. Cancellation is an event addressed at the owner, who stops and emits the terminal frame. The server's own indexer reports through the same view, which is how a client can explain "search is incomplete because the embedding model is still downloading".

## Where each lives

| Concern | Surface |
|---|---|
| register / revoke a mobile push token | `POST` / `GET` / `DELETE /v1/push/token` |
| inspect the account's server-held topics | `GET /v1/push/subscriptions` |
| per-space notify default | `PATCH /v1/spaces/:id/settings` `{"set":{"notifyMode":"mentions"}}` |
| per-chat override | the `chat.notifyMode` account-scoped property on the chat object |
| receiver-side keys | `SpaceInfo.push` on `GET /v1/spaces`, streamed by the [live space list](../realtime/space-list.html) |
| register / progress / finish / cancel a process | `POST /v1/processes[/:id/progress|finish|cancel]` |
| live process view | `GET /v1/processes`; raw frames on `GET /v1/events/subscribe?type=process.*` |

Desktop notifications for chat are a client concern built from subscriptions and materialized unread counters rather than push — see [Chat](../types/chat.html). ACL and invite events are surfaced in-app, never pushed.

<div class="cards">
<a href="push.html"><strong>Push notifications</strong><span>Sender-pushes E2E model, topics, notifyMode settings, receiver-side keys and configuration</span></a>
<a href="processes.html"><strong>Processes</strong><span>Report and cancel long-running work over the event bus; the live view and the built-in producers</span></a>
</div>
