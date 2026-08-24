---
title: Device pins
description: Which of your devices runs a trigger — the owner field, adoption and eviction, and the active-instance election that assigns unowned records.
order: 40
---
# Device pins

Your account can run the agent on several devices. A trigger's `owner` field says which one fires it: a **peer id** pins the trigger to that device, election-independent; an empty owner leaves it to whichever device currently holds the **active** role. Moving a job is writing one field.

## The owner field

| `owner` | Meaning |
|---|---|
| `<peerId>` | pinned: fires on that device whenever its agent process is up, online or not, active or standby. An offline pinned device simply does not fire |
| `""` | unowned: adopted by the election-active device |
| `anyrt-<pid>` | a legacy stamp; read as unowned |

Peer ids come from the account's devices registry — `GET /v1/devices` returns every device row and `self`, this server's own id:

```sh
curl -s http://127.0.0.1:7001/v1/devices | jq '{self, active, devices: [.devices[] | {id, name, os}]}'
```

## Pin, repin, float

```sh
# pin the digest to this machine
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/upsert \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"agent_triggers\",
       \"records\": [{\"id\": \"daily-digest\", \"fields\": {\"owner\": \"$PEER\"}}]}"
```

Only the field you send changes; the rest of the record stays. Effects within one tick:

- **repin** — the old device evicts the entry, the new one adopts it. A cron re-arms strictly forward on the new device; a consumed `once` stays consumed.
- **float** (`owner: ""`) — the election winner adopts it.
- **delete** the record — evicted everywhere.

The reconcile touches only records it should: foreign-pinned records are left alone, malformed ones are skipped with a log line and never crash the ticker.

> **Why it matters.** Pins are the substrate for remote runners. An always-on machine — a home server, a VM — joins the account as a device, and any trigger pinned to its peer id runs there, while your laptop keeps the conversational agent. No scheduler service is involved; the pin is a field on a synced, encrypted record.

## The active-instance election

Two agent processes on one account would both answer the chat. The devices registry settles that: each agent registers itself under the app slug `bao` (`PUT /v1/devices/me {"apps": {"bao": {"version": …}}}`), and the server computes one winner per slug from the devices' claims — highest claim `seq`, then `at`, then peer id — returned as `active.bao` on `GET /v1/devices`. The runtime polls that verdict every 10 seconds and never reimplements the rule.

| Verdict | The agent process… |
|---|---|
| active | watches the chat, adopts unowned triggers, runs the standing built-ins, drains deferred conversations |
| standby | keeps the chat watch disconnected; **still fires its pinned triggers**; adopts nothing |
| pruned (its device row tombstoned) | fires nothing at all, pins included — permanent standby until a fresh `any init` |

Claiming happens when no other device advertises `bao`, or when the recorded winner's row is gone; `POST /v1/devices/activate {"app": "bao"}` moves the role by hand. A takeover re-arms the standing crons strictly forward (no wake-and-replay burst) and reconnects the chat watch, whose snapshot backlog then answers messages that arrived meanwhile. A stand-down lets in-flight runs finish and clears local ownership without writing records.

```sh
curl -s http://127.0.0.1:7010/election
# {"app":"bao","enabled":true,"active":true,"peerId":"12D3…","winner":"12D3…"}
```

## Adoption stamps the record

When the active device adopts an unowned record it writes its peer id into `owner` — so an unowned trigger becomes pinned to whichever device adopted it first, and stays there across an election move. The UI labels an empty owner "Any active device"; to follow the active device after a move today, clear `owner` again. The proposed change that makes floating records truly follow the election is described on [Event triggers](event-triggers.html).

## Degrade

A server without the devices registry disables the election for the run (single-device behavior, gate always active, one log line); transient registry read errors keep the last verdict so the gate never flaps. Without a peer id the runtime stamps the legacy `anyrt-<pid>` form on adoption, which stays adoptable across restarts because every reader treats it as unowned.

## Reading pins

```sh
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'Content-Type: application/json' \
  -d "{\"objectId\": \"$ANCHOR\", \"dataset\": \"agent_triggers\"}" \
  | jq '.records[] | {id, name, kind, owner, enabled, lastStatus}'
```

The running agent's `GET http://127.0.0.1:7010/triggers` shows the same rows as its registry sees them — including which are adopted here — on [Runs and monitoring](runs-and-monitoring.html).
