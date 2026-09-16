---
title: Agent data
description: Every dataset the agent owns — turns, chunks, memory, config, secrets, triggers, run summaries — declared as runtime datasets on derived objects in the user's space, and readable with the ordinary query surface.
order: 60
---
# Agent data

Nothing agent-specific is compiled into the any server. The harness declares its stores as [runtime datasets](../database/runtime-datasets.html) on types it creates itself, homes them on objects it derives, owns the record shapes and validation, and reads and writes them through the generic query, modify and upsert surface. You can do the same.

## Where it lives

At serve boot anyrt registers the **`bao/v1` [bundle](../collaboration/bundles.html)** in the agent space and derives one child per store — deterministic ids every device computes offline, so two devices never mint two brains.

| Child seed | Datasets | Owner |
|---|---|---|
| `bao/config/v1` | `agent_config` | host (anyrt) |
| `bao/secrets/v1` | `agent_secrets` | host |
| `bao/triggers/v1` | `agent_triggers` | host |
| `bao/runs/v1` | `agent_runs` | host |
| `bao/brain/v1` | `agent_memory_items`, `agent_job_state`, `agent_roi_injections` | guest (`any@v1`) |
| `bao/log/v1` *of the chat's bundle* | `agent_turns`, `agent_chunks` | guest |

Turn logs are per chat: the general chat is the catalog's `system:general-chat/v1` root, and its log is a child of *that* bundle. The brain exists only in the agent space — memory has one home. Each store is ensured by its writer, idempotently, on first use.

Each store is one part of a hidden harness type (`agent_config`, `agent_secrets`, `agent_trigger`, `agent_brain`, `agent_log`), so type pickers never offer them. The names in the table are dataset **keys**; the records live in the storage collection `<typeId>_<key>`, which is the `dataset` value on the wire. Guest code passes the key and `any@v1` resolves it; from `curl`, look the storage collection up once:

```bash
TURNS=$(curl -s http://127.0.0.1:7001/v1/spaces/$SPACE/datasets \
  | jq -r '.datasets[].name | select(endswith("_agent_turns"))')
```

Resolve a child yourself — the route requires the child's `type`, the store's hidden harness type:

```bash
BRAIN=$(curl -s "http://127.0.0.1:7001/v1/spaces/$SPACE/types?includeHidden=true" \
  | jq -er '.types[] | select(.xKey=="agent_brain") | .id')
curl -s -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/bundles/bao%2Fv1/children \
  -H 'content-type: application/json' \
  -d '{"seed": "bao/brain/v1", "type": "'$BRAIN'"}'
```

## Datasets

**`agent_turns`** (`idRule: user`, id = zero-padded `seq`, all fields write-once, `deleteBy: author`)

| Field | Kind | Meaning |
|---|---|---|
| `seq` | number | client-allocated: one past the highest id ever written, deleted ids included |
| `userText`, `userName`, `fromAgent` | string | the message, its sender, and the agent that answered |
| `replies` | array | what the user saw in chat |
| `think` | string | narration that did not go to chat |
| `effects` | array | one-liners per side effect |
| `messageIds` | array | chat message ids posted |
| `traceRef` | string | run id of the run's trace |
| `interrupted` | boolean | a hard break ended the run |
| `llm` | object | `stopReason`, `inTokens`/`outTokens`/`cacheRead`/`cacheWrite`, `cells`, `promptFingerprint`/`soulFingerprint` |
| `searchText` | string | userText + replies, the indexed text (scope `history`) |

Stamps `creator` and `createdAt`.

**`agent_chunks`** — `seq`, `level` (1 over turns, 2+ over chunks), `fromSeq`/`toSeq`, `summary`, `periodStart`/`periodEnd` (datetime), `unitsCovered`, `fromAgent`; `summary` is indexed under scope `history`.

**`agent_memory_items`** (`idRule: auto`, `deleteBy: author`) — write-once `category` (required), `entities`, `keywords`, `validFrom` (datetime), `chatId`, `fromAgent`, `source`, `provenance`; author-mutable `context` (required), `body`, `tags`, `edges`, `confidence`, `importance`, `salience`, `accessCount`; stamps `creator`, `createdAt`, `modifiedAt`; search `{title: context, text: body, scope: "agent"}`. `agent_job_state` holds cron cursors (one record per job id); `agent_roi_injections` is auto-recall's injection log.

**`agent_config`** (`idRule: user`, id = the dotted key, `dynamic: true`) — `{key, value}`, synced to every device. Programs read a key through the `config.get` effect, which reads the row on every call, and write it through `config.set` or the `config@v1` tool. A fresh space gets defaults such as `llm.tier.*` and `search.provider.*` from anyrt at serve start; the `[config]` table of `anybao.toml` overwrites the keys it names.

**`agent_secrets`** (`idRule: user`, id = the secret ref) — `key`, `secret`, `value`, plus the credential-request metadata `status` (`missing` / `rejected` / `set`), `label`, `hosts`, `help`, `note`, `requestedBy`, `requestedIn`, `requestedAt`, `rejectedAt`, `rejectedWith`, `updatedAt`, `meta`. The `value` syncs end-to-end encrypted to the account's devices; guest reads are refused. See [Connectors](connectors.html).

**`agent_triggers`** (`idRule: user`, `dynamic: true`, zero declared fields) — the whole definition (`name`, `kind` cron/once/event, `spec`, `program`, `args`, `owner`, `enabled`, `limits`, `maxConsecutiveFailures`) plus the scheduler state the runner stamps (`lastRunAt`, `lastStatus`, `consecutiveFailures`) rides the free keyspace. Contract in [Scheduling](../scheduling/index.html).

**`agent_runs`** (`idRule: user`, `dynamic: true`) — one summary per run from every device: `runId`, `program`, `device`, `startedAt`/`endedAt`, `durationMs`, `status`, `errorType`, `turns`, `title`, `cells`, `effects`, `mutations`, `tokens`, `costUsd`, `model`, and `triggerId` for a run a trigger fired. "Did it run?" is a query here, not a field on the trigger.

## Reading it yourself

The last five turns of the general chat (`$LOG` is the chat's `bao/log/v1` child):

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'content-type: application/json' \
  -d '{"objectId": "'$LOG'", "dataset": "'$TURNS'",
       "sort": ["-seq"], "limit": 5}'
```

Every memory item in one category, live (`$MEMORY` resolved like `$TURNS`, with the `_agent_memory_items` suffix):

```bash
curl -N -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query/subscribe \
  -H 'content-type: application/json' \
  -d '{"objectId": "'$BRAIN'", "dataset": "'$MEMORY'",
       "filter": {"category": "decision"}, "sort": ["-createdAt"], "limit": 50}'
```

Memory and history also participate in [search](../search/index.html) under scopes `agent` and `history`.

## Scheduling a job from the agent

Because triggers are records, the agent creates one by writing a record — a reminder, for example:

```python
c = use("agent:any@v1")
anchor = c.bundle_child(space, "bao/v1", "bao/triggers/v1")["objectId"]
c.upsert_record(space, anchor, "agent_triggers", "remind-standup", {
    "name": "standup", "kind": "once", "spec": {"at": now() + 600},
    "program": "agent:remind@v1",
    "args": {"space": space_id, "chatId": chat_id, "text": "standup in 10 minutes"},
    "enabled": True})
```

The owning device adopts it on the next tick; a `once` fires when `now >= at`, then auto-disables and keeps its record as the audit trail. See [Once](../scheduling/once.html).

> **Why it matters.** The agent's whole operational state is a handful of datasets in your encrypted space: queryable, subscribable, exportable, deletable, and synced to your other devices — with no schema you cannot read and no store you cannot leave.
