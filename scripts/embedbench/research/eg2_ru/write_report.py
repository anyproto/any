"""Assemble the interpretation from immutable results; never embeds or ranks."""
from util import *
from tables import fmt

def run():
 d=json.loads((BASE/'results/derived.json').read_text());test=json.loads((BASE/'results/test.json').read_text());dev=json.loads((BASE/'results/dev.json').read_text());diag=json.loads((BASE/'results/test-diagnostics.json').read_text());reg=json.loads((BASE/'results/regression.json').read_text());lexreg=json.loads((BASE/'results/lexical-regression.json').read_text());trig=json.loads((BASE/'results/regression-triggers.json').read_text());parity=json.loads((BASE/'validation/parity.json').read_text());audit=json.loads((BASE/'AUDIT.json').read_text())
 syn=d['synthetic/para'];rig=d['rig/para'];nh=d['nohubs']['synthetic/para'];pub=d['public/para']
 text=f'''# Why EG2 loses on informal Russian, and what helps

**Keep the head patch and exact search. The strongest additional candidate is a
fixed document-side glossary that preserves the slang and appends its Russian
meaning plus English source term. Neither tested extra projection fixes the
Russian paraphrase deficit. This study establishes a controlled vector-retrieval
improvement, not a confirmed fix for the rig's full hybrid search.**

The evidence points to a weak **contextual bridge between Cyrillic developer slang
and a plain-Russian description**, amplified by short, low-redundancy messages.
It does **not** establish that EG2 simply lacks each word's definition, that Russian
is generally weak, or that a universal language direction causes the loss.

On 20 held-out authored slang scenarios, EG2-head MRR@10 is {syn['head']['mrr']:.3f}
versus Qwen {syn['qwen']['mrr']:.3f}: head−Qwen **{fmt(syn['head']['vs_qwen'])}**.
Additive glossary expansion reaches {syn['head-gloss']['mrr']:.3f}, an improvement of
**{fmt(syn['head-gloss']['vs_head'])}**; its residual difference from Qwen is
{fmt(syn['head-gloss']['vs_qwen'])}. This is not an equivalence test.
Without authored topical distractors, its gain is still
{fmt(nh['gloss_vs_head'])}, and head−Qwen remains {fmt(nh['head_vs_qwen'])}.

The glossary increases correct query–passage cosine by
{fmt(test['synthetic/para']['differences']['head-gloss']['head']['gold_score'])}
and its margin over the best wrong passage by
{fmt(test['synthetic/para']['differences']['head-gloss']['head']['margin'])}.
These are within-EG2 contrasts, avoiding comparisons of uncalibrated raw cosine
scales between models.

All brackets below are paired 95% bootstrap CIs, 4,000 resamples, seed 391.
Queries sharing a target are resampled together. Intervals are pointwise; the
small, selected samples do not estimate workspace-wide prevalence. Full metrics,
slices, ranks and comparisons are in [TABLES.md](TABLES.md) and `results/`.

## Evidence, strongest first

1. **Changing the semantic bridge helps; changing the alphabet alone does not.**
   On held-out Russian paraphrases, replacing Cyrillic slang with its actual
   English source gives {fmt(syn['head-latin']['vs_head'])}; replacing it with
   the Russian meaning gives {fmt(syn['head-plain']['vs_head'])}; mechanical
   Latin transliteration gives {fmt(syn['head-translit']['vs_head'])}.
   Additive expansion is the dev-selected primary candidate because it retains
   keyword evidence. Replacement loses keyword MRR on the synthetic stress set:
   {fmt(d['synthetic/kw']['head-plain']['vs_head'])}. The additive candidate's
   keyword change is {fmt(d['synthetic/kw']['head-gloss']['vs_head'])}; on the rig
   keyword queries it remains 1.000, difference [0.000, 0.000].

2. **Independently written, sealed rig examples support the mechanism, but do not
   resolve the aggregate effect.** The eight texts were opened only after
   `FROZEN.json`; no dictionary entries or parameters were added afterward.
   In our 808-document public pool, s04 (hotfix/master/review) moves from head rank
   14 to 1, and s05 (handler/flaky/CI) from 54 to 1. s07 moves 2 to 1. However,
   s08 moves 1 to 5: appending a generic endpoint explanation can dilute the
   specific synchronization/batch-update intent. s01's slang is uncovered and
   already ranks first in this easier pool. Six of eight texts trigger expansion.
   Rig paraphrase head MRR is {rig['head']['mrr']:.3f}, Qwen {rig['qwen']['mrr']:.3f},
   glossary {rig['head-gloss']['mrr']:.3f}; glossary−head is
   **{fmt(rig['head-gloss']['vs_head'])}**. The supplied rig's 1,434 private
   distractors were never read and are not reproduced here. Absolute ranks from
   those two pools must not be combined.

3. **Public technical Russian does not automatically reproduce the deficit.**
   On ten held-out public StackOverflow paraphrase targets, head MRR is
   {pub['head']['mrr']:.3f}, Qwen {pub['qwen']['mrr']:.3f}, head−Qwen
   {fmt(pub['head']['vs_qwen'])}. The supplied RuBQ/wiki observations are
   consistent with a domain/register-specific problem; RuBQ was not rerun.

4. **The isolated-word check weakens the simplistic “Qwen knows the words, EG2
   does not” account.** In 20-way held-out meaning retrieval, head reciprocal rank
   is {diag['lexical']['head']['meaning_from_loan_mrr']:.3f} and Qwen
   {diag['lexical']['qwen']['meaning_from_loan_mrr']:.3f}; difference
   {fmt(diag['lexical_differences']['head']['qwen']['meaning_from_loan_mrr'])}.
   Using the actual English source instead of the Cyrillic loanword improves
   head by {fmt(diag['lexical_source_minus_loan']['head'])} and Qwen by
   {fmt(diag['lexical_source_minus_loan']['qwen'])}. Both have lexical weak spots;
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
{fmt(syn['language']['vs_head'])}, and slang removal by
{fmt(syn['slang']['vs_head'])}. Against their matched random controls the changes
are {fmt(d['synthetic/para']['language_vs_random'])} and
{fmt(d['synthetic/para']['slang_vs_random'])}, respectively. This is far short of
the observed Qwen gap. No writer-side edit was attempted: the slang contrast did
not reveal a clean, consistent component that justified changing a writer tensor.
This rules out these particular edits, not all possible weight-space surgery.

Language removal does align matched RU/EN statement vectors: paired cosine
change {fmt(diag['lexical_differences']['language']['head']['sentence_ru_en'])}.
Its EN-query→RU-document MRR change is
{fmt(d['synthetic/cross']['language']['vs_head'])}; RU-query→EN-document change is
{fmt(diag['reverse_differences']['language']['head']['mrr10'])}. Better language
alignment therefore does not explain or repair the monolingual slang failure.
Query centering changes held-out slang paraphrase MRR by
{fmt(syn['centered']['vs_head'])}; it is not the missing fix either.

Prompt diagnostics were dev-only. A Russian instruction changes head MRR by
{fmt(dev['synthetic/para']['differences']['prompt-ru']['head']['mrr10'])}; an English
“same meaning” instruction by
{fmt(dev['synthetic/para']['differences']['prompt-paraphrase']['head']['mrr10'])};
bare queries by {fmt(dev['synthetic/para']['differences']['prompt-bare']['head']['mrr10'])}.
No prompt was selected. Production prefixes and pooling remain unchanged.

Removing artificial short topical non-answers changes held-out head paraphrase
MRR by {fmt(d['hubs']['para']['head']['removal_mrr'])}. The gap persists, so the
previous short-hub mechanism is insufficient. The authored hubs do intercept
keyword queries for both models, which is why their removal is reported
separately; the sensitivity restores head/Qwen keyword MRR to
{d['nohubs']['synthetic/kw']['head']:.3f}/{d['nohubs']['synthetic/kw']['qwen']:.3f}.
This does not imply that no real chat message can be a hub.

Seven held-out mixed-script examples contain the same generic English debug tail.
The full message's cosine to its meaningful English translation minus its cosine
to that tail is {fmt(diag['mixed_meaning_minus_tail']['head'])} for head and
{fmt(diag['mixed_meaning_minus_tail']['qwen'])} for Qwen. Both are susceptible;
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
'''
 for label,name in [('Authored slang (20)','synthetic/para'),('Public Q&A (10)','public/para'),('Sealed rig (8)','rig/para')]:
  r=d[name];text+=f"| {label} | {r['head-hybrid-base']['mrr']:.3f} | {r['head-hybrid-stem']['mrr']:.3f} | {fmt(r['head-hybrid-stem']['vs_head'])} | {r['head-hybrid-gloss']['mrr']:.3f} | {fmt(r['head-hybrid-gloss']['vs_head'])} |\n"
 text+=f'''
Stemming helps the authored stand-in but harms the sealed rig examples. It is not
a production recommendation from this study. A stemmer cannot supply a slang
synonym; glossary expansion adds explicit semantic knowledge.

Changing only document vectors and retaining base BM25 yields hybrid change
{fmt(syn['head-gloss-hybrid']['vs_head'])} on authored paraphrases and
{fmt(rig['head-gloss-hybrid']['vs_head'])} on rig paraphrases. The unchanged lexical
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
'''
 for name,r in reg.items():text+=f"| {name} | {fmt(r['differences']['language']['ndcg10'],4)} | {fmt(r['differences']['slang']['ndcg10'],4)} | {fmt(r['differences']['gloss']['ndcg10'],4)} | {trig[name]['gloss']} |\n"
 text+='\nRussian lexical changes were also checked with the same fixed head vectors:\n\n| Set | Δ hybrid nDCG: stemming | Δ hybrid nDCG: glossary lexical |\n|---|---|---|\n'
 for name,r in lexreg.items():text+=f"| {name} | {fmt(r['differences']['stem']['ndcg10'],4)} | {fmt(r['differences']['gloss']['ndcg10'],4)} |\n"
 text+=f'''
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
`{str(ROOT/'scripts/embedbench/research/eg2_ru/glossary.json')}` and `expand.py`.
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
python3 scripts/embedbench/research/eg2_ru/expand.py \\
  < bench/eg2-ru/from-rig/synthetic-lookalikes.jsonl \\
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
  never read. Re-encoding {audit['public_prefix_cache_parity']['sample_rows']} public
  sample records validates the prefix mapping at minimum cosine
  {audit['public_prefix_cache_parity']['min_cosine']:.8f}.
- Native b11600 end-to-end checks, using explicit synthetic corpus input and
  pipe IPC, give minimum Python/native cosine: stock
  {parity['stock']['min_cosine']:.8f}, patched Q8
  {parity['head']['min_cosine']:.8f}, Qwen {parity['qwen']['min_cosine']:.8f}.
  Exact projection versus the quantized head has minimum cosine
  {parity['exact_projection_vs_quantized_head']['min_cosine']:.8f}; ranking tables
  use the exact projection, as on the supplied synthetic rig check.
- New data occupy about {audit['new_data_bytes']/2**20:.0f} MiB at audit, well below
  5 GB. One heavy job at a time. All authored files stay in the two permitted
  directories; no commits, pushes, branch changes, account servers or local
  network ports were used. See [PROGRESS.md](PROGRESS.md), [AUDIT.json](AUDIT.json),
  and the scripts' README for reproduction.
'''
 (BASE/'REPORT.md').write_text(text);log('Wrote final report with held-out CIs, independent rig limitations, public regressions, exact pipeline and next rig confirmation instructions.')
if __name__=='__main__':run()
