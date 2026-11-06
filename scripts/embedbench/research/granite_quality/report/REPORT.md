# Granite Embedding 311M multilingual R2: public-data diagnosis

2026-11-06. Diagnosis only; no model weights, production code, branches, or services changed.

**The strongest explanation is a mismatch between topical similarity and answer-bearing relevance in short fragments.** Granite puts topic-bearing headings and discussion pings close enough to substantive answers to displace them. The effect is reproduced on public data and survives a language-restricted retrieval control. Its strongly shared CLS direction is measurable, but generic de-anisotropization is not a successful remedy. Token fragmentation and simple language offsets do not explain the Russian/German symptoms in these tests. The wiki benchmark's German result is substantially confounded by language-specific relevance labels.

There is **no validated universal weight-only fix** here. The practical handoff is to test a small, constrained readout adjustment and, more importantly, train against topic-matched non-answer negatives while preserving useful short answers. Exact tensors and falsification tests are in **Patch candidates** below.

## Evidence and scope

Embedding experiments used only public Wikipedia, BEIR FiQA, FLORES-200, public model weights, and explicitly synthetic templates derived from public headings. No account content or local ports were accessed. A late rig export was used only through its text-free rank/hit metadata files; its query, gold-passage and pooled-text files were not opened. Consequently, this report diagnoses reproducible public mechanisms and checks a limited rank-only export; it does not establish the complete cause of the nine real spaces' measured deficit. The supplied nine-space numbers remain prior evidence, not independently replicated observations.

All differences below are paired bootstrap estimates with 95% intervals. Scores are exact-search nDCG@10 unless specified; MRR means **MRR@10**, zero beyond rank 10. Intervals use 4,000 resamples, grouped by target for paired Russian/English queries and by source article for FLORES. Wiki article-cluster intervals supplement query intervals. Nonlinear geometry differences use 500 paired resamples. Exploratory sweep intervals are pointwise, without multiple-testing correction.

The complete evidence is in [TABLES.md](TABLES.md), [results.json](results.json), [geometry.json](geometry.json), [mechanism.json](mechanism.json), [probes.json](probes.json), and [language_control.json](language_control.json). [public_top_hits.jsonl](public_top_hits.jsonl) contains inspectable public examples. [data_hashes.json](data_hashes.json) identifies the dataset snapshot.

## Findings ranked by strength of evidence

### 1. Short topical non-answers reproduce the failure; shortness alone does not

The rebuilt wiki set contains 1,199 windows, including 130 heading-only windows, and 85 section queries. Adding 1,670 unique short distractors produces a stress corpus of 2,869 documents. Examples are `Discussion: <public heading>` and `Обсуждение: <public heading>`. These templates were built from document headings independently of query wording. They mention topics but contain no substantive answer. This is a stress test, not an estimate of the frequency or style of real chats.

| Exact-search corpus | Granite nDCG | Qwen nDCG | Granite − Qwen, 95% CI |
|---|---:|---:|---|
| Wiki windows | 0.7782 | 0.7945 | −0.0163 [−0.0532, +0.0244] |
| Wiki + short distractors | 0.7420 | 0.7877 | **−0.0457 [−0.0861, −0.0015]** |
| Same mixed corpus, candidates restricted to query language | 0.8188 | 0.8560 | **−0.0372 [−0.0709, −0.0024]** |
| FiQA10k, 648 queries | 0.5539 | 0.6345 | **−0.0806 [−0.0983, −0.0633]** |

The mixed-corpus gap also survives article-cluster resampling: −0.0457 [−0.0967, −0.0043]. Adding distractors changes Granite by −0.0363 [−0.0590, −0.0164], versus Qwen's −0.0069 [−0.0159, −0.0010].

Granite places **21.41%** short texts in its mixed-corpus top tens, versus **13.53%** for Qwen: **+7.88 percentage points [5.06, 10.94]**. Of Granite's 182 short hits, 181 are irrelevant under the section qrels; Qwen has 114 irrelevant short hits among 115. These are repeated query-hit counts, not unique documents. Even without added distractors the short-hit gap is +2.94 points [1.41, 4.94]. With 400 added distractors it is +3.65 points [1.65, 6.00].

Examples: for `генераторы и yield`, Granite's first hit is `Обсуждение: Генераторы`; Qwen returns the substantive generator section. For the origin of chloroplasts, Granite returns a discussion title while Qwen returns an explanatory paragraph. This is a semantic near-negative problem: the short text accurately identifies the topic but does not supply the requested information.

**A structural limit matters:** Granite uses the same unprompted encoder for queries and documents. If a non-answer heading is identical to the query, its cosine is exactly 1 for every shared weight patch. Qwen's query instruction makes its two input strings different. This is a mathematical constraint, not an estimate of how much of the observed gap prompts cause. A shared weight patch can improve near-negative ordering but cannot solve that exact-identity case without role information or an additional relevance stage. Granite's prompt-free behavior is nevertheless its intended usage.

FiQA behaves differently. Its short-hit rates are 0.478% and 0.525%, a difference of −0.046 points [−0.247, +0.154]. Granite's cosine/log10(character-length) slope is +0.01248 there; the Granite–Qwen slope difference is +0.01234 [+0.01133, +0.01331]. In the mixed wiki corpus Granite's marginal slope is essentially flat, +0.00004. A flat corpus-wide slope therefore does **not** rule out excessive ranking of a particular class of short non-answers.

As a negative control, the 58 available FiQA texts shorter than 80 characters were added to wiki. Granite retrieved just one such hit in 850 top-10 slots; Qwen retrieved none. The difference in their hit share is +0.00118 [0, +0.00353]. This small control has different content and fewer items than the main stress set; it is suggestive, not a volume-matched causal experiment.

“Looks fine on BEIR” should not be interpreted as “matches Qwen”: this exact public FiQA subset already shows an overall quality gap, although it does not show the workspace-like short-hit pathology. The added fragment distribution exposes an additional failure mode.

### 2. Granite has a strong shared CLS component, but removing it is not a sufficient mechanism-level fix

| Mixed wiki geometry | Granite | Qwen | Difference, 95% CI |
|---|---:|---:|---|
| Norm of mean unit document vector | 0.8842 | 0.5164 | +0.3679 [+0.3631, +0.3721] |
| Energy in each vector's largest eight coordinates | 0.3407 | 0.0846 | +0.2561 [+0.2554, +0.2569] |
| Top-10 k-occurrence skew | 3.6329 | 3.4551 | +0.1779 [−0.8301, +0.8610] |

Granite's mean-vector norm on FiQA is also large, 0.8860; relative to Qwen the difference is +0.4006 [+0.3994, +0.4018]. Thus the shared cone is present even where the short-hit pathology is absent. There is **no statistically resolved excess of global k-occurrence skew** in these samples. “More anisotropic” must not be equated with “more retrieval hubs” without measuring the latter.

The centered Granite mixed-corpus covariance puts 8.62%, 28.66%, and 37.49% of variance in its first 1, 5, and 10 PCs. TABLES.md reports both models' spectra, plus paired comparisons of held-out variance along axes fitted independently on FLORES dev. Those are centered-variance measurements, distinct from the large uncentered mean component.

Across 74 matched heading/following-body pairs, Granite headings are closer to the mixed-corpus mean by +0.0256 [+0.0219, +0.0293]. Against a mean computed only from substantive bodies, the gap remains +0.0080 [+0.0054, +0.0107]. But Qwen also has this tendency: its body-only-centroid gap is +0.0427 [+0.0261, +0.0593]. Centroid proximity alone does not explain the model ordering.

Layer probes on 46 matched pairs provide more specific evidence. The CLS state initially is exactly the same BOS state for every input. At layer 10, heading/body cosine to the empty-input state is 0.9862/0.9829, with paired gap +0.00334 [+0.00271, +0.00396]. After layer 22 and final LayerNorm, it is 0.8061/0.7706, gap **+0.03550 [+0.02968, +0.04057]**. Short headings retain more of the common empty-input direction, while late layers add substantial content discrimination. Their pre-L2 output norm is also higher by +0.3762 [+0.2985, +0.4563]; subsequent L2 normalization removes that magnitude advantage from cosine scoring.

Zeroing the BOS input embedding changes heading directions more than body directions: difference in `1 − cosine(original, ablated)` is +0.1766 [+0.1011, +0.2511]. Replacing it with EOS gives +0.1585 [+0.1077, +0.2094]. These are destructive sensitivity probes, **not retrieval improvements or proof that BOS is the sole cause**. Doubling the BOS row leaves outputs unchanged to numerical precision because the embedding LayerNorm cancels positive scaling. Shifting all positions by +37 is likewise invariant; there is no evidence of an absolute-position artifact.

Interventions reject the simplest “remove the nuisance component” story: deleting PCs or high-energy dimensions, mean pooling, and earlier-layer CLS either hurt or fail to improve held-out quality. The shared component is entwined with the trained representation.

### 3. Russian/German weakness is not explained by fragmentation or a simple language mean

FLORES dev supplies 160 sentence triples for calibration, and devtest supplies 240 for evaluation, in EN/DE/RU. The 52 calibration and 70 evaluation source articles do not overlap. Topic content is aligned across languages.

| Language | Granite tokens/sentence | Qwen tokens/sentence | Difference, source-cluster 95% CI |
|---|---:|---:|---|
| EN | 28.48 | 28.85 | −0.38 [−0.62, −0.16] |
| DE | 37.76 | 44.58 | **−6.82 [−7.27, −6.38]** |
| RU | 39.69 | 51.57 | **−11.88 [−12.83, −10.94]** |

Granite is not losing these languages through greater token fragmentation. This does not test slang, typos, or workspace-specific identifiers.

Excluding exact translations, same-language neighbors comprise 37.06% of Granite's nearest tens versus 53.44% of Qwen's: **−16.39 points [−18.32, −14.48]**. The same-language similarity advantage is also smaller: difference −0.03841 [−0.03940, −0.03738]. Granite clusters **less** by language on matched topics.

EN→DE, DE→EN, and RU→EN translation retrieval reaches MRR@10=1 for both models, differences 0 [0, 0]. EN→RU is 0.9958 for both, difference 0 [−0.00598, +0.00600]. This easy translation test is near ceiling and cannot certify workspace retrieval quality. It does rule against a gross cross-language alignment failure in these inputs. Per-language mean correction learned on FLORES yields no meaningful general recovery: mixed-wiki held-out ΔnDCG −0.00023 [−0.00083, +0.00027]; Russian paired held-out Δ0 [−0.02307, +0.02307].

The original 30 Russian paraphrases do not reproduce the large account deficit: native-Russian ΔnDCG −0.00999 [−0.07229, +0.05357], English→Russian −0.04379 [−0.14636, +0.06124]. But Russian topical queries become vulnerable when short topical non-answers are added: ΔnDCG −0.05810 [−0.11755, −0.00205]. Under Russian-only candidates the mixed-corpus gap remains −0.08239 [−0.14681, −0.01970]. This points toward the fragment/relevance problem rather than a universal Russian encoding defect.

### 4. The wiki “German weakness” is partly an evaluation confound

`wikiq.py` credits only the specified language, article, and section. It can label an answer-bearing foreign-language passage irrelevant. Public examples include Granite returning Japanese descriptions of Python's creator or use in education to English/German questions.

For the 19 German queries, unrestricted Granite/Qwen nDCG is 0.6899/0.7632, difference −0.07331 [−0.16437, +0.01248]. Restricting candidates to German gives 0.8197/0.7952, difference **+0.02448 [−0.02088, +0.07128]**. Granite's foreign-language top-1 rate is 6/19 versus Qwen's 0/19, difference +0.3158 [+0.1053, +0.5263].

This control changes the candidate set; it does not declare every foreign-language hit relevant. It shows why this proxy alone cannot establish poor German semantic capacity. The real account judgments may be better grounded and are not invalidated by this finding. Conversely, the short-distractor deficit survives the language restriction, so it is not merely this labeling artifact.

### 5. Cheap geometry changes do not yield a general held-out recovery

Calibration uses only five photosynthesis articles: 592 mixed documents and 20 queries. Held-out evaluation uses ten city/Python articles, 65 queries, and 2,265 mixed documents; seven exact generic texts overlapping calibration were removed. Russian held-out testing has 24 targets/48 paired queries. FiQA10k is entirely outside calibration. The following rows were specified before reading held-out results; the full sweep is in TABLES.md.

| Change to Granite | Held-out mixed-wiki ΔnDCG, 95% CI | FiQA10k ΔnDCG, 95% CI |
|---|---|---|
| Query centering, 0.5·μ | −0.0002 [−0.0259, +0.0232] | **−0.0118 [−0.0201, −0.0036]** |
| Document centering, 0.5·μ | **−0.0436 [−0.0790, −0.0093]** | **−0.0210 [−0.0303, −0.0119]** |
| Both-side centering, μ | +0.0040 [−0.0085, +0.0168] | −0.0028 [−0.0089, +0.0037] |
| Remove top three calibration PCs | **−0.0166 [−0.0376, −0.0020]** | −0.0026 [−0.0067, +0.0014] |
| Zero eight highest-energy calibration dimensions | −0.0050 [−0.0186, +0.0030] | **−0.0039 [−0.0067, −0.0013]** |
| Keep first 512 dimensions | +0.0046 [−0.0022, +0.0129] | +0.0044 [−0.0011, +0.0102] |
| Mean pooling, both sides | **−0.2276 [−0.2967, −0.1600]** | **−0.1996 [−0.2216, −0.1782]** |
| Layer-21 CLS, same final LayerNorm | **−0.0207 [−0.0348, −0.0082]** | −0.0059 [−0.0163, +0.0048] |
| Calibration RMS diagonal scale, exponent −0.25 | +0.0006 [−0.0090, +0.0101] | **−0.0047 [−0.0087, −0.0005]** |
| Log-length slope correction | **−0.3020 [−0.3661, −0.2382]** | **−0.1059 [−0.1207, −0.0912]** |
| Subtract 0.03 cosine for docs <80 chars | +0.0122 [−0.0049, +0.0293] | +0.0001 [−0.0005, +0.0007] |

The 0.03 short penalty was selected by calibration nDCG. It reduces held-out short-hit share by −0.1431 [−0.1800, −0.1077], but its nDCG gain is uncertain, including under article resampling (+0.0122 [−0.0087, +0.0267]). A smaller exploratory penalty, 0.01, gives +0.0098 [+0.0024, +0.0190]; it was **not** the fit-selected winner and needs a fresh confirmation set. Do not conflate reducing short hits with improving relevance.

Folding is the reliable structural control: heading-only folding gives +0.01154 [+0.00145, +0.02576] on all 85 queries, and **+0.00441 [+0.00026, +0.00965]** on the 65 held-out queries. It is an indexing change, not a weight patch. Valid short answers must be preserved; even FiQA's 45 queries whose relevant targets are all under 400 characters show Granite − Qwen nDCG −0.0930 [−0.1671, −0.0256].

## Late rig metadata: corroboration with strict scope limits

The late export covers two shared, Wikipedia-style **English editor spaces**, not the nine-space corpus or Russian chats. Only `known-ranks.jsonl` and `topical-hits.jsonl` were read, after checking their schemas contain ranks and identifiers only. The three text-bearing query/pool files were not opened, despite the export README suggesting re-embedding them: the task's no-account-text rule takes precedence. No vectors were supplied, so there is no account-vector geometry or closed-pool re-embedding in this report.

There are 108 known-item queries over 36 targets. The `weak` flags live in a text-bearing file that was deliberately not read. Therefore these are **unfiltered metadata-only diagnostics, not the rig's canonical weak-filtered metrics**, and cannot be equated with the supplied 402-query scores. Intervals resample the 36 target groups, preserving all query variants.

| Unfiltered rank-only cohort | Granite MRR@10 | Qwen MRR@10 | Difference, paired 95% CI |
|---|---:|---:|---|
| Vector, all 108 | 0.4710 | 0.5097 | −0.0387 [−0.1114, +0.0282] |
| Hybrid, all 108 | 0.4780 | 0.5096 | −0.0316 [−0.0788, +0.0104] |
| Vector, keyword queries (36) | 0.8704 | 0.9653 | −0.0949 [−0.1991, −0.0046] |
| Vector, Russian cross-language queries (36) | 0.2775 | 0.2461 | +0.0314 [−0.0838, +0.1472] |

FTS ranks agree exactly: ΔMRR 0 [0, 0]. This checks pairing and supports isolating the embedding/index leg rather than attributing these rank differences to different FTS results. The metadata does not support a universal Russian disadvantage in these two English-document spaces. It says nothing decisive about Russian chat retrieval. Topical short-hit and nDCG metrics were not recomputed because lengths and judgments were bundled with account text. See [rig_metadata.json](rig_metadata.json); a future text-free export of `qid, chunk, chars, language, heading_only, relevance, cosine` plus vectors would permit the missing checks without exposing content.

The export documentation specifies 2,048 tokens, one document per call, Q8_0 models, the stated prompts/pooling, and IVF-SQ with RRF k=60. These are provenance statements, not runtime settings inspected on this Mac. This late evidence does not validate any proposed weight patch.

## Pipeline audit and what was ruled out

The [IBM model card](https://huggingface.co/ibm-granite/granite-embedding-311m-multilingual-r2), local Sentence Transformers modules, and actual GGUF metadata agree: **CLS pooling, no prompts, BOS=2 only, and L2 normalization**. EOS=1 and PAD=0. There is no intended query prefix missing from production. Mean pooling is an experimental departure, not a correction to intended usage.

The model advertises 32,768 tokens; production defaults to 2,048. Production inserts EOS at the truncation boundary, unlike ordinary HF truncation. Only eight role/text records exceeded the cap; they were re-encoded with production tails. Their original/production cosines range from 0.99761 to 1. This does not explain short inputs. Source inspection also finds that `maxDocTokens` divides context by `batchDocs` when greater than one (default is one); runtime configuration should be verified independently without assuming a larger per-document cap.

Granite fp32 MPS batch/single cosine minimum is 0.99999988. Qwen bf16 MLX batch/single minimum across 12 checks is 0.999845; its small numerical variation is a limitation for tiny ranking differences. The supplied Granite Q8/HF cosine 0.99998 is prior evidence, not a new end-to-end GGUF measurement. Quantization was not separately ablated. The one empty public FiQA document has a defined CLS fallback for content-only mean pooling; production CLS vectors were finite and nonzero throughout.

Transformers 4.57.6's Mistral-regex warning is a local-config detection false positive: the saved Granite version is 4.57.3 and its pre-tokenizer is a space Split. All 103 tested multilingual token sequences match direct `tokenizer.json` tokenization. Do not apply an unrelated Mistral regex as a fix.

No learned absolute-position table or final LayerNorm bias exists. The actual graph uses RoPE and `final_norm.weight` only. `norm_bias=false`; llama.cpp passes null bias to the final norm. Positive BOS-row rescaling is canceled by embedding normalization. These are direct constraints on patch design.

The [IBM paper](https://arxiv.org/html/2605.13521v1) documents a CLS cosine objective, title/body and passage pairs, distillation, and an English-focused model merge. A training-distribution mismatch with contextless chat fragments is consistent with the public findings. Neither the merge nor limited capacity was causally isolated here. “Smaller model” is not a demonstrated explanation.

## Patch candidates

All tensor names below are exact HF names. Changes must retain the original architecture, tokenizer, output dimension, and CLS pooling. No proposed edit has been written to model files in this investigation.

### A. Constrained diagonal readout adjustment — cheapest weight-compatible experiment

**Edit:** `final_norm.weight` (shape `[768]`), mapped to GGUF `output_norm.weight`. For a diagonal factor `a`, set `gamma_new[j] = a[j] * gamma_old[j]`. This exactly implements `normalize(a * embedding)` after pooling, without changing the graph.

**Concrete first control:** set `a[0:512]=1`, `a[512:768]=0`. This keeps 768 output coordinates and implements the tested 512-dimensional Matryoshka geometry. Its held-out mixed-wiki and FiQA changes are +0.0046 [−0.0022, +0.0129] and +0.0044 [−0.0011, +0.0102]: promising only as a cheap falsifiable candidate, **not a demonstrated win**. Next compare bounded, supervised factors initialized at one, fitted with topic-matched short non-answers and substantive positives. Preserve short factual positives.

Calibration's highest-energy coordinates, zero-based, begin `[71, 12, 277, 449, 674, 72, 182, 297]`. Blindly deleting them was not helpful; inverse-RMS scaling also hurt FiQA. Parameters for reproducing those negative controls are in `calibration.npz`.

**Expected effect / test:** change angular margins without damaging the encoder. Require improvement on a fresh query set and a reduction in irrelevant-short hits; compare against unchanged Granite and the identity scale. The existing held-out results are now exploratory evidence and should not become a fresh confirmation set after further tuning.

### B. Train late CLS computation against short topical non-answers — strongest mechanism-targeted candidate

**Edit only, initially:** `layers.21.attn.Wqkv.weight`, `layers.21.attn.Wo.weight`, `layers.21.mlp.Wi.weight`, `layers.21.mlp.Wo.weight`, `layers.21.attn_norm.weight`, `layers.21.mlp_norm.weight`, optionally `final_norm.weight`. These are the last, global-attention block's existing tensors. If rank is restricted during training, merge the learned update into these matrices before GGUF conversion.

**Training comparison:** query → substantive passage positive; matched heading/discussion ping negative; short factual answer positive. Balance EN/RU/DE and include cross-language positives. Distill Qwen's candidate ranking as an auxiliary signal rather than copying its absolute cosine scale. Regularize against unchanged Granite on FiQA/public passage pairs. Split both articles and template families before fitting.

**Expected effect / test:** teach answer-bearing content to outrank an accurate topic mention, preserving cross-language alignment and good short targets. This is a hypothesis justified by the failures, not a measured patch. Test exact search first; report paired nDCG/MRR and short-negative-share intervals on unseen data. Keep a separate passage benchmark as a non-inferiority check, with a predeclared tolerance rather than “not statistically significant” as proof of no harm.

**Negative control:** zeroing `layers.21.attn.Wo.weight` and `layers.21.mlp.Wo.weight` exactly bypasses the last block's two residual updates. GGUF names are `blk.21.attn_output.weight` and `blk.21.ffn_down.weight`. It reproduces the tested layer-21 output, which hurts mixed-wiki nDCG −0.0207 [−0.0348, −0.0082]. Do **not** simply remove the last block: its learned discrimination is useful.

### C. BOS-direction-only tuning — isolated causal experiment, lower priority

**Edit:** only row 2 of `embeddings.tok_embeddings.weight` (768 values); GGUF `token_embd.weight` at token ID 2. Optimize its direction under the same contrastive/anchor losses, with all other rows fixed. Constrain update size and use separate short/long and language evaluation slices.

**Expected effect / test:** determine whether the common short-input readout can be adjusted without retraining blocks. The ablations show stronger short-input sensitivity but do not show a quality gain. Compare identity, norm-preserving directional update, and positive scalar rescaling. Scalar rescaling should be a no-op; EOS replacement/zeroing are destructive controls, not candidates to ship. There is no evidence for editing broad Russian/German token rows.

### Changes that cannot simply be folded into existing weights

- **Final-LayerNorm mean shift:** no bias tensor exists and the stock graph does not consume one. Moreover, subtracting a mean from *unit* embeddings would require a norm-dependent pre-normalization shift for exact equivalence. A fixed bias would only approximate it even in a bias-supporting graph.
- **Query-only/document-only centering:** requires role-specific behavior; one unchanged shared encoder cannot represent both transformations merely by a common final scale.
- **General PC projection:** is a dense output mixing, not a diagonal LayerNorm scale. It cannot simply be absorbed into the final residual output matrices because of the skip path and normalization. It could be approximated through training, but the measured PC-removal controls do not motivate it.
- **Per-language centering:** requires language-dependent routing; it is not a common affine edit. The public alignment results do not motivate damaging language-specific token embeddings.
- **Length penalties, mean pooling, and folding:** require score/pooling/input changes or learned approximations. They are not exact tensor-only edits under unchanged CLS semantics.

For every weight candidate, convert a copied checkpoint and recheck HF-versus-Q8 output agreement on short, long, and multilingual texts. Rebuild document embeddings and the IVF index if document geometry changes. Measure ANN recall against exact neighbors and repeat RRF evaluation separately. Exact results here neither certify IVF recall nor predict hybrid gains. In particular, query-side centering's FiQA loss occurs already under exact search, so IVF is not required to produce that regression.

## Reproduction and handoff

See `scripts/embedbench/research/granite_quality/README.md`. The scripts rebuild public sets, encode sequentially, check tokenizer/GGUF metadata, verify vector/statistical integrity, run retrieval and geometry, and generate the numeric appendix. Outputs include per-query metric arrays, calibration parameters, hashes, and public hit metadata. Temporary full vector banks and layer-vector dumps were removed after analysis; rerun encoding to recreate them. No original assets were modified.

Granite encoding took 654 seconds; Qwen 1,345 seconds on this Mac. One heavy job ran at a time. Peak new on-disk artifacts were approximately 484 MB, well below the 6 GB limit. This study omits a third model, new MIRACL/Mr.TyDi runs, fresh blind cross-language relevance judgments, and a new production IVF/hybrid run. Existing rig ranks were analyzed separately as described above. Those are limits on generalization, not reasons to reinterpret the negative interventions as successful fixes.
