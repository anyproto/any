# Round 2: no tested cheap fix meets the acceptance bar

**Acceptance: FAILED. Recommendation: retain EG2-head and exact search. Do not ship the round-2 dictionary/lexical expansion or token alias patch.** This experiment tests a scalable public-source mapping and a no-training weight edit; it does not establish that every cheap fix is impossible.

The preregistered combined arm C changes sealed paraphrase MRR@10 by **+0.010 [-0.018, +0.043] in vector search** and **+0.032 [+0.003, +0.073] after fusion** (64 targets). Acceptance requires resolved gains in both, independent confirmation, and non-regression. `results/acceptance.json` records each gate.

## Independent confirmation

The **20 newly supplied, independently authored rig pattern cases** give C−H **-0.017 [-0.105, +0.043] vector** and **+0.015 [-0.092, +0.135] hybrid**. These texts were first opened after FROZEN.json. The 24 new cases (20 patterns + 4 controls) are reported separately from the original eight; the combined 24-pattern slice includes four original cases and is not 24 entirely new cases.

| Arm | Vector MRR | BM25 MRR | Hybrid MRR | Δ hybrid vs H |
|---|---:|---:|---:|---|
| H | 0.604 | 0.440 | 0.640 | +0.000 [+0.000, +0.000] |
| Q | 0.900 | 0.440 | 0.805 | +0.165 [+0.037, +0.317] |
| V | 0.587 | 0.440 | 0.675 | +0.035 [-0.015, +0.125] |
| L | 0.604 | 0.409 | 0.621 | -0.019 [-0.095, +0.056] |
| C | 0.587 | 0.409 | 0.655 | +0.015 [-0.092, +0.135] |
| S | 0.604 | 0.360 | 0.525 | -0.115 [-0.245, -0.003] |
| W0.25 | 0.604 | 0.440 | 0.640 | +0.000 [+0.000, +0.000] |
| R0.25 | 0.604 | 0.440 | 0.640 | +0.000 [+0.000, +0.000] |

The external post reports EG2-head/Qwen MRR 0.476/0.825 in its private 1,458-document pool. Our same 20 targets give 0.604/0.900 in the 832-document public pool. This difference in distractors limits transfer. No private chunk, private vector row or local service was accessed; external absolute ranks are provenance, not our reproduced measurements.

## What was fixed in evaluation

The new 64-target set was authored and sealed before source acquisition, with 48/64 primary families outside round 1’s 40-entry glossary. It contains 64 topical competitors, three queries per target (plain Russian, keyword, specific), 12 ordinary Russian controls and 12 English controls, plus the existing 800 attributed public distractors: 952 documents, 216 queries. The primary paraphrase sample is 64 target clusters, not 192 independent observations.

The authored set has **temporal/source separation, not independent authorship**: this agent wrote the cases and executed the research. Unlike round 1, no dictionary definition came from an authored query; whole published categories supplied definitions mechanically. The fresh rig batch supplies the independent-author check. Known-item relevance is single-target, not exhaustive semantic judgment. Some English controls describe similar facts, so the prespecified `sealed-ru` sensitivity removes those 12 English documents/queries; it never replaces the primary.

On sealed-ru, C−H is +0.010 [-0.018, +0.043] vector and +0.029 [+0.000, +0.070] hybrid. No text/label or parameter was changed after the seal/freeze.

All differences use 10,000 paired bootstrap draws, seed 391. Queries sharing a relevant target are joined into connected components and resampled together; this also applies to public multi-relevance sets. Intervals are pointwise; acceptance requires all prespecified gates, rather than selecting the best arm. A nonsignificant loss is not proof of no regression. Material-loss margins were .01 nDCG on public sets and .02 MRR on controls.

## Source mapping and honest coverage

Whole Russian Wiktionary computer-slang/internet/game and programming categories supplied 1,320 revision snapshots. Extraction yielded **914 entries**, **337 Latin-script etymon candidates**, and **300 active plausible loanword mappings**. Published source text, permanent revisions, contributor-history links, extraction rules and CC BY-SA 4.0 notices are in `sources/`; no missing held-out/rig term was filled manually.

On the sealed set, exact family-name lookup finds 13/64 families in the extracted dictionary and 8/64 in the active mapping. Actual vector expansion fires on 5/64 target messages. **A trigger may be incidental vocabulary, not successful coverage of the primary slang family.** `sealed-coverage-detail.jsonl` lists every matched lemma/meaning/source and every missed case; the covered/uncovered slices in TABLES use any document trigger and are labeled accordingly.

Exact-family lookup is conservative about derivatives and spelling variants (for example a family label and its verb may differ); raw entry count is not a claim of workspace coverage. Source senses include older desktop/internet language and can omit modern dev-team verbs. The lemma rules handle regular noun cases and common verb endings but do not perform general Russian morphological analysis. Fifty exact family labels are absent from the acquired category pages, one has no eligible extracted sense, five fail etymon/similarity activation, and eight are active before context gates. Improvements on an uncovered target can arise from changed competitors or lexical statistics; they do not establish acquisition of its missing slang meaning.

Active rows use an extracted etymon candidate and transliteration/source similarity ≥.45; source-language verification proved incomplete (see below). Short Cyrillic documents only (≤600 characters) can receive at most two definitions, each at most 18 words, with a 240-character suffix budget. Ambiguous dictionary senses require an independent technical cue. Untested senses/ambiguous flight `рейс` and wet `мок` are not filled by transliteration guesses. These conservative gates trade recall for precision and still do not resolve all sense ambiguity.

The lexical arm preserves original NFKC/casefold/UAX#29 tokens and adds missing canonical slang, English and definition tokens to both sides. Reverse definition matching excludes common definition words and needs at least two anchors and half the remaining content words (one for a true one-word meaning). Pure English inputs are unchanged by lexical expansion. Full Snowball stemming/ё folding exists only in S. This is source-based synonym expansion, not trained retrieval.

## Arms and held-out retrieval

| Arm | Change |
|---|---|
| H | EG2-head, base BM25 |
| Q | Qwen3, base BM25 |
| V | document vector glossary, base BM25 |
| L | H vectors, bidirectional lexical glossary |
| C | V + L; preregistered primary |
| S | H vectors, Snowball lexical ablation |
| W0.25 | actual sparse token-row aliases, head, base BM25 |
| R0.25 | per-row norm/displacement matched random edits |

| Set / slice | H vector | Q vector | C vector | Δ C−H vector | H hybrid | C hybrid | Δ C−H hybrid |
|---|---:|---:|---:|---|---:|---:|---|
| sealed/para (n=64) | 0.268 | 0.331 | 0.278 | +0.010 [-0.018, +0.043] | 0.214 | 0.246 | +0.032 [+0.003, +0.073] |
| sealed/outside_round1 (n=48) | 0.298 | 0.365 | 0.311 | +0.013 [-0.025, +0.057] | 0.230 | 0.273 | +0.043 [+0.004, +0.097] |
| sealed/covered (n=5) | 0.560 | 0.667 | 0.700 | +0.140 [-0.220, +0.540] | 0.625 | 0.900 | +0.275 [+0.000, +0.675] |
| sealed/uncovered (n=59) | 0.243 | 0.303 | 0.242 | -0.001 [-0.006, +0.001] | 0.180 | 0.191 | +0.011 [-0.001, +0.032] |
| round1/synthetic/para (n=20) | 0.521 | 0.731 | 0.549 | +0.028 [-0.005, +0.080] | 0.340 | 0.338 | -0.003 [-0.019, +0.016] |
| round1/public/para (n=10) | 0.770 | 0.620 | 0.767 | -0.003 [-0.010, +0.000] | 0.498 | 0.500 | +0.002 [+0.000, +0.007] |
| rig/para (n=8) | 0.625 | 0.750 | 0.688 | +0.062 [+0.000, +0.188] | 0.688 | 0.604 | -0.083 [-0.250, +0.000] |

TABLES.md contains every arm’s vector, BM25 and fused ranks/metrics separately, including controls and external pattern slices. The full per-query arrays retain ranks/top-10 IDs and MRR/nDCG/Recall. No vector-to-hybrid contrast is presented as an intervention gain. Round-1 EG2/Qwen vector ranks and metrics reproduce exactly; three prior hybrid queries change under the preregistered stable document-ID tie order (round1-hybrid-tie-differences.json).

## Weight-space attempt

Edited **18 actual token_embd rows** from independently sourced unambiguous loanword forms. English source token rows are averaged and norm-matched, then interpolated at .25/.5/1 and renormalized to the original row norm. Random edits preserve each final norm and match each row’s displacement. WEIGHT_TABLES.md reports all dev doses against their random controls, including ordinary and English retrieval. `alias_norms.json` records numerical checks; `alias_rows.npz` is the compact reproducible patch.

The faithful graph computes both the residual input and the per-layer embedding (PLE) projection from the same `token_embd` lookup. Therefore the sparse edit changes both paths consistently; no separate PLE lookup table is left stale. Final audit measures the projected PLE difference. This is an inference-time raw weight edit, not a post-hoc vector transform, tokenizer text replacement or training step. No requantized alias GGUF was produced; these negative tests use edited dequantized Q8 rows in the validated MLX graph, not a new native deployment artifact.

| Dose | Dev slang Δ vector | Dev slang Δ hybrid | Ordinary Δ vector | Ordinary Δ hybrid |
|---|---|---|---|
| 0.25 | +0.000 [+0.000, +0.000] | +0.000 [+0.000, +0.000] | +0.000 [+0.000, +0.000] | +0.000 [+0.000, +0.000] |
| 0.5 | -0.007 [-0.020, +0.000] | -0.008 [-0.025, +0.000] | +0.000 [+0.000, +0.000] | +0.000 [+0.000, +0.000] |
| 1.0 | -0.012 [-0.035, +0.000] | -0.008 [-0.025, +0.000] | -0.050 [-0.150, +0.000] | -0.075 [-0.208, +0.000] |

No positive eligible dev dose existed. W0.25 is frozen as a **rejected diagnostic**, not a selected improvement. The higher dose exposes collateral ordinary-Russian damage. Whole-word tokens such as inflections of `ник` and `комп` can still occur inside ordinary words; dedicated probes and public sets test that sharing.

The headline slang often splits into generic pieces: хотфикс → ▁хо / т / фи / кс; хендлер → ▁х / ен / д / лер; задеплоить → ▁за / де / пло / ить; ревью → ▁ре / вью. Changing those common pieces globally would not implement a selective alias. The current constrained rows do not cover these key bridges. This rejects this cheap alias design, not all possible multilingual weight editing.

## Non-regression and dilution risks

The combined arm also fails the preregistered public margin on RU wiki: vector nDCG falls 0.0131 (CI −0.0394 to 0), and hybrid falls 0.0081 (CI −0.0242 to 0). Thus aggregate public safety is not established even though FiQA, SciFact and German MIRACL are unchanged. Specificity controls also lose 0.036 vector MRR, exceeding their .02 material-loss margin.

Independent s20 is a useful success: a source definition of линтер moves vector rank 52→4 and fused rank 6→1. But s15 goes vector 1→4 after a generic коммит definition dilutes the specific forced-push/rollback intent. s12 has no document expansion yet goes fused 2→10 under C as reverse lexical expansion adds a noisy table definition. In the original eight-case pool, s08 stays vector rank 1, while s05 deteriorates 54→372 under V. All cases, including these losses, remain in the aggregate. See failure-case-expansions.json and RIG_RANKS.md.

| Public set | Queries | Δ C vector nDCG | Δ C hybrid nDCG | Δ W vector nDCG | Δ W hybrid nDCG |
|---|---:|---|---|---|---|
| fiqa-full | 648 | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] |
| wiki-mix | 65 | +0.0000 [+0.0000, +0.0000] | -0.0003 [-0.0023, +0.0015] | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] |
| scifact | 300 | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] |
| miracl-ru-pool | 200 | +0.0008 [+0.0000, +0.0022] | -0.0000 [-0.0002, +0.0001] | -0.0006 [-0.0017, +0.0000] | +0.0000 [+0.0000, +0.0000] |
| miracl-de-pool | 200 | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] |
| ru | 48 | -0.0131 [-0.0394, +0.0000] | -0.0081 [-0.0242, +0.0000] | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] |

FiQA uses all 57,638 documents and 648 test queries. SciFact uses all 5,183 documents and 300 test queries. MIRACL RU/DE remain the prior 200-query **closed pools**, not full MIRACL retrieval: passage IDs in the repack cannot be independently recovered. Wiki stress and RU wiki are the prior public sets. Existing models/banks are reused only via the reconstructed public prefix and verified by fresh EG2/Qwen encodings.

| Sealed controls | Δ C vector MRR | Δ C hybrid MRR |
|---|---|---|
| kw | +0.000 [+0.000, +0.000] | +0.000 [+0.000, +0.000] |
| specific | -0.036 [-0.083, +0.000] | -0.015 [-0.040, +0.004] |
| ordinary | +0.000 [+0.000, +0.000] | -0.007 [-0.021, +0.000] |
| english | +0.000 [+0.000, +0.000] | +0.000 [+0.000, +0.000] |
| risk | +0.001 [-0.002, +0.005] | -0.002 [-0.006, +0.000] |

`results/rank1-to-below3.jsonl` records all 25 arm/leg/query rank-1→>3 events, including duplicate observations across sensitivity pools; these are not 25 independent failures. `RIG_RANKS.md` records every external case in the current public pool. Even a scoped glossary can dilute specific intent or shift BM25 length/IDF and RRF ordering. Exact no-op inputs and unchanged metrics are distinguished by per-set coverage counts.

## Exact recommendation and reproducible rig protocol

Keep the existing production EG2 Q8 + exact head projection. Do not alter tokenizer rows, enable this glossary, or deploy a stemmer on the evidence here. The delivered scripts/data are an auditable rejected-candidate experiment, not a production patch. No retraining or model download is required to reproduce it.

If the teammate runs the frozen candidate for diagnostic replication:

1. Hash the original workspace pool/query/qrels export and all round-2 frozen files before scoring. Keep the independent 20 new patterns and four controls separate from the old eight and from the original 30 Russian queries. Never add uncovered terms after seeing outcomes.
2. Fix corpus/chunks/IDs, context 2048, EG2 production prefixes, mean pooling, head projection and exact search. Keep Qwen’s production query instruction and last-token pooling. Do not apply the head twice. Use the original text for display/citations.
3. Run in fixed order H, Q, V, L, C, S, W0.25, R0.25, one heavy process at a time. V/C use `pipeline2.vector_text`; L/C use `expanded_tokens` on docs and queries; S uses `stem_tokens`. W/R load the sparse rows via `alias2.apply` in the faithful MLX model. Verify model hashes first. Re-embed/reindex each changed document; never mix incompatible banks.
4. First use the estimator settings exactly: BM25 k1=1.2, b=.75, original-token OR semantics, positive lexical hits, equal RRF k=60, 30 hits/leg. Record the production-index result separately if titles, adaptive weights, filters or operators are enabled. A proxy hybrid gain does not certify production fusion.
5. Save each query’s target rank in vector, BM25 and fusion, top-10 IDs/scores, coverage/matched source entries, truncation and elapsed time. Report MRR/nDCG/Recall@10, paired target-cluster 95% bootstrap CIs, keyword/paraphrase/control/language slices, and every rank-1→>3 loss. Do not discard uncovered terms or suppress losses.
6. Promotion still requires positive lower CI bounds for the preregistered primary vector and hybrid deltas on genuinely independent queries, and non-inferiority at the frozen .01/.02 margins. Keep the original 30-query workspace Russian result as its own endpoint. Failure here is not repaired by choosing the best retrospective arm.

## Provenance and limits

SEALED.json preceded source acquisition; FROZEN.json preceded all held-out scoring and opening raw rig batches. One dev-only source/rule revision is documented in AMENDMENTS.md with the first results preserved. A prescore verb-inflection/MLX-indexing repair was also preserved. No held-out tuning occurred. All result tables derive from saved ranks; reporting never re-encodes or reranks.

The fresh rig data makes independent testing possible, but these synthetic, single-target pools do not reproduce the private workspace. A post-score source audit found 35/337 etymon candidates came from unresolved generic templates (33 active), including the false English assignment таблица → tabula. The extractor and scores are preserved unchanged; SOURCE_LIMITATIONS.md explains why this defect further prevents a production recommendation or a universal claim that no cheap fix exists. Dictionary senses/English etymons are automatically extracted and not exhaustively manually vetted. Conservative gates leave many phrases unchanged; reverse lexical matching can still be semantically noisy. The non-regression battery does not cover every ordinary Russian word sharing a subtoken.

All writes stay in the two round-2 directories; no git state changes, account reads or local ports. AUDIT.json verifies round-1/model/frozen hashes, cache parity and the <5 GB limit. The artifact is research-only and carries source attribution in sources/ATTRIBUTION.md.
