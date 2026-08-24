---
title: Agent data
description: Every dataset the agent owns — turns, chunks, memory, config, secrets, triggers — declared as runtime datasets on derived objects in the user's space, and readable with the ordinary query surface.
order: 60
---
# Agent data

Nothing agent-specific is compiled into the any server. The harness declares its stores as [runtime datasets](../database/runtime-datasets.html) on objects it derives itself, owns the record shapes and validation, and reads and writes them through the generic query, modify and upsert surface. You can do the same.

## Where it lives

At serve boot anyrt registers the **`bao/v1` [bundle](../collaboration/bundles.html)** in the agent space and derives one child per store — deterministic ids every device computes offline, so two devices never mint two brains.

| Child seed | Datasets | Owner |
|---|---|---|
| `bao/config/v1` | `agent_config` | host (anyrt) |
| `bao/secrets/v1` | `agent_secrets` | host |
| `bao/triggers/v1` | `agent_triggers`, `agent_trigger_runs` | host |
| `bao/brain/v1` | `agent_memory_items`, `agent_job_state`, `agent_roi_injections` | guest (`any@v1`) |
| `bao/log/v1` *of the chat's bundle* | `agent_turns`, `agent_chunks` | guest |

Turn logs are per chat: the general chat is the `general-chat/v1` bundle root, and its log is a child of *that* bundle, so it cascade-deletes with the chat. Each store is ensured by its writer, idempotently, on first use.

Resolve a child yourself:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/bundles/bao%2Fv1/children \
  -H 'content-type: application/json' \
  -d '{"seed": "bao/brain/v1"}'
```

## Datasets

**`agent_turns`** (`idRule: user`, id = zero-padded `seq`, all fields write-once, `deleteBy: author`)

| Field | Kind | Meaning |
|---|---|---|
| `seq` | number | client-allocated, max+1 |
| `userText`, `userName`, `fromAgent` | string | the message and who sent it |
| `replies` | array | what the user saw in chat |
| `think` | string | narration that did not go to chat |
| `effects` | array | one-liners per side effect |
| `messageIds` | array | chat message ids posted |
| `traceRef` | string | run id of the device-local trace |
| `interrupted` | boolean | a hard break ended the run |
| `llm` | object | `in`/`out`/`cacheRead`/`cacheWrite`/`costUsd`/`fuelUsed`/`cells` |
| `searchText` | string | userText + replies, the indexed text (scope `history`) |

**`agent_chunks`** — `seq`, `level` (1 over turns, 2+ over chunks), `fromSeq`/`toSeq`, `summary`, `periodStart`/`periodEnd`, `unitsCovered`; indexed under scope `history`.

**`agent_memory_items`** (`idRule: auto`, `deleteBy: author`) — write-once `category`, `context` (both required), `validFrom`, `chatId`, `fromAgent`; author-mutable `body`, `tags`, `entities`, `keywords`, `confidence`, `importance`, `salience`, `accessCount`, `edges`; stamps `creator`, `createdAt`, `modifiedAt`; search `{title: context, text: body, scope: "agent"}`. `agent_job_state` holds cron cursors (one record per job id); `agent_roi_injections` is auto-recall's injection log.

**`agent_config`** (`idRule: user`, id = the dotted key, `dynamic: true`) — `key`, `secret` (boolean), `value` (space-wide, synced), `localValue` (`scope: local`, this device only). Resolution is `localValue ?? value ?? default`; defaults such as `llm.tier.*` and `search.provider.*` ship inside anyrt.

**`agent_secrets`** (`idRule: user`, id = the secret ref) — `key`, `secret`, and `value` with `scope: local`: the plaintext never syncs and guest reads are refused. See [Connectors](connectors.html).

**`agent_triggers`** / **`agent_trigger_runs`** (`idRule: user`, `dynamic: true`, zero declared fields) — the whole definition (`name`, `kind` cron/once/event, `spec`, `program`, `args`, `owner`, `enabled`, `limits`, `maxConsecutiveFailures`) plus the rollup (`lastRunAt`, `lastStatus`, `lastDurationMs`, `runCount`, `lastRunRef`) rides the free keyspace. Contract in [Scheduling](../scheduling/index.html).

## Reading it yourself

The last five turns of the general chat:

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'content-type: application/json' \
  -d '{"objectId": "'$LOG'", "dataset": "agent_turns",
       "sort": ["-seq"], "limit": 5}'
```

```
any query $SPACE $LOG agent_turns --sort -seq --limit 5
```

Every memory item in one category, live:

```bash
curl -N -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query/subscribe \
  -H 'content-type: application/json' \
  -d '{"objectId": "'$BRAIN'", "dataset": "agent_memory_items",
       "filter": {"category": "decision"}, "sort": ["-createdAt"], "limit": 50}'
```

Memory and history also participate in [search](../search/index.html) under scopes `agent` and `history`.

## Scheduling a job from the agent

Because triggers are records, the agent creates one by writing a record — a reminder, for example:

```python
c = use("agent:any@v1")
c.upsert_records(space, triggers_id, "agent_triggers", [{
  "id": "remind-standup",
  "fields": {"kind": "once", "spec": {"at": 1756112400},
             "program": "agent:remind@v1",
             "args": {"space": space_id, "chatId": chat_id,
                      "text": "standup in 10 minutes"},
             "enabled": True}}])
```

The running device adopts it on the next tick; a `once` fires when `now >= at`, then auto-disables and keeps its record as the audit trail. See [Once](../scheduling/once.html).

> **Why it matters.** The agent's whole operational state is a handful of datasets in your encrypted space: queryable, subscribable, exportable, deletable, and synced to your other devices — with no schema you cannot read and no store you cannot leave.
