# SearchBench M0: a three-layer slice on one snapshot

SYN-391 follow-up. Roma asked for this design on 2026-11-11.

## Why

The local embedder was chosen from end-to-end **hybrid** results. That mixes the embedder's quality with fusion, FTS
and labelling effects. Three examples:
- **Label bias.** EG2's −0.15 on one-word lookups was mostly label bias from pages production had served.
- **The weight is a switch.** vectorWeight acts as a switch, not a dial (≤0.8 puts FTS-only hits first, ≥1.2 puts
  vector-only hits first).
- **Short queries were never measured.** Real queries are mostly short lookups that the curated sets never measured.

The goal is to separate the three decisions:
1. which **embedder**, from a vector-only benchmark;
2. which **FTS** settings, from an FTS-only benchmark;
3. which **fusion** rule and constants, on frozen leg results.

Each one is judged on the same queries and labels.

M0 is the smallest vertical slice. It should prove that the separation works and answer two questions:
- **Q1:** with fusion out of the picture, how do Qwen3 and EG2-head compare on vector-only search, per query shape?
- **Q2:** for each embedder, which fusion rule wins per shape, and how far is each rule from the oracle bound?

**Non-goals:**
- M0 decides nothing. It measures, and its numbers feed the full benchmark design.
- No FTS tuning: FTS runs once, as it ships.
- No recall-gate benchmark.
- No Russian. The snapshot has 16 Cyrillic docs; Russian stays with cheggaaa's account and the public sets.

**Anti-goals:**
- Any label that comes from a page some system served.
- Any content leaving Roma's Mac.
- Tuning a rule and confirming it on the same queries without saying so.

## Data

- **Snapshot.** A copy of Roma's Any Nightly `index.db`, taken 2026-11-10 23:40. It is opened only as a copy, with
  sha256 recorded.
  - Spaces: Team Agents (3,309 docs; 2,624 embedded) and Agents (1,244 docs).
  - Roma cleared this data for the work. It is processed only on his Mac, by Claude agents, and results leave only as
    aggregates.
- **Embedders:**
  - **Qwen3-Embedding-0.6B Q8, 1024 dims.** Doc vectors are taken from the snapshot, which is what production
    searches. Query vectors use the production prefix and last-token pooling.
  - **EG2 Q8 with the head projection.** `v' = normalize(v − u(u·v))`, with `u` from `eg2_head_patch/head_mean.npy`;
    768 dims; mean pooling. Doc prompt `title: none | text: `, query prompt `task: search result | query: `. It
    re-embeds the same doc texts that carry a Qwen3 vector in the snapshot.
  - **Runtime.** Both embed through the Go embedder child (`run.sh` / TestEmbedBench, llama.cpp b11600, Metal).
    Queries embed as docs with the query prompt set as the doc prefix. That is identical to `EmbedQuery`: the same
    pooling with a different prefix.
- **Queries.** About 300 in total, frozen in `queries.jsonl` with a sha256 before any system runs:
  - **The existing 177:** 59 targets × {one word, 2–3 words, question}, written by a Sonnet agent from the record. 3
    weak targets are excluded.
  - **About 120 new:**
    - **id-like (code):** a ticket or ADR id, file name or identifier unique to a target, by regex over the target
      text. Only ids that occur in fewer than 5 records.
    - **name (code):** a prefix of the target object's name, 1–2 words.
    - **typo (code):** a one-edit typo of the one-word query.
    - **two-stage paraphrase (agents):** a Haiku agent sees the record and writes the *need* in a sentence. A second
      agent sees only the need and writes a 2–5-word query. It's tagged `para2`.

    Shapes are tagged by code from the query text, not by the generator. Token overlap with the target is measured
    and reported.

## Layers

- **L0 integrity** (failure aborts the run):
  - offline re-fusion reproduces the production page;
  - the offline exact Qwen3 vector leg matches the production vector leg the harness dumped;
  - no hybrid page reports `vectorStatus: used` with an empty vector leg while `rest > 0`;
  - every vector has unit norm and no NaN.
- **L1 vector only:** exact cosine over each space's embedded chunks. A record scores its best chunk; there is no
  title pass and no FTS. Qwen3 and EG2 are compared on the same queries.
- **L2 FTS only:** the production FTS leg and title pass, from the fusion_sweep dump, grouped like production. One
  configuration.
- **L3 fusion:**
  - **Inputs:** the frozen title block, the FTS leg and one vector leg per embedder. Qwen3 uses the production dump;
    EG2's leg is computed offline with production's window rule (at least 30 chunks, covering `rest` records, title
    records skipped).
  - **Rules:** RRF at w1, w2 and the shape rule (w1 for one-token and id-like queries, else w2); the min-max score
    blend at α 0.4; FTS-only; vector-only.
  - **Oracle bound:** per query, the better of the FTS-only and vector-only pages.

## Labels and metrics

- **Known-item:** the target object. Any hit from it counts, including a title hit. Metrics are MRR@10, hit@1 and
  recall@10.
- **Judged alternatives:**
  - **What gets judged:** for queries where systems disagree on rank 1, every non-target record in any system's top 3
    is judged blind, with no system names. Two Haiku votes grade each pair 0–3, and Sonnet decides splits.
  - **Lenient metric:** success@1 and success@3, which count the target or a record graded ≥2.
  - **Calibration:** Roma labels 30 pairs to check the judge. Until then, lenient numbers carry an "uncalibrated"
    mark.
- **Statistics:**
  - paired bootstrap over targets (a target's queries resample together), 95% CI, per shape;
  - every number is reported per shape, and no aggregate is weighted by a real query mix yet;
  - fusion α and the shape rule were chosen on the first 177 queries, so they are confirmed only on the new ~120, and
    the report says so.

## Reproducibility

The run is reproducible from these files:
- the snapshot hash and model sha256s;
- the code commit (this branch plus `fusion_sweep`);
- `queries.jsonl`, the vector dumps and leg dumps;
- the judgment cache, keyed by query, record, chunk hash and judge-prompt version.

LLM steps (query writing and judging) run once and are cached; reruns reuse them. Private files stay under the scratch
dir and never enter `bench/` or git. Git gets only code and aggregate results.

## Calls made after the builder check (2026-11-11, mine; Roma was away)

1. **Held-out means new targets, not only new queries.** 63 new targets were sampled at record level, never from a
   served page, with every query shape generated for them. That gives 120 targets and 679 queries in total.
   - Tune split: 340 queries on the first 59 targets.
   - Held-out split: 339 queries on the 63 new ones.
2. **Exact search for both embedders in L1 and L3.** The production Qwen3 vector leg is used only in L0. Its
   tolerance is reported, not gated: mean top-10 overlap with exact search, given different llama.cpp builds for the
   query vectors.
3. **What the judge sees:** the query, the written need (from a need-writer that saw the target) and the candidate
   record. It never sees the target record or which system found the candidate.
   - What gets judged: rank-1 non-target records wherever w0.5/w1/w2 (Qwen3, EG2) or L1 Qwen3/EG2 disagree.
   - Hidden controls: 60 targets and 60 random records.
4. **Persistence:** private inputs live in `~/searchbench/roma-nightly/m0/`, outside every repo. Git gets code and
   aggregates only.
5. **`u` is unit-norm**, as checked, and the projection applies to both docs and queries.
6. **Label rule fix:** memories, chat messages and config records live in container objects, so they match by record.
   Page blocks match by page. The first object-level pass inflated every MRR for 42 of 59 targets.

## Left to the builder

File layout, script structure, the exact regexes, batch sizes and agent counts within about 10 agents.
