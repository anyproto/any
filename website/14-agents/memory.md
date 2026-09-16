---
title: Memory and recall
description: Separate persistent memory and history from model context, save useful facts, and understand retrieval and background maintenance.
order: 30
---
# Memory and recall

Memory lets an agent use what it learned in a later session. The supplied harness stores it in the user's database, alongside conversation history and application data. Retrieval selects relevant records and the conversation loop includes them in the next model request.

## Memory, history, and context

| Layer | What it holds | Lifetime |
|---|---|---|
| Application data | tasks, documents, contacts, and other structured records | stored in the space |
| History | original conversation turns and summaries of older turns | stored on each chat's log object |
| Memory items | selected facts, preferences, decisions, and lessons | stored on the agent's brain object |
| Model context | instructions, selected history and memories, current message, tool results | assembled for a model call |

Saving a memory does not change the model's weights or put the whole database into every prompt. The harness chooses what to retrieve and how much to include. A fresh run reconstructs its context from records; Python variables from the previous run are gone.

For a custom harness, this separates durable state from prompt policy. You can inspect or correct the stored facts, change retrieval, and keep your application's structured data available through the same API. [Agent data](agent-data.html) shows the stores and runnable queries.

## Memory items

A memory item is a record in the `agent_memory_items` dataset on the brain object of the bao space. A fact about another space names that space in its `context` or `tags`.

Saving requires `category` (an open slug set such as preference, decision, lesson, or fact) and `context` (a one-line fact). Optional fields are `body`, `confidence`, `importance`, `tags`, `entities`, `keywords`, `edges` (typed links to other objects), and `validFrom`. User-stated facts outrank inferred ones.

Save policy, taught by the `_memory` skill (the write path enforces the required fields and value ranges):

| Save | Skip |
|---|---|
| stable preferences | greetings, meta-chatter |
| decisions **with the why** | restatements of what was just said |
| durable domain facts | anything already in history (it is drillable) |
| hard lessons, shipped outcomes | low-confidence speculation |

The save policy targets roughly 1–2 useful facts per turn. Episodes and session summaries belong in history.

### Save a fact from a program

This example runs inside anyrt with the `agent` overlay configured. Set `space` to the space to search for context; the memory client uses the agent space's brain. Recall needs its search index, and deduplication uses the configured classify-tier model.

```python
c = use("agent:any@v1")
r = use("agent:recall@v1").recall(c, space)
m = use("agent:memory@v1").memory(c)       # no space: the brain is the bao space's
m.save_with_dedup({"category": "decision",
                   "context": "picked sqlite for the importer: simpler ops",
                   "confidence": 8}, recall=r)
# → {itemId, action}  or  {deduplicated: true, mergedInto: "<id>", action: "merge"}
```

### Dedup on save

Every save first runs recall over scope `agent`. A classify-tier model then chooses **merge** (update an existing item), **supersede** (create an item linked by `supersedes`), or **create**. A `{deduplicated: true}` reply is a success. A machine-sourced merge keeps stored text, never lowers confidence, and unions tags and edges. The final decision comes from the judge rather than a fixed similarity threshold.

## Recall: one surface, three axes

`recall@v1` is read-only and binds to one space; its memory source is always the bao space's brain:

- **Semantic** — `r.search(q, scopes=["agent", "history", "basic", "email"])` over the [search index](../search/index.html); `r.hydrate(hits)` turns hits into full records in one read.
- **Temporal** — `r.by_period(from, to)` merges memory items (`validFrom`), turns (`createdAt`) and chunks overlapping the range.
- **Graph** — `r.neighbors(id)` walks forward relation properties plus the server's backlinks, types and properties named by xKey.
- **Drill-down** — a chunk expands to its children (chunks or raw turns); a turn's `traceRef` opens the run.

Every memory item auto-recall injects gets its `accessCount` bumped, and so does an item a deliberate dig actually uses (`m.bump_access`), so the store measures its own usefulness.

## Auto-recall

At the start of every turn, `autorecall@v1` runs `recall.search(user_message)` and includes the top hits within a token budget. The conversation model does not have to request this lookup. Search uses the index; generating a query embedding follows the server's configured local or online embedder.

The prompt represents the lookup as a synthetic `run_cell` call and a digest of its results. Memory hits carry provenance date and confidence. History hits point to a past discussion that the agent can open in more detail.

Hits already in the recent-history window are skipped. A relevance threshold can leave a generic message with no recalled items. Each injection is logged to `agent_roi_injections`, so you can inspect what retrieval added.

## History: turns and the chunk pyramid

Turns are append-only records on the chat's log child. `rollup@v1` (an hourly cron) summarises complete batches of ten turns into level-1 chunks and ten level-1 chunks into a level-2 chunk — level-2 summaries are built from child summaries only.

```jsonc
{"seq": 12, "level": 1, "fromSeq": 40, "toSeq": 49,
 "summary": "…", "periodStart": …, "periodEnd": …, "unitsCovered": 10}
```

Same-level chunks are contiguous and non-overlapping. Each summary points to the turns or lower-level chunks it covers, allowing the agent to retrieve the underlying detail. The boot window fits recent raw turns, then L1 and L2 summaries into a token budget. The ten-to-one hierarchy adds roughly 11% record overhead.

## Background jobs

Maintenance runs as standing [triggers](../scheduling/index.html) on the election-active device. Each fire has a trace and an `agent_runs` summary. The runtime must be running for these jobs to execute; their records persist while the device is stopped.

| Program | Schedule | State |
|---|---|---|
| `extraction@v1` | every 15 minutes, over newly persisted turns | on — proposes only stable-fact shapes, through the dedup judge, provenance `fromSeq`, confidence capped at 6 |
| `rollup@v1` | hourly | on — the chunk pyramid |
| `linkgen@v1` | hourly | on — seed-search neighbours, classify-tier proposes typed links from the curated vocabulary (`relates_to`, `caused_by`, `supersedes`, `decided_in`, `part_of`, `owned_by`, `discussed_in`); never invents edge types |
| `evolution@v1` | every 6 hours | ships disabled — refreshes `context`/`tags` from linked neighbours |
| `reflection@v1` | daily | ships disabled — synthesises never-recalled clusters into insights, flags contradictions |
| `decay@v1` | daily | ships disabled — salience halves per idle half-life, never deletes |

The disabled jobs are implemented but do not run by default. Scoring fields can be recorded before an enabled job uses them.

## Where model calls send data

Deduplication, extraction, and summarization use the configured models. A hosted provider can read the inputs included in those calls. The server's default `auto` embedder also uses an online primary with a local fallback; choose `index.embedder: local` for on-device embeddings. The local model must be available before it can serve embeddings. See [Embedders](../search/embedders.html).

You can query, subscribe to, and delete stored memories through the database API. Guest clients resolve the key `agent_memory_items`; raw HTTP uses the discovered `<typeId>_agent_memory_items` storage collection. Continue to [Agent data](agent-data.html#reading-it-yourself) to inspect what was saved, or [Runs and monitoring](../scheduling/runs-and-monitoring.html) to inspect a maintenance job.
