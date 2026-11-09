# Why EG2 loses on informal Russian, and what helps

**Keep the head patch and exact search. The strongest additional candidate is a
fixed document-side glossary that preserves the slang and appends its Russian
meaning plus English source term. Neither tested extra projection fixes the
Russian paraphrase deficit. This study establishes a controlled vector-retrieval
improvement, not a confirmed fix for the rig's full hybrid search.**

The evidence points to a weak **contextual bridge between Cyrillic developer slang
and a plain-Russian description**, amplified by short, low-redundancy messages.
It does **not** establish that EG2 simply lacks each word's definition, that Russian
is generally weak, or that a universal language direction causes the loss.

On 20 held-out authored slang scenarios, EG2-head MRR@10 is 0.521
versus Qwen 0.731: head−Qwen **-0.209 [-0.404, -0.037]**.
Additive glossary expansion reaches 0.703, an improvement of
**+0.182 [+0.074, +0.307]**; its residual difference from Qwen is
-0.027 [-0.227, +0.170]. This is not an equivalence test.
Without authored topical distractors, its gain is still
+0.181 [+0.073, +0.304], and head−Qwen remains -0.208 [-0.404, -0.037].

The glossary increases correct query–passage cosine by
+0.054 [+0.037, +0.071]
and its margin over the best wrong passage by
+0.039 [+0.020, +0.057].
These are within-EG2 contrasts, avoiding comparisons of uncalibrated raw cosine
scales between models.

All brackets below are paired 95% bootstrap CIs, 4,000 resamples, seed 391.
Queries sharing a target are resampled together. Intervals are pointwise; the
small, selected samples do not estimate workspace-wide prevalence. Full metrics,
slices, ranks and comparisons are in [TABLES.md](TABLES.md) and `results/`.

## Evidence, strongest first

1. **Changing the semantic bridge helps; changing the alphabet alone does not.**
   On held-out Russian paraphrases, replacing Cyrillic slang with its actual
   English source gives +0.089 [+0.022, +0.168]; replacing it with
   the Russian meaning gives +0.171 [+0.005, +0.351]; mechanical
   Latin transliteration gives -0.030 [-0.140, +0.064].
   Additive expansion is the dev-selected primary candidate because it retains
   keyword evidence. Replacement loses keyword MRR on the synthetic stress set:
   -0.191 [-0.270, -0.109]. The additive candidate's
   keyword change is +0.033 [+0.000, +0.092]; on the rig
   keyword queries it remains 1.000, difference [0.000, 0.000].

2. **Independently written, sealed rig examples support the mechanism, but do not
   resolve the aggregate effect.** The eight texts were opened only after
   `FROZEN.json`; no dictionary entries or parameters were added afterward.
   In our 808-document public pool, s04 (hotfix/master/review) moves from head rank
   14 to 1, and s05 (handler/flaky/CI) from 54 to 1. s07 moves 2 to 1. However,
   s08 moves 1 to 5: appending a generic endpoint explanation can dilute the
   specific synchronization/batch-update intent. s01's slang is uncovered and
   already ranks first in this easier pool. Six of eight texts trigger expansion.
   Rig paraphrase head MRR is 0.625, Qwen 0.750,
   glossary 0.900; glossary−head is
   **+0.275 [-0.138, +0.625]**. The supplied rig's 1,434 private
   distractors were never read and are not reproduced here. Absolute ranks from
   those two pools must not be combined.

3. **Public technical Russian does not automatically reproduce the deficit.**
   On ten held-out public StackOverflow paraphrase targets, head MRR is
   0.770, Qwen 0.620, head−Qwen
   +0.150 [-0.160, +0.455]. The supplied RuBQ/wiki observations are
   consistent with a domain/register-specific problem; RuBQ was not rerun.

4. **The isolated-word check weakens the simplistic “Qwen knows the words, EG2
   does not” account.** In 20-way held-out meaning retrieval, head reciprocal rank
   is 0.504 and Qwen
   0.509; difference
   -0.006 [-0.163, +0.151].
   Using the actual English source instead of the Cyrillic loanword improves
   head by +0.359 [+0.190, +0.542] and Qwen by
   +0.253 [+0.132, +0.381]. Both have lexical weak spots;
   Qwen's advantage here is in contextual retrieval. This diagnostic uses full
   reciprocal rank among 20 meanings, not MRR@10. Raw cosine scales across
   models are not calibrated and are not a knowledge test by themselves.

## Geometry, prompts, mixing and hubs

The language contrast is the mean of matched short Russian/English statements,
after the existing head projection, orthogonalized to the head direction. Dev
split-half cosine is 0.785; slang-vs-plain contrast stability is only 0.199.
The five-dose sweeps (−.5, .25, .5, 1, 1.5) choose .25 for language and .5 for
slang on dev paraphrase MRR, with smaller absolute dose breaking ties.

On held-out Russian paraphrases, language removal changes MRR by
+0.004 [+0.000, +0.012], and slang removal by
+0.022 [-0.007, +0.075]. Against their matched random controls the changes
are +0.004 [+0.000, +0.012] and
+0.018 [-0.020, +0.075], respectively. This is far short of
the observed Qwen gap. No writer-side edit was attempted: the slang contrast did
not reveal a clean, consistent component that justified changing a writer tensor.
This rules out these particular edits, not all possible weight-space surgery.

Language removal does align matched RU/EN statement vectors: paired cosine
change +0.031 [+0.028, +0.034].
Its EN-query→RU-document MRR change is
+0.051 [-0.016, +0.135]; RU-query→EN-document change is
+0.009 [+0.000, +0.022]. Better language
alignment therefore does not explain or repair the monolingual slang failure.
Query centering changes held-out slang paraphrase MRR by
+0.003 [-0.075, +0.079]; it is not the missing fix either.

Prompt diagnostics were dev-only. A Russian instruction changes head MRR by
-0.131 [-0.253, -0.030]; an English
“same meaning” instruction by
-0.225 [-0.359, -0.101];
bare queries by +0.033 [-0.023, +0.118].
No prompt was selected. Production prefixes and pooling remain unchanged.

Removing artificial short topical non-answers changes held-out head paraphrase
MRR by +0.001 [+0.000, +0.004]. The gap persists, so the
previous short-hub mechanism is insufficient. The authored hubs do intercept
keyword queries for both models, which is why their removal is reported
separately; the sensitivity restores head/Qwen keyword MRR to
0.925/0.967.
This does not imply that no real chat message can be a hub.

Seven held-out mixed-script examples contain the same generic English debug tail.
The full message's cosine to its meaningful English translation minus its cosine
to that tail is -0.051 [-0.131, +0.030] for head and
-0.066 [-0.159, +0.018] for Qwen. Both are susceptible;
neither interval resolves a difference from zero. This small nuisance-tail probe
and the Latin-share slices do not identify code-mixing as the cause. They are not
a fully crossed causal experiment: length, style and term family vary together.

## Hybrid and Russian analysis: useful, but not a reliable repair yet

The installed `github.com/anyproto/any-store/v2@v2.4.1/internal/fts/analyzer.go`
explicitly implements **NFKC → Unicode case folding → UAX#29 words** (plus CJK
bigrams). It explicitly does **not** stem or fold diacritics. Thus case is already
normalized, `ё` and `е` remain distinct, and Russian inflections do not merge.
The app's optional query stop list in `internal/indexer/stopwords.go` is
English-only. BM25 defaults are k1=1.2, b=.75, with OR semantics.

Our estimator uses a single field and equal RRF(60), 30 hits per leg. It omits
production title boosts, operators, filters, optional English stopword stripping
and adaptive leg weights. The Russian comparison is not affected by the English
stop list, but these are **estimated hybrid results, not a run of the production
index**. Morphology applies Snowball Russian stemming plus `ё→е` to both sides.
Glossary lexical analysis additionally appends the same frozen document glossary.

| Held-out paraphrases | Base head hybrid MRR | Stemmed hybrid MRR | Δ vs base | Glossary lexical hybrid MRR | Δ vs base |
|---|---:|---:|---|---:|---|
| Authored slang (20) | 0.340 | 0.651 | +0.311 [+0.138, +0.492] | 0.731 | +0.391 [+0.230, +0.549] |
| Public Q&A (10) | 0.493 | 0.557 | +0.064 [-0.202, +0.328] | 0.557 | +0.064 [-0.198, +0.327] |
| Sealed rig (8) | 0.688 | 0.348 | -0.340 [-0.567, -0.135] | 0.634 | -0.053 [-0.300, +0.156] |

Stemming helps the authored stand-in but harms the sealed rig examples. It is not
a production recommendation from this study. A stemmer cannot supply a slang
synonym; glossary expansion adds explicit semantic knowledge.

Changing only document vectors and retaining base BM25 yields hybrid change
+0.054 [+0.011, +0.114] on authored paraphrases and
+0.025 [+0.000, +0.075] on rig paraphrases. The unchanged lexical
leg can erase much of the vector gain. A combined vector+lexical expansion is a
specific **next rig experiment**, not a confirmed result here; it was not selected
or scored post hoc on these held-out cases.

## Public non-regression

These comparisons are against the existing **exact head projection**, on full
FiQA (57,638 docs), the wiki stress set, SciFact, MIRACL RU/DE closed pools, and
Russian wiki. They are not independent replications of the earlier head study.
All frozen document-rewriting and projection variants were rechecked; the full
matrix, including random controls and replacement/transliteration variants, is
in `results/regression.json` and [TABLES.md](TABLES.md).

| Set | Δ nDCG: language projection | Δ nDCG: slang projection | Δ nDCG: additive glossary | Glossary-triggered docs |
|---|---|---|---|---:|
| fiqa-full | -0.0003 [-0.0013, +0.0007] | +0.0006 [-0.0014, +0.0025] | +0.0000 [+0.0000, +0.0000] | 0 |
| wiki-mix | -0.0011 [-0.0048, +0.0005] | -0.0002 [-0.0022, +0.0018] | +0.0000 [+0.0000, +0.0000] | 5 |
| scifact | +0.0000 [+0.0000, +0.0000] | +0.0009 [-0.0015, +0.0042] | +0.0000 [+0.0000, +0.0000] | 0 |
| miracl-ru-pool | +0.0003 [+0.0000, +0.0008] | +0.0015 [+0.0001, +0.0034] | +0.0002 [-0.0001, +0.0006] | 22 |
| miracl-de-pool | -0.0001 [-0.0009, +0.0003] | -0.0002 [-0.0016, +0.0008] | +0.0000 [+0.0000, +0.0000] | 0 |
| ru | +0.0100 [+0.0000, +0.0278] | +0.0000 [+0.0000, +0.0000] | -0.0027 [-0.0082, +0.0000] | 5 |

Russian lexical changes were also checked with the same fixed head vectors:

| Set | Δ hybrid nDCG: stemming | Δ hybrid nDCG: glossary lexical |
|---|---|---|
| fiqa-full | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] |
| wiki-mix | +0.0107 [+0.0000, +0.0245] | +0.0107 [+0.0000, +0.0245] |
| scifact | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] |
| miracl-ru-pool | +0.0239 [-0.0078, +0.0565] | +0.0239 [-0.0078, +0.0565] |
| miracl-de-pool | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] |
| ru | +0.0127 [-0.0114, +0.0439] | +0.0127 [-0.0114, +0.0439] |

A zero metric difference can reflect a true no-op or changed vectors with unchanged
retrieval metrics; trigger counts and the lexical no-op proofs distinguish these
cases. It is not statistical proof on unseen tasks. Nonzero public losses are not hidden by
an aggregate. Russian dictionary ambiguity (for example `рейс` as a flight,
`мок...` as an ordinary adjective) and expansion length are reasons to scope this
prototype to technical content and validate coverage before a general rollout.
A zero regression on existing benchmarks is not a guarantee for all workspace
notes. No global “remove another Russian direction” patch is recommended.

## Exact change and handoff

Use the original text followed by this suffix, only when a frozen glossary entry
matches a token boundary:

```
<original text> | Значение терминов: <Russian meaning> (<English source>); ...
```

The full 40-entry mapping and boundary/ambiguity rules are in
`/Users/roman/anytype/any-wt-syn391/scripts/embedbench/research/eg2_ru/glossary.json` and `expand.py`.
Duplicate meanings are appended once, in glossary order. The original text is
kept for display and citations. Only the embedding input changes; the query
prefix remains `task: search result | query: ` and document prefix remains
`title: none | text: `. Keep mean pooling, context 2048, the existing head patch,
and exact search. Re-embed changed documents and rebuild the affected vector
index. No weight training or query-time LLM is required.

The additional diagnostic projection, if reproduced, is
`normalize((I−alpha*l*lᵀ)(I−u*uᵀ)v)`. `u` is the existing head vector;
`dev/directions.npz` contains `language`, `slang` and matched random directions.
`FROZEN.json` fixes doses .25/.5. The equivalent pre-quantization raw-head edit is
`W_new=(I−alpha*l*lᵀ)(I−u*uᵀ)W`. Random controls match the exact-space operator's
Frobenius edit norm, not the quantized weight norm. These are **research controls**,
not new recommended GGUF artifacts.

For the teammate's rig:

1. Preserve the original 30 Russian known-item queries and eight look-alikes as
   confirmation, with all existing controls. Freeze this dictionary/version and
   search settings before running. Do not add missing s01/s06 terms from these
   examples, and do not optimize against their outcomes.
2. Run Qwen, EG2-head, and EG2-head with `expand.py` document inputs against the
   **same full workspace pool**, with exact search. Evaluate keyword, Russian
   paraphrase and both cross-language directions separately. Preserve the
   original chunks and all IDs; exclude glossary suffixes from displayed text.
3. First keep lexical retrieval unchanged. Then run separately preregistered
   lexical-only and combined vector+lexical glossary arms. Treat Russian stemming
   as an ablation, given its rig regression here. Use RRF k=60/equal weights and
   ≥30 per leg as specified; log vector, BM25 and fused gold ranks separately.
4. Report MRR@10/nDCG@10/Recall@10 and paired passage-cluster CIs, plus expansion
   coverage, s08-like regressions, truncation, and useful short answers. Retest
   the English/topical wins. Promotion requires a resolved **hybrid** improvement
   without unacceptable regressions. Only after that confirmation should a new
   independently evaluated glossary version be expanded for coverage.

Portable command (stdlib-only):

```sh
python3 scripts/embedbench/research/eg2_ru/expand.py \
  < bench/eg2-ru/from-rig/synthetic-lookalikes.jsonl \
  > bench/eg2-ru/expanded-rig.jsonl
```

Embed the emitted `embedding_text`; keep `text` unchanged. The script is intended
for an authorized corpus export on the rig, not for reading local account data.

## Provenance, protocol and limits

- Streamed the first 6,000 threads of the public
  [IlyaGusev/ru_stackoverflow repack](https://huggingface.co/datasets/IlyaGusev/ru_stackoverflow),
  then hash-selected 824 passages: 800 background passages and 24 prospective
  targets. Three targets were excluded before scoring as context-poor/boilerplate;
  21 received manually authored keyword/Russian/English queries (11 dev, 10 test).
  Public comments are public Q&A material, not scraped private chats. Original
  URL, author, timestamp and licensing attribution are stored with each record.
- The repack says CC BY-SA 2.5, but
  [Stack Overflow's policy](https://stackoverflow.com/help/licensing) assigns
  2.5/3.0/4.0 by contribution date. We retained per-creation-date versions and the
  caveat that later revisions can have a newer license. Normalization removes
  HTML tags/unescapes entities and collapses whitespace. No full archive was kept.
- Forty independently invented scenarios, 20 dev/20 test, include chat/notes,
  English tech tails and English translations. Each split has disjoint primary term
  families and three queries per target. All query texts were authored before
  scoring. The static glossary was built alongside all 40 family definitions;
  **its authored test result measures dictionary-covered new contexts, not
  blind acquisition/generalization to unseen words**. The same author/resource
  supplies those meanings and paraphrases, so that intervention can be optimistic.
  The rig's separately written, sealed examples are the stronger independent
  check, and their aggregate CI is unresolved.
- Background passages are shared between dev/test; targets and queries are
  disjoint. Relevance is single-target known-item relevance, not exhaustive
  semantic judgments. Source-related Q&A passages may offer alternate answers.
  Corpus size, artificial hubs, synthetic redundancy, old source distribution
  and 20/10/8 target counts limit extrapolation.
- Length, formality and Latin-share slices are all saved in `test.json`; their
  small, confounded subgroups should not be treated as causal coefficients.
  Cross-language failure is not uniformly reproduced; it depends on the pool.
- Fifty main dev passes were used, then directions, doses, glossary, data and
  code hashes were frozen. Rig text was opened afterward. Candidate/held-out
  combinations were scored once; all later CIs/tables use saved arrays.
  A JSONL parser correctness repair during regression preparation is recorded
  in `CORRECTIONS.json`: Unicode separators inside valid JSON strings must not
  be treated as record boundaries. It preceded regression scoring and changed
  no candidates or inputs; the original freeze was retained. A second repair
  keeps this experiment ahead of the shared loader directory on Python's import
  path; otherwise native summary code imported the old identically named
  evaluator. Native validation was rerun after that correction; held-out retrieval
  was not repeated.
- Public regression vectors were reused only through a reconstructed public
  prefix of the earlier bank; its mixed text bank and private vector rows were
  never read. Re-encoding 12 public
  sample records validates the prefix mapping at minimum cosine
  0.99999988.
- Native b11600 end-to-end checks, using explicit synthetic corpus input and
  pipe IPC, give minimum Python/native cosine: stock
  0.99999952, patched Q8
  0.99999899, Qwen 0.99999666.
  Exact projection versus the quantized head has minimum cosine
  0.99994808; ranking tables
  use the exact projection, as on the supplied synthetic rig check.
- New data occupy about 100 MiB at audit, well below
  5 GB. One heavy job at a time. All authored files stay in the two permitted
  directories; no commits, pushes, branch changes, account servers or local
  network ports were used. See [PROGRESS.md](PROGRESS.md), [AUDIT.json](AUDIT.json),
  and the scripts' README for reproduction.
