---
title: Memory and recall
description: A small, high-signal memory store with dedup on save, topical auto-recall on every turn, hierarchical history rollups, and background maintenance jobs.
order: 30
---
# Memory and recall

The agent's memory is two channels with different jobs. **History** (turns and chunk summaries) keeps everything verbatim and drillable; **memory items** hold only distilled, stable facts. Recall composes both, plus the space's own content, through one surface.

## Memory items

A memory item is a record in the `agent_memory_items` dataset on the space's brain object. Required at save time: `category` (an open slug set — preference, decision, lesson, fact, …) and `context` (a one-line fact). Optional: `body`, `confidence` (1–10; user-stated facts outrank inferred ones), `importance`, `tags`, `entities`, `keywords`, `edges` (typed links to other objects), `validFrom`.

Save policy, taught by the `_memory` skill and enforced by the write path:

| Save | Skip |
|---|---|
| stable preferences | greetings, meta-chatter |
| decisions **with the why** | restatements of what was just said |
| durable domain facts | anything already in history (it is drillable) |
| hard lessons, shipped outcomes | low-confidence speculation |

Budget is ~1–2 saves per turn. Episodes and session summaries never become memory items — that is the history channel's job.

```python
c = use("agent:any@v1")
r = use("agent:recall@v1").recall(c, space)
m = use("agent:memory@v1").memory(c, space)
m.save_with_dedup({"category": "decision",
                   "context": "picked sqlite for the importer: simpler ops",
                   "confidence": 8}, recall=r)
# → {id, ...}  or  {deduplicated: true, mergedInto: "<id>"}
```

### Dedup on save

Every save first runs recall over scope `agent`, then a cheap classify-tier judge decides *same fact?* → **merge** (evolve the existing item), **supersede** (new item plus a `supersedes` edge) or **create**. A `{deduplicated: true}` reply is a success. Merges are humble: a machine-sourced candidate never overwrites user-stated text, never lowers confidence, and unions tags and edges. There is no similarity threshold — the judge decides.

## Recall: one surface, three axes

`recall@v1` is read-only and binds to one space:

- **Semantic** — `r.search(q, scopes=["agent", "history", "basic", "email"])` over the [search index](../search/index.html); `r.hydrate(hits)` turns hits into full records in one read.
- **Temporal** — `r.by_period(from, to)` merges memory items (`validFrom`), turns (`createdAt`) and chunks (`periodStart`).
- **Graph** — `r.neighbors(id)` walks forward link properties plus server backlinks, names resolved.
- **Drill-down** — a chunk expands to its children (chunks or raw turns); a turn's `traceRef` opens the run.

Every recall bumps the item's `accessCount`, so the store measures its own usefulness.

## Auto-recall

Prompt guidance alone does not produce memory behaviour — an agent asked nicely saves and searches only when memory is the topic. So recall is structural: at the start of every turn `autorecall@v1` runs `recall.search(user_message)` (index-backed, no model call) and injects the top hits, budget-capped.

The injection is framed as a synthetic `run_cell` call whose code is the literal recall idiom, plus a digest-shaped result — evidence the model weighs and can discount as stale, not prompt truth. Memory hits render as distilled facts with provenance date and confidence; history hits as "related past discussion, <date>" pointers with a drill-down handle. Guards: hits already inside the boot window are skipped (auto-recall is the topical channel; the window owns recency), a relevance threshold means generic messages inject nothing, and every injection is logged to `agent_roi_injections`.

## History: turns and the chunk pyramid

Turns are append-only records on the chat's log child. `rollup@v1` (a cron trigger) summarises complete batches of ten turns into level-1 chunks, ten level-1 chunks into a level-2 chunk, and so on — level-2+ summaries are built from child summaries only.

```jsonc
{"seq": 12, "level": 1, "fromSeq": 40, "toSeq": 49,
 "summary": "…", "periodStart": …, "periodEnd": …, "unitsCovered": 10}
```

Same-level chunks are contiguous and non-overlapping, and every summary keeps explicit pointers to what it covers, so drill-down never dead-ends. The boot window is token-budgeted over this pyramid: newest raw turns, then L1, then L2 — all history present at decreasing resolution, at roughly 11% record overhead.

## Background jobs

All maintenance runs as [triggers](../scheduling/index.html) with per-run observability; when the bird's-eye view is stale, a run log says why.

| Program | Schedule | State |
|---|---|---|
| `extraction@v1` | cron over newly persisted turns | on — proposes only stable-fact shapes, through the dedup judge, provenance `fromSeq`, confidence capped at 6 |
| `rollup@v1` | cron | on — the chunk pyramid |
| `linkgen@v1` | hourly | on — seed-search neighbours, classify-tier proposes typed links from the curated vocabulary (`relates_to`, `caused_by`, `supersedes`, `decided_in`, `part_of`, `owned_by`, `discussed_in`); never invents edge types |
| `evolution@v1` | cron | ships disabled — refreshes `context`/`tags` from linked neighbours |
| `reflection@v1` | cron | ships disabled — synthesises never-recalled clusters into insights, flags contradictions |
| `decay@v1` | cron | ships disabled — salience halves per idle half-life, never deletes |

Disabled jobs ship their mechanism and activate one at a time behind an eval; scoring fields are recorded before they are consumed.

> **Why it matters.** The memory is a dataset in your encrypted space, not a vendor's RAG store. You can query it (`dataset: "agent_memory_items"`), subscribe to it, audit what the extractor saved and from which turn, and delete an item with a normal record delete. See [Agent data](agent-data.html) for the fields.
