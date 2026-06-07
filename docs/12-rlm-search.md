# RLM search — recursive-LM recall without a vector index

**Status: IMPLEMENTED (v1)** — `cmd/bobrik-watch/programs/search@v1.js`
+ `tool-descriptions/search.md`, tested by
`cmd/bobrik-watch/tests/js/search_test.js` (scripted mock LLM) and
verified live end-to-end. One deliberate divergence from the original
design: cell containment uses `new Function` parameter scoping instead
of the IIFE-over-`js.eval` sketch — see §5, which describes what was
actually built. §9 lists the still-open questions.

The approach is taken from *Recursive Language Models* (arXiv
2512.24601, local copy in `rlm.md`): treat a corpus too large for a
context window as part of an **external environment** the model
interacts with through a persistent REPL, rather than as prompt text.
The model writes code that probes, slices, and programmatically maps
cheap sub-LLM calls over snippets — only metadata and distilled results
ever enter its own context.

## 0. Why this exists

`any` has **no search**. No vector index, no FTS:

- `anyHelper.search()` is a stub returning `[]`
  (`cmd/bobrik-watch/anyHelper.js:618` — "no full-text index on the any
  backend yet").
- `convmemory.search()` runs in degraded mode — explicit/detected time
  ranges, category filters, recency. No similarity ranking
  (`tool-descriptions/convmemory.md`, `docs/11-agent-memory.md`).
- Semantic recall over `agent_memory_items` is documented as a
  non-functional TODO until an external vector service lands
  (`docs/07-roadmap.md` §9).

Meanwhile every dataset is reachable through indexed, paged reads
(`anyHelper.getObjects({type|objectId, dataset, filter, sort, limit,
offset})`, `$regex` prefilters, `describeType` counts) — i.e. the
corpus is already a perfectly good *environment*. What's missing is the
loop that reads it without flooding the calling agent's context.

The RLM result that matters here: a model orchestrating code over an
offloaded corpus, mapping sub-LM calls over slices, beats both
compaction and retrieval scaffolds on information-dense tasks — and the
REPL-offloading alone (depth=0) is what enables working beyond the
context window at all. We already have the REPL. This program adds the
loop.

## 1. The RLM thesis applied to `any`

Mapping the paper's ingredients onto existing infrastructure:

| RLM ingredient (paper) | bobrik-watch mechanism |
|---|---|
| Persistent REPL environment ℰ holding the prompt/corpus | the Sobek kernel; cells via `js.eval(code, args, {persistent: true})` (`toolcall_core@v1.js`) |
| Prompt as variable, model sees only metadata | corpus stays behind paged `anyHelper`/`convmemory` reads; root prompt carries **counts and schemas only** |
| Programmatic sub-LM calls inside loops ("symbolic recursion") | `llm.classify` / `llm.chat` callable from cells; **`llm.completeBatch(prompts, tier)`** (`llm.js:733`) issues N completions in one `fetchBatch` round trip — a parallel map |
| Constant-size metadata of stdout per iteration | the `toolEffects` stash pattern (`toolcall_core@v1.js:1463-1475`): full value kept in the kernel, counts+sample summary in context — lossless offload, not truncation |
| In-context decomposition examples (Fig 4a — the highest-leverage prompting lever) | per-scope worked examples in the search root's system prompt (§4) |
| `Final` variable terminates the loop | `rlm.final(obj)` sentinel (§4) |
| depth=1 sweet spot | sub-calls are haiku one-shots; no nested RLM by default |

One structural difference from the paper: their prompt *P* is a single
giant string; ours is a **lazy corpus** — datasets behind indexed
queries. That's strictly easier: the environment already supports
selective access (filters, sorts, ranges), so "peek into the variable"
becomes "page through an index", and lexical `$regex` prefilters play
the role of `P.find(...)`.

The two long-context corpora this serves first:

```
chat object                          space brain object
├── agent_turns   (seq index)        └── agent_memory_items
└── agent_chunks  (seq, periodEnd        (category, createdAt,
    + fromSeq..toSeq drill-down)          validFrom indexes)

plus: every other object/type in the space (objects scope)
```

## 2. Architecture

```
parent agent cell
│
│  search("what did we decide about lexid ordering?", {scope: "memory"})
▼
search@v1  ───────────────────────────────────────────────────────────┐
│   isolated ROOT LOOP — own messages[] (like subagent), sonnet tier  │
│                                                                     │
│   while not final and toolcalls < MAX_TOOLCALLS:                    │
│     root LLM  ──run_cell──▶  js.eval(wrapped cell, {persistent})    │
│                              SAME kernel, scoped under __rlm        │
│                cell code: rlm.candidates → page corpus →            │
│                           rlm.mapClassify (haiku completeBatch)     │
│                           → rank → rlm.hydrate(top-k)               │
│                ◀── metadata-only tool_result (full values stashed)  │
│     ...until cell calls rlm.final({results, answer?})               │
└─────────────────────────────────────────────────────────────────────┘
│
▼
parent gets ONLY: {ok, mode, scope, results[k], answer?, stats}
```

Key properties:

- **Isolated LLM context, shared runtime.** The root loop has its own
  `messages[]` — the parent agent's conversation never sees a single
  scanned record. But cells execute against the *same* persistent
  kernel (no second runtime, no process). The parent is suspended
  synchronously inside the `search(...)` call, so there is no
  interleaving — sequential mutation only, contained by §5.
- **Not subagent, not toolcall_core.** `subagent.delegate` spawns a
  nested `toolcall_core` invocation whose `main()` calls `js.reset()`
  (`toolcall_core@v1.js:1704`) — reusing it here would nuke the parent
  agent's kernel state mid-turn. And `toolcall_core` carries chat
  history load/persist, the debug collector, space-context injection,
  compression — all wrong for a search subroutine. `search@v1`
  implements its own lean loop (§4), borrowing only the *patterns*
  (RUN_CELL tool shape, execute→format cycle, stash discipline).
- **Context bloat is the enemy this kills.** Today a parent cell that
  pages 200 memory items gets the whole array echoed back into its
  context: `formatToolResult` renders `"Last value: " +
  displayValue(lastValue)` and `displayValue` **never truncates**
  (`utils@v1.js:73`, `toolcall_core@v1.js:1482`). Inside `search@v1`
  that data never reaches *any* LLM context — it lives in kernel
  variables; the root sees shapes and counts; the parent sees k
  results.

## 3. API surface

One method plus one alias — parity with `anyHelper.search` /
`convmemory.search` (query in → ranked results out), not a method per
corpus. The paper's premise is a *general* loop steered by examples;
separate `memory()/history()/objects()` entry points would fork the
loop and guardrails three ways for no behavioral gain, and would give
the future vector service three integration points instead of one.

```js
search(query, opts) -> {
  ok:    true,
  mode:  "rlm" | "rlm+semantic" | "fallback-recency" | "error",
  scope: "memory" | "history" | "objects" | "auto",
  results: [
    {
      id, kind,            // kind ∈ "item" | "chunk" | "turn" | "object"
      score,               // 0..10 from the map-classify pass
      title, snippet,      // human-scannable; snippet is the matched region
      source: { type, dataset?, objectId, link },  // any:// link for citing
      why                  // one line from the sub-LLM: why this matched
    }
  ],
  answer?:    "...",            // iff opts.synthesize (or ask())
  citations?: [{id, link}],     // subset of results backing the answer
  note?:      "...",            // e.g. which fallback ran, coverage report
  stats: {
    rootTurns, subCalls, batchedSubCalls, objectsScanned,
    coverage,             // {scanned, total} per corpus touched — honest accounting
    tokensIn, tokensOut, ms,
    wrappedUp: bool       // true if MAX_TOOLCALLS / context ceiling forced an early final
  }
}

ask(question, opts)   // alias: search(question, {...opts, synthesize: true})
                      // answer always present; results become the evidence set
```

`opts`:

```js
{
  scope: "auto",            // "memory" | "history" | "objects" | "auto"
  k: 8,                     // how many results to return
  synthesize: false,        // also produce answer + citations
  type, dataset,            // narrow objects scope to one type/dataset
  categories,               // memory scope: category filter
  periodFrom, periodUntil,  // memory/history: time range
  chatId,                   // history scope: which chat (default: primary)
  maxToolcalls,             // override MAX_TOOLCALLS (root cells)
  maxRootTurns              // override root LLM turn cap
}
```

Contract notes:

- `{ok, mode, results, note}` is a **strict superset of
  `convmemory.search`'s return shape**, so `search` can transparently
  back it later (§9.6). `stats`, `answer`, `citations`, `results[].why`
  are additive.
- `results[].why` is the RLM's distinctive value over any index lookup:
  the sub-LLM that scored the snippet states *why* it matched, so the
  parent agent can trust or discard a hit without re-reading the
  corpus.
- `ask()` exists because "answer + citations" is a different *return
  contract* (synthesis) than "ranked records" (search), and a caller
  asking a question shouldn't need to know about `synthesize: true`.
- **No `budget` option, no cost ceiling.** Bounded-ness comes from
  toolcall/turn caps with an explicit wrap-up, never from a spend
  number (§7).

## 4. The root loop

A purpose-built loop inside `search@v1.js` — roughly 1/10th of
`toolcall_core`:

```
buildSystemPrompt(scope, corpusMeta)
messages = [ {role: "user", content: query + opts} ]
toolcalls = 0
while true:
  resp = llm.chat(messages, {system, tools: [RUN_CELL_TOOL], tier: "reason"})
  for each tool_use block:
      toolcalls++
      result = js.eval(wrap(code), args, {persistent: true})   // §5 wrapping
      if __rlm.final is set:  return assembleReturn(__rlm.final, stats)
      messages += tool_result(metadataOnly(result))            // stash discipline
  if resp.stop_reason == "end_turn":      // model thinks it's done w/o final
      messages += "emit rlm.final(...) in one cell"            // one nudge, then assemble best-so-far
  if toolcalls >= MAX_TOOLCALLS or contextSize(messages) > CONTEXT_CEILING:
      messages += WRAPUP_PROMPT            // "caps reached — final NOW with best-so-far + coverage note"
      // one last turn allowed for the final cell only
```

Borrowed from `toolcall_core` (the patterns, not the program):

- the `run_cell` single-tool shape (`RUN_CELL_TOOL`,
  `toolcall_core@v1.js:65-92`) and the execute→`js.eval`→format cycle
  (`executeToolUse`, :1502);
- the stash discipline: when a cell's value or trace is large, the
  **full value stays in a kernel store** and the tool_result carries
  shape + counts + first/last sample + the handle to fetch it
  (`_buildEffectsContent` / `toolEffects`, :1463-1475). Inside the
  search loop this applies to `lastValue` too (via
  `inferSchema`-style previews from `utils@v1`), not just traces.
  **This is offloading, not truncation — no data is ever dropped**;
  the root can always pull the full value into a later cell by handle.

Deliberately absent: `js.reset()`, chat-history load/persist,
turn-compression, debug collector, space-context injection,
mention-gating, effects→chat persistence. Errors in a cell flow back
as `is_error: true` tool_results and the root self-recovers, same as
the main loop.

### System prompt structure

1. **Role.** "You are a search subroutine inside another agent's run.
   There is no user. You cannot send messages. Your only output is
   `rlm.final({...})`."
2. **Corpus metadata, never corpus.** Counts and schemas only:
   relevant `describeType` summaries, total objects per type, memory
   category inventory with per-category counts, turn/chunk counts for
   the chat. (Constant-size, the paper's `Metadata(state)`.)
3. **The cell environment.** `anyHelper` (paged reads, `$regex`),
   `convmemory` (indexed reads, `expandChunk`), and the `rlm.*`
   primitives (§6). Explicitly: "page with limit/offset; never pull a
   whole dataset into one value; map `rlm.mapClassify` over batches."
4. **Per-scope worked decomposition examples** — 2–3 complete cells
   each (§6). The paper's Fig 4a shows these dominate the quality of
   the first decomposition attempt, which in turn dominates overall
   quality — this is where most iteration effort should go.
5. **Output contract.** `rlm.final({results: [...], answer?, ...})`,
   the result-record shape, and the wrap-up rule: "if told caps are
   reached, emit final immediately with best-so-far and state in
   `note` what was not scanned."

### Termination

- **Primary:** a cell calls `rlm.final(obj)` — host detects the
  sentinel right after the eval and returns without another LLM turn.
- **`end_turn` without final:** one nudge message; if the next turn
  still doesn't produce it, the host assembles best-so-far from
  `__rlm.s` (whatever the root accumulated) with
  `mode: "fallback-recency"` semantics if nothing usable exists.
- **Caps (§7):** wrap-up prompt, one final turn.

## 5. Kernel sharing and containment

Search cells run in the **parent agent's runtime** — that's the point
(no isolated runtime; intermediate state is ordinary JS data; §10's
future direction depends on this). Two containment problems:

1. **Variable clobbering.** A root-emitted cell doing `var rows = ...`
   must not collide with a parent kernel `rows` (in the persistent
   kernel, top-level `var` lands on the global object —
   `toolcall_core` *relies* on that leak for cell-to-cell state).
2. **Leftover state.** Search scratch surviving after `search()`
   returns would confuse the parent's later cells.

**As built** (supersedes the original IIFE-over-`js.eval` sketch; the
original §9.1 open question is resolved): root cells execute via

```js
new Function("rlm", "s", "anyHelper", "convmemory", "llm", "query", code)
```

— plain function compilation, **no `js.eval` at all** (which also
sidesteps the nested-js.eval question: the parent's cell is itself
inside a `js.eval` when it calls `search.search(...)`). Properties,
all verified by smoke test + `search_test.js`:

- top-level `var`s in a cell are function-scoped — nothing reaches
  `globalThis`, no cleanup needed, no `__rlm` global exists at all;
- `s` is an ordinary closure-held scratch object that persists across
  the root's own cells (and holds the lossless stash of large returns
  as `s._<n>`); it's garbage when `search()` returns;
- the primitives and data helpers arrive as explicit parameters, so
  the root sees OUR instances (noTrace client) regardless of kernel
  state — which also makes the loop runnable outside the kernel
  (tests, `any-agent-runtime`);
- cells use `return <value>` to surface a value — there is no
  implicit last-expression capture (the cell contract in the system
  prompt says so);
- **remaining hazard:** a bare `x = 1` assignment (no `var`) DOES
  create a kernel global — Sobek is non-strict here. The cell
  contract instructs `var`-always; not a security boundary.

Safety argument unchanged: the parent is blocked inside a synchronous
`search(...)` call in its own cell — kernel access is strictly
sequential.

What search does **not** do: `js.reset()` (would destroy the parent's
session state), writes to any dataset (read-only; the salience-bump
question is §9.3).

## 6. `rlm.*` primitives and per-scope strategies

The paper's recommendation — and its OOLONG ablation evidence — favors
a **general loop steered by in-context examples** over hardcoded
pipelines: frozen pipelines can't adapt the decomposition to the query,
which is the whole game. But pure improvisation wastes root turns
reinventing batch-classify every call. So: ship a *small* set of
composable primitives, teach their composition through the §4 examples.

Bound only inside search's cell wrapper (not parent-visible — that's
§10):

```js
rlm.candidates(scope, query, opts)
//  → [{id, kind, title}]  — ids+titles ONLY, the cheap generator stage.
//  Internally: if globalThis.__searchService exists → use it (§8);
//  else lexical prefilter: $regex tokens over title/snippet fields,
//  category/period indexes for memory, chunk summaries for history.
//  THE single function the vector service swaps in behind.

rlm.mapClassify(snippets, query, {batch = 20})
//  → [{idx, score: 0..10, why}]
//  llm.completeBatch (classify tier = haiku) over batched snippets.
//  The parallel map — the paper's sub_LLM(slice) inside a loop.

rlm.mapExtract(snippets, question, {batch = 20})
//  → [{idx, extract}]   — per-snippet extraction, feeds synthesis (ask).

rlm.hydrate(ids)
//  → full records via indexed id queries — top-k only, after ranking.

rlm.final(obj)
//  sets __rlm.final; host returns after this cell.
```

Per-scope decomposition examples (live in the system prompt as worked
cells; sketched here):

- **memory** — temporal phrase in query → `convmemory.byPeriod` slice;
  else `rlm.candidates("memory", q)` fanned across matching categories
  → `mapClassify` batches → rank → `hydrate` top-k. Items' `context`
  field (≤512B one-liner) is the snippet — cheap to batch.
- **history** — coarse-to-fine, exploiting the chunk layer:
  scan `agent_chunks` *summaries* first (one chunk ≈ 10 turns — a
  20-chunk batch covers ~200 turns in one haiku call), `mapClassify`
  them, then `convmemory.expandChunk(seq)` ONLY the promising ranges
  and `mapClassify` the raw turns inside. This is the paper's
  recursive-decomposition pattern landing on a structure
  (`fromSeq..toSeq` pointers) that was built for exactly this
  drill-down.
- **objects** — probe first (`describeType`, counts), use priors to
  narrow (the paper's BrowseComp behavior), `$regex` lexical prefilter
  where the query has distinctive tokens, page titles+ids with
  limit/offset, `mapClassify` snippets, `hydrate` winners. For
  `synthesize`, follow with `mapExtract` over the winners and one
  summarize-tier reduce.

## 7. Guardrails — caps with wrap-up, never budgets or truncation

Two rules (project decision, overriding anything the paper's cost
discussion might suggest):

1. **No budget enforcement.** No dollar ceilings, no token-spend
   aborts. `stats` *reports* tokens honestly; nothing acts on a spend
   number.
2. **No lossy truncation.** Data is never dropped to fit. Large values
   are stashed in the kernel with metadata handles (lossless, always
   retrievable); corpus coverage is never silently capped — if the
   loop didn't scan everything, `stats.coverage` + `note` say exactly
   what was and wasn't covered.

What bounds the loop instead:

- **`MAX_TOOLCALLS`** (default ~12 root cells) and **`maxRootTurns`**
  (default ~8 LLM turns). On exhaustion: a **wrap-up turn** — the host
  tells the root "caps reached, emit `rlm.final` now with best-so-far;
  state unscanned remainder in `note`" — and allows exactly one more
  cell. `stats.wrappedUp = true`.
- **Context-ceiling fallback.** The host tracks approximate root
  context size (chars/4 across `messages`); crossing `CONTEXT_CEILING`
  (well under the model window, e.g. ~60% — tune later) triggers the
  same wrap-up path. This can't be avoided by discipline alone: a
  pathological corpus can produce big tool_results even through
  metadata summaries.
- **Early-exit on confidence** — the inverse guardrail: examples teach
  the root to stop scanning once ≥k hits score above threshold
  (`stats.earlyExit = true`). Most searches should finish in 2–4
  cells, nowhere near the caps.

Tier routing: root = `reason` (sonnet) — it orchestrates, never reads
bulk data; map/classify = `classify` (haiku) via `completeBatch`;
synthesis reduce = `summarize` or `reason` depending on `ask` depth.

Latency sketch (500 memory items, batch 20): 25 prompts through ONE
`completeBatch` round trip ≈ 1–3s, plus 2–3 sonnet orchestration turns
≈ 2–4s each → **~5–12s end-to-end**. (Sequential would be 25s+; the
batch primitive is what makes this usable.) History scope is cheaper
still: the chunk layer compresses 10:1 before any raw turn is read.

## 8. The vector-service seam

`convmemory@v1.js` already specifies the contract the future external
vector service must satisfy (quoted verbatim, `convmemory@v1.js:455`):

```
// Interface the future vector service must satisfy:
//   globalThis.__searchService.query({ text, chatId?, categories?, k })
//     -> [{ id, score, kind }]   // kind ∈ item|chunk|turn; id = record id
```

`rlm.candidates` is designed as the **single integration point**: when
the service is injected, candidates come from `__searchService.query`
instead of lexical/index prefilters, `mode` flips `"rlm"` →
`"rlm+semantic"`, and *nothing else changes* — the map-classify rerank,
hydration, synthesis, guardrails, and return shape are identical.

That end state is strictly better than either half alone: vectors give
recall over fuzzy phrasing the lexical prefilter misses; the RLM map
gives precision and the `why` rationale a similarity score can't. The
RLM loop is not a stopgap to delete when vectors land — it's the
reranker/synthesizer that survives, with a better generator stage. And
until the service exists, search is *functional* instead of a 501-style
degraded mode.

## 9. Open questions (iterate here)

1. ~~Sobek IIFE/persistent-`var` semantics.~~ **RESOLVED** — moot:
   cells run via `new Function` (§5), whose function scope contains
   `var`s by ordinary JS semantics. Verified by smoke test and the
   containment assertion in `search_test.js`.
2. **`fetchBatch` concurrency ceiling.** What batch size is safe
   before provider 429s? Determines the `batch` default (20?) and
   whether very large maps need chunked `completeBatch` calls with
   backoff.
3. **Should search write?** Bumping `salience`/`accessCount`
   (`evolveMemory` allow-list) on returned memory hits would make
   recall reinforce itself — but turns a read into a mutation.
   Default off; revisit after usage data.
4. **`scope: "auto"` routing.** Metadata sniff by the root (free, costs
   a turn) vs a dedicated haiku classify upfront (one extra call,
   saves a turn)? Start with root-decides; measure.
5. **Citation fidelity for `ask`.** Enforce that every `answer` claim
   maps to a `citations[]` id? Possibly a verification sub-pass
   (mapClassify over answer-sentences × cited snippets). Adds a turn;
   maybe `opts.verify`.
6. **`convmemory.search` cutover.** Have `convmemory.search` delegate
   to `search@v1` when present (clean for existing callers, couples
   the programs) vs migrate callers and retire it. Leaning delegate.
7. ~~Stats fidelity.~~ **RESOLVED** — `llm.chat` returns `usage` in
   the full response body; for batched maps llm.js gained
   `completeBatchDetailed(prompts, tier)` → `[{text, inTokens,
   outTokens}]` (same fetchBatch round trip, usage extracted per
   provider shape). `stats.tokensIn/Out` sums both.
8. **Recursion depth >1.** Should a search cell be allowed to call
   `search()` again (sub-RLM, paper's depth=2 — wins on
   quadratic-density tasks)? Needs a depth counter on `__rlm` and a
   re-entrancy story for the namespace (stack of `__rlm` frames).
   Out of scope for v1; design the namespace as a stack anyway.
9. **Where examples live.** System-prompt-embedded (simple, version-
   locked with the program) vs agent-skill objects (editable live,
   like `_toolcaller`). Start embedded; promote to a skill if
   iteration outpaces program redeploys.

## 10. Future: the non-isolated variant

The isolated `search` tool is deliberately the *first* RLM in the
system, not the only one. Because its cells already run in the shared
kernel, the generalization is exposure, not re-architecture:

1. **Parent-callable map primitives.** Export the §6 primitives (or a
   thin `llm.map(items, instruction, {tier, batch})` over
   `completeBatch`) to the main agent's tool surface, so the agent can
   apply the decompose→map→reduce pattern to **any data it has built
   in its own kernel** — a fetched API payload, a built-up array, a
   markdown export — not just the indexed corpora. Results come back
   as plain JS data to filter with normal array methods (same
   ergonomic as `toolEffects`).
2. **Auto-stash of large `lastValue`s.** Fix the §2 bloat at the
   source: when a parent cell's `lastValue` exceeds a threshold,
   `formatToolResult` stashes the full value in a kernel store (the
   `toolEffects` pattern generalized to values) and the context gets
   `inferSchema(value)` + a handle — lossless, fetch-by-handle, never
   truncation. With (1)+(2), the main agent *is* an RLM whenever it
   needs to be: big values stay symbolic, and it maps sub-LLM calls
   over them by handle.
3. **A decomposition skill.** Per the paper's Fig 4a, worked examples
   are the lever — an agent-skill teaching probe→decompose→map→reduce
   over kernel data would let the main loop adopt the pattern without
   any further mechanics.

Sequencing: ship `search@v1` first — it validates the primitives,
prompt patterns, and containment in an isolated, measurable surface —
then promote the primitives outward.

## Implementation map (all landed)

| Artifact | What |
|---|---|
| `cmd/bobrik-watch/programs/search@v1.js` | the program: root loop, `rlm.*` primitives, prompts, stats; `createSearch(deps)` factory for injection |
| `cmd/bobrik-watch/tool-descriptions/search.md` | tool description + method schema (`search`, `ask`, `createSearch`) for toolhood |
| `cmd/bobrik-watch/programs/llm.js` | added `completeBatchDetailed` (batched completions with per-prompt token usage) |
| `cmd/bobrik-watch/tests/js/search_test.js` | loop mechanics with a scripted mock LLM: final/nudge/wrap-up/fallback/containment/stats (jsrunner harness) |

Measured live (all tiers pinned to sonnet):
- memory scope, 19 items: `search` 1 root turn / 1 cell / 1 batched
  map → 12.1s, 2.8k in / 0.9k out tokens; `ask` 2 turns → 17.1s,
  5.5k in / 1.1k out.
- objects scope, 8 pages with deliberately uninformative TITLES
  (semantics only in markdown content — forces the two-stage
  title-scan → hydrate → content-classify pattern): `search` found
  the right page at score 10 with full-coverage note — 4 turns,
  32.4s, 13.3k in / 1.3k out; `ask` answered correctly with citation
  — 6 turns, 45.0s, 24.5k in / 2.3k out.

Pinning the `classify` tier to haiku (config@v1 TIERS) makes the map
stage ~10× cheaper and faster — the loop already routes maps through
that tier.
