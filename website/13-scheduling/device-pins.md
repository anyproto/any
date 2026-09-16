---
title: Device pins
description: Which of your devices runs a trigger — the owner field, claiming and eviction, and the active-instance election that claims unassigned records.
order: 40
---
# Device pins

A trigger's `owner` selects the device that runs it. Set a peer ID to pin the job; clear it to let the currently active device claim it. This lets you keep a job on a particular machine while using the agent from another device.

A pin is independent of the active-agent election. A standby runtime still runs jobs pinned to its device. A stopped runtime runs nothing, and moving the active role does not move already pinned user jobs. Use [Runs and monitoring](runs-and-monitoring.html) to inspect a job before changing its owner.

## The owner field

| `owner` | Meaning |
|---|---|
| `<peerId>` | pinned: fires on that device whenever its agent process runs, with or without a network connection, active or standby. While that process is not running the trigger does not fire anywhere |
| `""` | unassigned: the election-active device claims it and stamps its own peer id |
| `anyrt-<pid>` | the stamp a device writes when it has no peer id; every reader treats it as unassigned |

Every trigger is pinned once claimed. The record alone decides where a trigger runs — a device compares `owner` with its own peer id and never consults the election at fire time.

Peer ids come from the account's devices registry — `GET /v1/devices` returns every device row, the per-app `active` winner and `self`, this server's own id:

```sh
curl -s http://127.0.0.1:7001/v1/devices | jq '{self, active, devices: [.devices[] | {peerId, name, os}]}'
```

## Pin, repin, reassign

```sh
# pin the digest to this machine
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/upsert \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"$TRIGGERS\",
       \"records\": [{\"id\": \"daily-digest\", \"fields\": {\"owner\": \"$PEER\"}}]}"
```

`$ANCHOR` and `$TRIGGERS` come from [Scheduling](index.html). Only the field you send changes; the rest of the record stays. Effects within one tick:

- **repin** — the old device evicts the entry, the new one adopts it. A cron re-arms strictly forward on the new device; a consumed `once` stays consumed.
- **reassign** (`owner: ""`) — the device that is active now claims it and stamps its peer id.
- **delete** the record — evicted everywhere.

The reconcile touches only records it should: foreign-pinned records are left alone, malformed ones are skipped with a log line and never crash the ticker.

> **Why it matters.** Pins are the substrate for remote runners. An always-on machine — a home server, a VM — joins the account as a device, and any trigger pinned to its peer id runs there, while your laptop keeps the conversational agent. No scheduler service is involved; the pin is a field on a synced, encrypted record.

## The active-instance election

Two agent processes on one account would both answer the chat. The devices registry settles that: each agent registers itself under the app slug `bao` (`PUT /v1/devices/me {"apps": {"bao": {"version": …}}}`), and the server computes one winner per slug from the devices' claims — highest claim `seq`, then `at`, then peer id — returned as `active.bao` on `GET /v1/devices`. The runtime polls that verdict every 10 seconds and never reimplements the rule.

| Verdict | The agent process… |
|---|---|
| active | claims unassigned triggers, runs the standing built-ins, and holds the chat responder record |
| standby | **still fires its pinned triggers**; claims nothing |
| pruned (its device row tombstoned) | fires nothing at all, pins included — permanent standby until a fresh `any init` |

A device claims the role when no other device row carries `apps.bao`, or when the recorded winner's row is gone; `POST /v1/devices/activate {"app": "bao"}` sent to the target device's own server moves the role by hand.

Only the "this is the agent" set follows the election: the standing built-ins and the `chat-watch` record ([Event triggers](event-triggers.html)). A takeover re-arms the standing crons strictly forward (no wake-and-replay burst), re-stamps their records and `chat-watch`, and the chat watch connects — its snapshot backlog then answers messages that arrived meanwhile. A stand-down lets in-flight runs finish, drops deferred conversations, and clears the owner of `chat-watch` on the record so the new winner can take it. **Moving the active device never moves user triggers**: a claimed trigger stays on its device until someone repins or reassigns it.

```sh
curl -s http://127.0.0.1:7010/election
# {"app":"bao","enabled":true,"active":true,"peerId":"12D3…","winner":"12D3…"}
```

## The overlap window

The election is polled, so for up to one poll two devices can both believe they are active. Only two things are exposed to it: the standing built-ins can overlap, and two devices can both claim a brand-new unassigned record — the record converges on one owner value and the other device evicts its entry. A trigger that already has an owner is unaffected.

## Degrade

A server without the devices registry disables the election for the run (single-device behavior, gate always active, one log line); a registration that keeps failing at boot does the same. Transient registry read errors keep the last verdict so the gate never flaps. Without a peer id the runtime stamps the `anyrt-<pid>` form on claim, which stays claimable across restarts because every reader treats it as unassigned.

## Reading pins

```sh
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"$TRIGGERS\"}" \
  | jq '.records[] | {id, name, kind, owner, enabled, lastStatus}'
```

The running agent's `GET http://127.0.0.1:7010/triggers` shows the entries its registry holds on this device — see [Runs and monitoring](runs-and-monitoring.html).
