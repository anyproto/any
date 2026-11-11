# SearchBench: what the three-layer benchmark found (2026-11-11, overnight)

SYN-391 follow-up. Design in `DESIGN.md`. Everything below is aggregate. Private inputs stayed on Roma's Mac, outside
git.

## What was run

- **Harnesses** (`*.txt` files drop into `internal/indexer/` at the app's commit):
  - `fusion_sweep`: the production `Search` over an `index.db` copy. It dumps the title block and both legs at chunk
    level.
  - `TestCorpusBench`: any corpus with precomputed vectors, loaded into an in-memory production store.
- **Layers.** One set of queries and labels is scored at every layer:
  - L0 integrity;
  - L1 vector only (exact cosine, no titles, no FTS);
  - L2 FTS and title pass only;
  - L3 fusion on frozen legs, with production's window rule.
- **Data:**
  - **Roma's Nightly snapshot,** Team Agents and Agents. 875 known-item queries on 120 targets, in 9 shapes: one
    word, id-like, name, typo, search-as-you-type prefix, 2–3 words, 4–5 words, question, pasted excerpt. 63 of the
    targets were held out.
  - **9 public and synthetic sets,** 2,064 queries with graded qrels: RU wiki, wiki-mix, MIRACL-RU, MIRACL-DE,
    SciFact, NFCorpus, FiQA-10k, sealed RU dev-slang chat, rig RU look-alikes.
- **Embedders:**
  - Qwen3-Embedding-0.6B Q8 at 1024 dims;
  - EG2 Q8 with the head projection;
  - stock EG2 as a control.
- **Labels:** known-item. A blind judge (2 Haiku votes, Sonnet deciding splits) graded the 880 rank-1 disagreements
  against a written need.
  - It graded all 60 hidden targets 3, and 51 of 60 random records 0.
  - The two Haiku votes agreed exactly 90% of the time, and 100% on the useful / not-useful side.
  - The judged results confirm every known-item direction.

## L0: the pipeline is what we think it is

- Offline re-fusion reproduces 100% of production pages at every weight.
- The production Qwen3 vector leg overlaps exact search at 0.98–0.99 in its top 10 chunks.
- Stored vectors are unit-norm.
- **A labelling pitfall:** memories, chats and config live in container objects, so a known item has to match by
  record. Object-level matching inflated MRR for 42 of 59 targets.

## L1: embedder alone

| | Qwen3 | EG2-head | EG2 stock |
|---|---|---|---|
| Roma's data (MRR, 679 queries) | 0.496 | +0.009 | −0.041* |
| names | 0.286 | +0.118 (borderline) | +0.112 |
| SciFact (nDCG) | 0.701 | **+0.149*** | |
| FiQA | 0.633 | **−0.122*** | |
| MIRACL-RU / RU wiki | 0.836 / 0.868 | +0.023 / +0.029 | |
| RU dev-slang chat (sealed / rig) | 0.781 / 0.946 | −0.033* / −0.102* | |
| Japanese (9 queries) | 0.932 | −0.236* | |

- **On Roma's data EG2-head ties Qwen3.**
- **On public sets the result swings by domain:** EG2 wins science, and loses finance Q&A and Russian dev slang.
- **The head projection is essential:** stock EG2 loses 0.041*.
- **Fusion hides embedder differences.** At L3 w1 on Roma's data, EG2 − Qwen3 is only +0.012 (n.s.).

**Qwen3 at 768 dims against 1024 on Roma's data** (875 queries, `RESULTS_DIM768.md`):
- L1 −0.007 (n.s.);
- fused at w1 −0.001;
- fused at w2 −0.012*.

So the 768 default costs nothing measurable under w1-style fusion.

## L2: FTS alone, and its holes

- **Typos:** FTS scores 0.000 on one-edit typos; it has no fuzzy matching. Only the vector leg finds them, at 0.31.
- **Chinese and Japanese:** FTS scores 0.04–0.11, because the analyzer doesn't segment them.
- **Cross-language (en→ru):** FTS scores 0.000, as expected.
- **Strengths:**
  - pasted excerpts, 0.975;
  - id-like lookups, 0.83;
  - names via the title pass, 0.91 (0.47 without it).

**Two cheap rewrites tested through the production FTS leg** (`RESULTS_FTS_PREFIX.md`; MRR point estimates):
- **Search-as-you-type:** putting `*` on the unfinished last word, as the title pass already does, takes FTS from 0.581
  to **0.710** and fusion at w1 from 0.663 to **0.723** (n=76).
- **Typos:** a prefix fallback (each word cut to its first 3–5 chars + `*`) only reaches 0.113 FTS and 0.326 fused,
  against 0.310 vector-only. Typos need real fuzzy matching.

## L3: fusion

1. **vectorWeight is a three-position switch, not a dial.**
   - **Why:** at k=60 over a ~30-chunk window, a single-leg contribution spans only 1.49×.
   - **≤0.8:** every FTS-only hit ranks above every vector-only hit.
   - **≥1.2:** the reverse, so 1.5, 2 and 3 rank identically.
   - The rig shows the same: 0%, 29% and 44% vector-only slots.
2. **No fixed weight fits both kinds of data:**
   - **Roma's agent-memory data:** global w2 is −0.052* (id-like −0.269*, pasted excerpts −0.088*); w0.5 is +0.017*.
   - **Public paraphrase and question sets:** w2 is +0.012* and w0.5 is −0.051*.
3. **RRF ignores how confident a leg is.**
   - **Exact hits lose:** a mediocre two-leg record beats FTS #1. Pasted excerpts score 0.975 in FTS alone, but 0.764
     fused at w1.
   - **Fusion can trail vector-only search:** on paraphrase sets fusion at w1 often scores below vector-only (public
     average 0.606 against 0.670; RU wiki 0.643 against 0.868).
4. **A query-time router works on both kinds of data.**
   - **Signal:** `cov`, the share of the query's words found in the best of the top-3 FTS chunks.
   - **Router F2:**
     - `cov` = 1 → a min-max score blend with α 0.3 (vector share);
     - `cov` < 0.5 → vector leg only;
     - otherwise → RRF w2.

| Δ against w1 | Roma's data, Qwen3 (MRR) | Roma's data, EG2 | public, Qwen3 (nDCG) | public, EG2 |
|---|---|---|---|---|
| global w2 | −0.052* | −0.050* | +0.012* | +0.003 |
| router C (RRF w0.5 at `cov` = 1) | +0.028* | +0.015* | +0.024* | +0.020* |
| **router F2** | **+0.043*** (held-out targets +0.054*) | **+0.029*** | **+0.024*** | **+0.020*** |

   F2 is never significantly worse than w1 in any shape on Roma's data. Its biggest gains are pasted excerpts
   (+0.200*), id-like queries (+0.080*), RU wiki (+0.166*) and MIRACL (+0.05*).

   **Caveat:** the router was designed after seeing the pooled M0 numbers. The public sets, cheggaaa's account (replay
   requested) and the Air Crash space are its independent checks.

**Constant maps** (`RESULTS_CONSTS.md`, Qwen3 legs; M0 MRR / public nDCG):

| setting | M0 | public |
|---|---|---|
| RRF w1, k=60 | 0.665 | 0.606 |
| RRF w1, k=5 | 0.670 | 0.628 |
| RRF w3, k=5 | 0.588 | 0.657 |
| best blend α (0.4 for M0, 0.8 for public) | 0.697 | 0.662 |
| router F2 region (low cut 0.5–0.75, α 0.2–0.3, middle w2) | **0.705–0.709** | **0.629–0.653** |

- **RRF k barely matters at w ≤ 1.** A small k only helps the vector-heavy public sets at high weights.
- **The best fixed blend α is opposite on the two datasets.**
- **Only the router family is good on both, and it's flat across its cut points,** so it isn't a knife-edge tune.

## L4: recall gate (first pass, `RESULTS_GATE.md`)

Measured on the 9 public sets, whose qrels cover their pooled results. The gate injects a content hit when its fused
score is ≥ θ, up to the cap.

| gate | precision | injected per query |
|---|---|---|
| bao 0.015, cap 5, w1 | 0.25 | 5.00 (100% of queries) |
| bao, normalized 0.4575 at w2 | 0.25 | 5.00 |
| any-agents 0.030, cap 3, w1 | 0.41 | 2.09 (89%) |
| any-agents raw 0.030 at w2 | 0.34 | 3.00, the cap, every time |
| any-agents normalized 0.915 at w2 | 0.43 | 2.12 |
| a stricter 0.032 at w1 | 0.60 | 0.99 (70%) |

- **bao's gate never filters.** It injects its full cap at 0.25 precision, before and after normalization.
- **Scale normalization fixes any-agents only.**
- **The threshold is the problem, not the signal.** Fused score separates relevant from non-relevant candidates almost
  as well as raw cosine: AUC 0.773 against 0.786, and 0.794 for cosine relative to the query's top-1 cosine.

## What this suggests (decisions are cheggaaa's and Tolya's)

- **Don't ship a global vectorWeight of 2.** It is the wrong switch position for lookups, ids and pastes, which make up
  most real queries.
- **Route by FTS coverage instead.** It's one signal from the FTS leg at the fusion site, as in router F2, or C if a
  score blend is unwanted. Its cut points still need confirming on the account.
- **FTS work with clear payoff:** typo tolerance, and CJK segmentation.
- **Embedder choice:** on this evidence EG2-head is competitive in fusion. Its real losses are Russian dev slang and
  finance-style Q&A, and both show up only in vector-only tests.

## Not covered yet

- **The recall gate on Roma's data:** it needs graded negatives beyond rank 1. A first pass on public qrels is above.
- **Human calibration labels:** about 30 pairs from Roma.
- **Real logged queries on Roma's side:** there are no bao traces on Nightly.
- **Big-space behaviour:** Air Crash, 61.6k chunks, is running.
