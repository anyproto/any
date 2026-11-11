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
  - **The Air Crash Database space on Roma's Nightly** (61,577 chunks, both embedders), with the rig's cleared
    queries: 51 known-item (17 keyword, 17 paraphrase, 17 cross-language) and 6 graded topical. This is the
    big-space check, and it played no part in designing the router.
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

   **Caveat:** the router was designed after seeing the pooled M0 numbers. The public sets, the Air Crash space and
   cheggaaa's account (replay requested) are its independent checks.

5. **The Air Crash check, and the fragile branch** (`RESULTS_AIR.md`, `RESULTS_F2_LOWCUT.md`).
   - **On Air Crash, w2 helps** (+0.033* Qwen3, +0.057* EG2, article-level MRR), as on the public sets. w0.5 hurts
     (−0.073*, −0.120*).
   - **Keyword lookups are saturated:** every setting scores about 1.0, so this space can't test the full-coverage
     branch.
   - **Paraphrases need both legs:** fusion scores 0.44–0.47, against 0.27 for either leg alone (record level).
   - **The vector-only branch for low coverage is the fragile part.** Its best cut point moves with the data, while
     the full-coverage blend branch holds everywhere:

| Δ against w1 | Roma's data Qwen3 / EG2 | public Qwen3 / EG2 | Air Crash Qwen3 / EG2 |
|---|---|---|---|
| F2, low cut 0.75 | +0.041* / +0.024* | **+0.047* / +0.039*** | −0.013 / +0.069* |
| F2, low cut 0.5 (as above) | **+0.043* / +0.029*** | +0.024* / +0.020* | +0.011 / **+0.074*** |
| F2, low cut 0.25 | +0.040* / +0.026* | +0.017* / +0.010* | **+0.043*** / +0.057* |
| **F2, two branches** (full → blend α0.3, else RRF w2) | +0.040* / +0.025* | +0.014* / +0.007* | +0.033* / +0.057* |

   - **Only the two-branch form is significantly positive on all six columns.**
   - **The best cut on one dataset is the worst on another:** cut 0.75 adds +0.033 on the public paraphrase sets,
     but takes Air Crash's Qwen3 gain from +0.033* to −0.013.
   - **Where it hurts:** 12 of the 17 Air paraphrases have coverage < 0.5, yet their FTS leg still contributes.

   **Embedders on Air Crash:**
   - **Known-item:** EG2-head beats Qwen3 by +0.090 at L1 (article MRR, n.s., 51 queries), and by +0.049 fused at
     w2 (n.s.). The cross-language queries account for most of it: 0.495 against 0.349.
   - **Topical:** Qwen3 0.693 against EG2 0.453 at L1, but on only 6 queries.
   - **L0:** the production Qwen3 vector leg overlaps exact search at 0.979 at this size.

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

**Graded relevance on Roma's data** (M1, `RESULTS_M1_GRADED.md`):
- **Method:** 126 queries, 14 per shape.
  - The pool is the top 10 of w1, w2 and both F2 forms, plus the top 5 of each leg alone (1,591 records).
  - Each record is blind-judged against the written need. That counts the useful records that aren't the known
    item, which known-item labels can't see.
  - Haiku votes agreed exactly 90% of the time, and 100% on the ≥2 side. 68 splits went to Sonnet.

| Qwen3 legs | nDCG@10 | Δ vs w1 | known-item MRR, same queries | Δ vs w1 |
|---|---|---|---|---|
| vector only | 0.617 | −0.155* | 0.504 | −0.191* |
| FTS only | 0.627 | −0.145* | 0.651 | −0.043 |
| w1 | 0.772 | | 0.695 | |
| w2 | 0.717 | **−0.056*** | 0.666 | −0.029 |
| F2 two-branch | 0.788 | +0.016 | 0.731 | +0.036* |
| F2, low cut 0.5 | 0.789 | +0.016 | 0.736 | +0.041* |

- **w2's loss holds up under graded labels.** It isn't an artefact of known-item scoring.
- **The router's gain roughly halves under graded labels, and isn't significant overall** (+0.016).
  - It stays significant on id-like queries (+0.104*) and questions (+0.019*).
  - The w1 page often puts other useful records where the router puts the known item.
- **Leg alone vs fused:** pastes are the one shape where FTS alone (0.846) beats every fused page except the blend
  (0.866). Typos score 0 in FTS and are carried by the vector leg alone (0.602 in every fused rule).

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

**On Roma's data** (M1 graded pool, w1 content hits, Qwen3; 1,243 judged candidates, 31% relevant):

| gate | precision (≥2) | injected per query | queries with ≥1 relevant injected |
|---|---|---|---|
| bao 0.015, cap 5 | 0.40 | 4.96 | 0.90 |
| any-agents 0.030, cap 3 | 0.64 | 1.60 | 0.63 |
| 0.032, cap 3 | 0.85 | 0.71 | 0.51 |

- **bao's 0.015 still injects the full cap,** at 0.40 precision.
- **Signal choice flips with the data.** Here the fused RRF score separates best (AUC 0.735), ahead of raw cosine
  (0.673) and relative cosine (0.581). On the public sets relative cosine was best.
- **So the gate signal should be the fused score**, which is what the clients already have. Only its threshold
  needs moving.

## What this suggests (decisions are cheggaaa's and Tolya's)

- **Don't ship a global vectorWeight of 2.** It is the wrong switch position for lookups, ids and pastes, which make up
  most real queries.
- **Route by FTS coverage instead.** It's one signal from the FTS leg at the fusion site.
  - **The safe default is two branches:** full coverage → min-max blend α0.3, otherwise RRF w2. It's positive on
    every dataset and embedder.
  - **The vector-only branch for low coverage is an optional tune.** Its cut point should come from the account
    replay, not from these sets.
  - **Expect a modest gain.** Under graded labels on Roma's data it is +0.016 (n.s.), mostly from ids, pastes and
    questions. The large, unambiguous effect is avoiding w2.
- **Recall gates:** keep the fused score as the signal, and raise the threshold. bao's 0.015 never filters; 0.030–0.032
  gives 0.64–0.85 precision on Roma's data.
- **FTS work with clear payoff:** typo tolerance, and CJK segmentation.
- **Embedder choice:** on this evidence EG2-head is competitive in fusion. Its real losses are Russian dev slang and
  finance-style Q&A, and both show up only in vector-only tests.

## Not covered yet

- **Human calibration labels:** about 30 pairs from Roma. Until then, every graded number rests on the LLM judge
  (checked only by its controls and vote agreement).
- **Real logged queries on Roma's side:** there are no bao traces on Nightly.
- **The full-coverage branch at scale:** Air Crash keyword lookups are saturated. A big space with harder lookups
  (near-duplicate pages, many partial matches) is still missing.
- **cheggaaa's account replay** of router F2: requested, and needed for the low-cut choice.
