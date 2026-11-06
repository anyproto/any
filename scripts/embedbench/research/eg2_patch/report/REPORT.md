# EmbeddingGemma 2 weight patch: frozen held-out results

**Recommend the head-projection Q8 candidate for the teammate's full workspace
evaluation.** It has reproducible public held-out gains and runs unchanged in
stock llama.cpp. It is not a universal retrieval improvement or a demonstrated
Russian-language capability fix. Deployment remains unvalidated because exact
search and the cleared pools do not reproduce the full workspace index/hybrid.

The recommended file is [embeddinggemma-2-patched-Q8_0.gguf](out/embeddinggemma-2-patched-Q8_0.gguf),
with [BF16](out/embeddinggemma-2-patched-BF16.gguf) alongside it. Only
`output.weight` differs. The separate 32-column writer patch is retained for
research, with its regressions documented; it is not the primary recommendation.

The strongest confirmation is full FiQA: nDCG@10 rises from
0.3173 to
0.3483, difference +0.0310 [+0.0222, +0.0405].
On the short topical-distractor mix the difference is +0.0432 [+0.0208, +0.0700].
The cleared rig pools show +0.0176 [+0.0016, +0.0381] for known items and
+0.0442 [+0.0009, +0.1101] for topical questions. All brackets in this report are paired
95% bootstrap intervals unless explicitly described otherwise.

These gains do not close FiQA's gap to Qwen: head minus Qwen on full FiQA is
-0.1206 [-0.1400, -0.1015]. Small mean losses remain on folded wiki, SciFact,
and the RU/DE MIRACL pools, with main-set intervals spanning zero. A folded-RU
slice has a larger negative result. The tables disclose both wins and losses.

## Per-set confirmation

All results use the actual frozen Q8 artifacts. The same 648 queries appear in
the two FiQA rows; the wiki variants also share queries. Do not count these as
independent replications. MIRACL and cleared-rig results are closed-pool tests.

| Set | Original nDCG | Head nDCG | Head minus original | Head minus Qwen | Writer minus original |
|---|---:|---:|---|---|---|
| wiki | 0.8747 | 0.8821 | +0.0074 [-0.0032, +0.0188] | +0.0428 [-0.0061, +0.0945] | -0.0026 [-0.0165, +0.0114] |
| wiki-mix | 0.7534 | 0.7966 | +0.0432 [+0.0208, +0.0700] | -0.0414 [-0.1017, +0.0197] | +0.0386 [+0.0149, +0.0662] |
| wiki-fold | 0.8914 | 0.8874 | -0.0040 [-0.0126, +0.0042] | +0.0472 [-0.0008, +0.0987] | -0.0107 [-0.0229, -0.0003] |
| ru | 0.8723 | 0.8968 | +0.0245 [+0.0021, +0.0637] | +0.0287 [-0.0302, +0.0978] | -0.0172 [-0.0730, +0.0255] |
| fiqa10k | 0.4737 | 0.5103 | +0.0366 [+0.0278, +0.0454] | -0.1225 [-0.1405, -0.1039] | +0.0423 [+0.0324, +0.0524] |
| fiqa-full | 0.3173 | 0.3483 | +0.0310 [+0.0222, +0.0405] | -0.1206 [-0.1400, -0.1015] | +0.0365 [+0.0271, +0.0462] |
| scifact | 0.8526 | 0.8507 | -0.0020 [-0.0094, +0.0051] | +0.1496 [+0.1103, +0.1888] | -0.0073 [-0.0146, -0.0005] |
| nfcorpus | 0.3552 | 0.3578 | +0.0027 [-0.0034, +0.0087] | +0.0025 [-0.0145, +0.0209] | +0.0017 [-0.0039, +0.0077] |
| miracl-ru-pool | 0.8626 | 0.8588 | -0.0038 [-0.0105, +0.0024] | +0.0229 [-0.0047, +0.0514] | -0.0078 [-0.0176, +0.0013] |
| miracl-de-pool | 0.7956 | 0.7895 | -0.0061 [-0.0142, +0.0007] | +0.0138 [-0.0185, +0.0437] | -0.0024 [-0.0114, +0.0066] |
| rig-known | 0.9396 | 0.9573 | +0.0176 [+0.0016, +0.0381] | -0.0055 [-0.0474, +0.0348] | +0.0301 [+0.0014, +0.0641] |
| rig-topical | 0.6551 | 0.6993 | +0.0442 [+0.0009, +0.1101] | +0.0027 [-0.0975, +0.0871] | +0.0650 [+0.0147, +0.1399] |

The stress-set head gain survives article-cluster resampling:
+0.0432 [+0.0233, +0.0649].
Restricting each wiki query to its own language gives
+0.0401 [+0.0200, +0.0653].
This supports transfer beyond the photosynthesis calibration topic and beyond
cross-language distractor effects, within this small synthetic wiki experiment.

The [complete five-metric tables](TABLES.md) report nDCG@10, MRR@10, Recall@10,
and top-ten shares below 80/400 characters for original EG2, Qwen, explicit
centering, both patches and both random controls. Every variant has paired
intervals against both original EG2 and Qwen. Direct head/writer/centering
comparisons are included. Per-query rankings and metrics are saved under `results/`.

## Controls, centering and short-answer risks

| Set | Head minus matched random, nDCG | Writer minus matched random, nDCG | Head minus explicit centering, nDCG |
|---|---|---|---|
| wiki | +0.0114 [-0.0010, +0.0266] | -0.0046 [-0.0177, +0.0082] | -0.0074 [-0.0147, -0.0012] |
| wiki-mix | +0.0450 [+0.0218, +0.0733] | +0.0397 [+0.0154, +0.0682] | -0.0361 [-0.0609, -0.0160] |
| wiki-fold | +0.0018 [-0.0072, +0.0113] | -0.0108 [-0.0228, -0.0005] | -0.0039 [-0.0098, +0.0010] |
| ru | +0.0268 [+0.0035, +0.0630] | -0.0172 [-0.0730, +0.0255] | -0.0086 [-0.0249, +0.0000] |
| fiqa10k | +0.0400 [+0.0309, +0.0497] | +0.0481 [+0.0375, +0.0590] | -0.0074 [-0.0121, -0.0029] |
| fiqa-full | +0.0348 [+0.0258, +0.0442] | +0.0395 [+0.0297, +0.0496] | -0.0029 [-0.0084, +0.0026] |
| scifact | +0.0029 [-0.0059, +0.0120] | -0.0076 [-0.0149, -0.0009] | +0.0040 [-0.0003, +0.0099] |
| nfcorpus | +0.0019 [-0.0045, +0.0082] | +0.0020 [-0.0038, +0.0080] | +0.0014 [-0.0016, +0.0046] |
| miracl-ru-pool | +0.0011 [-0.0061, +0.0080] | -0.0076 [-0.0174, +0.0016] | +0.0061 [-0.0009, +0.0147] |
| miracl-de-pool | -0.0052 [-0.0138, +0.0020] | -0.0012 [-0.0104, +0.0084] | +0.0004 [-0.0047, +0.0062] |
| rig-known | +0.0178 [+0.0014, +0.0385] | +0.0300 [+0.0012, +0.0638] | +0.0039 [+0.0000, +0.0114] |
| rig-topical | +0.0502 [+0.0060, +0.1155] | +0.0646 [+0.0144, +0.1395] | -0.0224 [-0.0552, +0.0054] |

The targeted head effect exceeds its norm-matched random control on FiQA, the
stress set and the cleared pools. The frozen query-centering baseline is stronger
on the stress set: head minus centering is
-0.0361 [-0.0609, -0.0160].
On full FiQA their difference is
-0.0029 [-0.0084, +0.0026].
The weight patch is therefore a useful stock-GGUF route, not evidence that
shared head projection dominates role-specific query centering.

Head MRR@10 and Recall@10 differences on full FiQA are
+0.0315 [+0.0198, +0.0436] and +0.0407 [+0.0249, +0.0584]; on the
stress set they are +0.0336 [+0.0041, +0.0718] and
+0.0328 [+0.0108, +0.0590]. The effect is not confined to one relevance metric.

On the stress set, head changes the under-80 share by
-17.3846 [-20.3077, -14.4615] percentage
points. On full FiQA it changes the under-400 share by
-15.1389 [-16.0648, -14.2747] percentage
points. Suppressing short hits is not sufficient evidence of quality. Relevant
short-answer subsets show the following tradeoffs (exploratory slices):

| Set, all relevant answers under 400 chars | Queries | Head minus original nDCG | Writer minus original nDCG |
|---|---:|---|---|
| fiqa10k | 45 | -0.0100 [-0.0338, +0.0131] | -0.0445 [-0.0818, -0.0138] |
| fiqa-full | 45 | -0.0110 [-0.0294, +0.0015] | -0.0157 [-0.0397, -0.0003] |
| ru | 6 | +0.0116 [+0.0000, +0.0347] | -0.1710 [-0.4911, +0.0000] |
| miracl-ru-pool | 24 | -0.0133 [-0.0308, +0.0000] | +0.0119 [-0.0072, +0.0445] |
| miracl-de-pool | 26 | +0.0048 [-0.0025, +0.0153] | -0.0202 [-0.0448, -0.0011] |
| rig-known | 21 | +0.0000 [+0.0000, +0.0000] | +0.0000 [+0.0000, +0.0000] |

The writer's whole-set SciFact change is -0.0073 [-0.0146, -0.0005]; its MRR change
is -0.0089 [-0.0182, -0.0003]. It also loses on useful short FiQA and
DE-pool answers. Its folded-wiki query-level interval is negative, but the
article-cluster interval is
-0.0107 [-0.0248, +0.0031].
The writer performs better than the head in the small cleared topical pool
(head minus writer -0.0208 [-0.0413, -0.0014]),
but those wider regressions make it a less attractive default patch.

Folding headings alone improves original EG2 on wiki by
+0.0167 [+0.0020, +0.0343]
with article-cluster intervals. Adding short topical non-answers still hurts the
patched head relative to its clean corpus by
-0.0855 [-0.1387, -0.0387].
The patch mitigates this failure; it does not remove it. All condition contrasts
are in [CROSS_CONDITION.md](CROSS_CONDITION.md).

## Russian and multilingual interpretation

The 48-query RU-target set mixes 24 Russian paraphrases and 24 English queries
for Russian passages. Its aggregate gain must not be described as a pure
Russian-query gain. The cleared rig's `cross` queries are Russian paraphrases
of English gold passages.

| Slice | Queries | Head minus original nDCG | Writer minus original nDCG |
|---|---:|---|---|
| Russian queries / Russian passages | 24 | +0.0167 [+0.0000, +0.0487] | +0.0013 [-0.0449, +0.0474] |
| English queries / Russian passages | 24 | +0.0323 [+0.0031, +0.0796] | -0.0356 [-0.1105, +0.0280] |
| Cleared rig Russian queries / English passages | 35 | +0.0385 [+0.0000, +0.0858] | +0.0644 [+0.0086, +0.1276] |
| Russian wiki with topical distractors | 18 | +0.0297 [+0.0069, +0.0561] | +0.0272 [+0.0076, +0.0508] |
| Russian folded wiki | 18 | -0.0243 [-0.0438, -0.0058] | -0.0301 [-0.0580, -0.0084] |

MIRACL RU has head change -0.0038 [-0.0105, +0.0024]; MIRACL DE has
-0.0061 [-0.0142, +0.0007]. These are not positive multilingual confirmations.
The folded-RU negative slice is a warning against assuming the head combines
harmlessly with heading folding. Slice intervals are exploratory and pointwise;
the wiki samples contain few independent article topics. Russian chat retrieval
and full-corpus MIRACL remain untested.

## Native compatibility and artifact verification

Both candidate formats load and run unchanged through
`scripts/embedbench/mac/run.sh` on both stock builds. The public/synthetic
validation corpus contains 25 role/text inputs, including short, empty,
multilingual and truncated-long cases. Minimum cosine against the Python path:

| Candidate | Format | Stock build | Minimum cosine |
|---|---|---|---:|
| head | BF16 | b11454 | 0.99997920 |
| head | BF16 | b11600 | 0.99998569 |
| head | Q8_0 | b11454 | 0.99999863 |
| head | Q8_0 | b11600 | 0.99999905 |
| writer | BF16 | b11454 | 0.99999273 |
| writer | BF16 | b11600 | 0.99999464 |
| writer | Q8_0 | b11454 | 0.99999940 |
| writer | Q8_0 | b11600 | 0.99999952 |

All exceed the requested 0.9999 threshold. Qwen's float16 arithmetic path also
passes native validation (minimum 0.99999630). Shared-prefix writer inference
matches independent full forwards at >=0.999999. The cached head transformation
uses a pseudoinverse of the **fixed original output matrix** to recover pooled
pre-head states, not a head fitted to data. It matches direct forward at
0.99999988 or better.

| Q8 versus BF16, same candidate | Minimum cosine | Mean cosine |
|---|---:|---:|
| original | 0.99987739 | 0.99994612 |
| head | 0.99959689 | 0.99985361 |
| writer | 0.99988300 | 0.99994326 |

Projection increases Q8/BF16 drift in the head candidate. This is separate from
Python/native agreement, and is why confirmation used the actual Q8 artifacts.
Stock gguf-py quantization reproduces zero differing bytes at both edited sites
in the original ggml-org Q8 model. Edited tensors are quantized from rounded
patched BF16 values; all other Q8 tensor bytes remain original. Q8 block scales
can spread numeric changes within the edited tensor, so sparse scalar counts
in the method refer specifically to BF16.

The final audit verified every retained GGUF hash against the pre-evaluation
freeze, all frozen input/direction hashes, and the cleared export hashes. It
rebuilt the edited tensor payloads from packaged specs/directions and found
exact byte equality. Metadata, tensor names/shapes/types, and unedited tensors
were already verified byte-identical during publication. No commits, pushes or
branch changes were made. Temporary token dumps totaling 1,038 MiB and temporary
random-control BF16 copies were removed; retained artifacts occupy about 3.9 GiB.

| Artifact | Only differing tensor | Status |
|---|---|---|
| `embeddinggemma-2-patched-{BF16,Q8_0}.gguf` | `output.weight` | Recommended for independent workspace testing |
| `embeddinggemma-2-writer-{BF16,Q8_0}.gguf` | `blk.23.ffn_down.weight` | Research alternative with disclosed regressions |
| `embeddinggemma-2-head-random-Q8_0.gguf` | `output.weight` | Matched control |
| `embeddinggemma-2-writer-random-Q8_0.gguf` | `blk.23.ffn_down.weight` | Matched control |

Complete hashes: [SHA256SUMS](out/SHA256SUMS). Tensor/source/output hashes and
closed-form specs: [head manifest](out/embeddinggemma-2-patched-manifest.json),
[writer manifest](out/embeddinggemma-2-writer-manifest.json). The requested files:

- `embeddinggemma-2-patched-Q8_0.gguf`: `17b7bf4dd431cb043b0cddd1c24c2c156de9b14e32f49c95f6c9f9fa87b27cb8`
- `embeddinggemma-2-patched-BF16.gguf`: `ae92cc9252b321fa105db52cf33bf9aabbbcffcee95647b62aa48240fda70b6a`

## Protocol and data

All edits are closed-form tensor operations: no gradients, optimizer, LoRA,
learned head, embedding-table edit or final-norm edit. Only public data and the
explicitly cleared two-space export were used. No local account server was
contacted. Authored files stay in the two authorized directories.

The [protocol](PLAN.md) allowed 50 dev retrieval passes; **47** were used:
one baseline, ten site-isolation variants, fourteen dose variants, seven sparse
variants, ten head variants, four additional controls, and one centering baseline.
Activation-only diagnostics and numerical parity checks are separate.
[FROZEN.json](FROZEN.json) records candidate specifications, directions, data and
model hashes before held-out scoring. No candidate was changed after that point.
Confirmation scored seven variants on twelve sets once each: 84 primary
variant/set evaluations, plus 21 prespecified language-restricted wiki sensitivity
rankings from the same frozen embeddings. Subsets, bootstrap intervals and
cross-condition contrasts reuse those saved rankings; no test feedback was used
for another candidate or dose.

Calibration has 592 documents and 20 questions about **photosynthesis** in five
languages, including short topical non-answers. The 11 qualifying heading/body
pairs are EN 6, DE 1, RU 3 and ZH 1; none qualified in JA. A heading is paired with
the following related body, not an identical-content rendering. Length, topic
and answer content are consequently confounded. This is a narrow calibration
set, with considerable adaptive-selection risk.

| Confirmation set | Queries | Passages | Scope |
|---|---:|---:|---|
| FiQA 10k | 648 | 10,000 | Exact previous public subset |
| FiQA full | 648 | 57,638 | Same test queries; not an independent replication |
| SciFact | 300 | 5,183 | Public BEIR test |
| NFCorpus | 323 | 3,633 | Public BEIR test, graded relevance |
| Wiki windows | 65 | 921 | Other articles, EN/DE/RU/JA/ZH, headings retained |
| Wiki short-distractor mix | 65 | 2,265 | Same queries plus topical non-answers |
| Wiki folded | 65 | 827 | Same queries, headings folded into following windows |
| RU-target query pairs | 48 | 921 | 24 Russian paraphrases plus 24 English cross-language queries |
| MIRACL RU pool | 200 | 2,073 | Official-dev sample; union of judged passages |
| MIRACL DE pool | 200 | 2,045 | Official-dev sample; union of judged passages |
| Cleared rig known-item pool | 105 | 366 | Three weak queries excluded from 108 |
| Cleared rig topical pools | 12 | 330 unique | Each query ranks its own judged pool |

All 12 sets have zero exact document/query overlap with calibration, per the
[audit](results/data-overlap-audit.json). Wiki variants and RU paraphrases are
related tests. The inherited wiki labels and synthetic distractors do not make
this a random sample of real workspace chats.

MIRACL queries were selected by a fixed hash of their IDs, independently of
scores. Public [RU](https://huggingface.co/datasets/Hieuman/ru_MIRACL) and
[DE](https://huggingface.co/datasets/Hieuman/de_MIRACL) text repacks were checked
against [official MIRACL](https://huggingface.co/datasets/miracl/miracl) dev query
wording and judgment counts. Repack passage IDs are unavailable, so passage
identity cannot be independently checked. These are **closed pools, not full
MIRACL retrieval**. Provenance and source hashes are saved with each set.

The cleared export's README was read first; its passages and queries were opened
only after freeze. Topical text is clipped at 900 characters and labels are LLM
judgments. Existing models influenced pool construction. Known-item queries
search the union of pool and gold documents; topical queries search their own
judged pools. Neither reproduces the full rig index, IVF-SQ or hybrid scores.

## Faithful graph and unchanged production interface

The HF source is [google/embeddinggemma-2](https://huggingface.co/google/embeddinggemma-2),
revision `f9972ac22e696a105c482202646979e33e4b5a1e`. Inference reads the existing
[ggml-org GGUF weights](https://huggingface.co/ggml-org/embeddinggemma-2-GGUF)
directly. Source hashes and runtime versions are in [environment.json](environment.json).

The stock b11600 graph at
`bench/granite/llama.cpp-b11600/src/models/gemma-embedding2.cpp` applies the
**bias-free** `output.weight` per token after `output_norm`. Its NumPy shape is
`[768, 512]`; this linear map commutes with mean pooling. A shared output-space
projection can be folded into it. Constant mean subtraction cannot, because
there is no bias.

EG2 has 24 blocks, hidden width 512 and MLP width 2,048. Attention, MLP and
per-layer-embedding (PLE) writes have post-write RMS normalization. A layer output
scale follows the residual updates. Thus projecting a raw writer matrix does
not exactly project its final residual contribution. The Python implementation
includes the PLE path, attention masks, q/k/v normalization, BOS/EOS and truncation.

All candidates preserve tensor names, shapes, types, metadata, mean pooling,
L2 normalization and the 2,048-token context. Prefixes remain exactly
`title: none | text: ` and `task: search result | query: `.
Qwen uses the existing production Q8 weights and query instruction.

## Direction derivation and localization

At layer `l`, with pooled matched heading/body states `s_i,b_i`, the residual
direction is `u_l = normalize(mean(normalize(s_i) - normalize(b_i)))`.
All-layer residuals, raw and normalized attention/MLP/PLE writes, and writer-input
RMS activations were captured. Columns are ranked by
`abs(u_l @ W[:,j]) * RMS(input_j)`. Complete per-token traces were collected for
representative pairs and dev queries; pooled/BOS/prefix/content summaries and
audits remain after reproducible temporary dumps are removed.

The initial localization heuristic compared unscaled writes against post-scale
residual magnitudes. A post-freeze audit corrected that confound: the largest
contributions are attention layer 0, MLP layers 5/3/4, then attention layers 2/10.
The selected final MLP ranks **seventh**, not first, after correction. Its causal
evidence comes from intervention and controls, not a claim that it is the sole
writer. See [corrected localization](dev/localization-scaled.json).

The sparse writer candidate applies (zero-based layer and column indices):

```
W[:,J] -= 0.5 * outer(u_23, u_23 @ W[:,J])
W = blk.23.ffn_down.weight
J = [268,1876,663,1346,2012,1860,209,389,679,765,530,920,1644,1647,1006,418,
     1971,414,1148,1185,240,1745,1651,1156,1772,1571,1963,1026,818,1134,1454,1536]
```

It touches 16,384 values; **15,299 actually differ after BF16 rounding**.
Its Frobenius change is 0.6801% of this matrix's norm. It is the smallest tested
writer edit attaining the selected dev effect, not a global minimum. No attention
head or PLE matrix is changed in the final candidate.

The separate head candidate uses `v = normalize(mean(unit dev doc embeddings))`
over all 592 dev documents and applies `W -= outer(v, v @ W)` to `output.weight`.
It changes 362,680 BF16 values, with Frobenius change 14.4255% of the matrix norm.
This is a broader edit and is kept separate from the sparse writer experiment.
An output-space heading/body contrast was also tested; it did not win on dev.

Each candidate has a seeded orthogonal random direction in the **same matrix and
columns**, matched for pre-rounding Frobenius edit norm and subjected to identical
rounding/quantization. A single seed is a limited control, not a distribution
over random edits. Direction vectors and exact specs are packaged in `out/`.

Explicit centering uses `normalize(q - 0.5*mu)`, with `mu` the mean of 100 seeded
unit **dev** document vectors. The same frozen mean is used on every test set.
This is transferable centering, not the earlier corpus-adapted fix #1; fitting
a mean on each reported corpus was avoided.

## Development controls and mechanism limits

Dev inference uses BF16 source weights with float32 arithmetic. Original dev
nDCG@10 is 0.36899. Below are paired 95% bootstrap intervals, 4,000 draws, for
differences from original. These intervals are exploratory after adaptive
selection on only 20 queries.

| Dev intervention | Delta nDCG@10 [95% CI] |
|---|---|
| Final MLP, all columns, alpha 0.1 | +0.0333 [+0.0043, +0.0705] |
| Final MLP, all columns, alpha 0.25 | +0.0864 [+0.0335, +0.1484] |
| Final MLP, all columns, alpha 0.5 | +0.0974 [+0.0412, +0.1626] |
| Final MLP, all columns, alpha 1 | -0.0636 [-0.1241, -0.0108] |
| Selected 32 columns, alpha 0.25 | +0.0712 [+0.0245, +0.1285] |
| Selected 32 columns, alpha 0.5 | +0.1091 [+0.0446, +0.1843] |
| Same columns, reverse alpha -0.5 | -0.2836 [-0.3738, -0.1941] |
| Matched writer random control | +0.0000 [+0.0000, +0.0000] |
| Mean-head projection | +0.1377 [+0.0773, +0.2047] |
| Matched head random control | +0.0054 [-0.0083, +0.0235] |
| Explicit query centering | +0.1646 [+0.1011, +0.2329] |

The writer response is nonmonotonic: partial attenuation helps on dev, while
stronger or reversed edits can seriously damage it. Early attention and layer-5
MLP edits were explored but did not displace the selected sparse candidate.
Every attempted configuration is retained in [DEV_TABLES.md](DEV_TABLES.md) and
`dev/passes.jsonl`; this includes losses, not only the selected configurations.

![Development dose response](dev/dose-response.png)

The offset-corrected [token audit](dev/prompt-audit.json) does **not** establish
prompt-only erasure. Along the selected direction, original short-minus-body
pooled contrast is +3.5906 [3.2676, 3.8680]; prefix-token contrast is -0.0017
[-0.2257, 0.2624], BOS contrast +0.0782 [-0.0562, 0.1854], and content contrast
+3.3976 [3.0166, 3.7322]. The writer changes short pooled states by -0.2415
[-0.2831, -0.1980] and body pooled states by +0.4945 [0.4474, 0.5337]. These are
activation units and in-sample pair intervals, not retrieval gains. Body content
and nonlinear normalization matter. Initial empty-prefix token-count summaries
were approximate; the audit uses actual tokenizer offsets.

The subsequent dev-only [corpus audit](dev/corpus-alignment.json) measured all
592 documents and 20 queries without changing the frozen directions. At layer
23, the writer direction has cosine -0.1043 with the raw pooled document mean,
+0.0973 with the BOS mean, and -0.0072 with the document-prefix mean. Its cosine
with the pooled query mean is -0.0819. The output-space heading/body contrast
and output corpus-mean direction have cosine +0.0909. The writer and head
directions are distinct, and the writer direction is not a strong alignment
with the corpus or prompt mean. These descriptive alignments further limit a
simple common-prompt-component explanation.

The experimental logic follows the [weight-space programming post](https://rikiyat.github.io/blog/weight-space-programming/)
and [authors' logs](https://github.com/RikiyaT/lm_understand-logs/tree/main/exp8_math).
The p50c recipe used matched contexts, selected MLP columns and an empirically
chosen scale; p10c was much weaker. These are precedents for intervention tests,
not proof that EG2 contains the same kind of clean component. Consistent with
[Honnibal's critique](https://honnibal.dev/blog/astra-read-write-misleading), this
is activation- and feedback-guided search, not behavioral repair derived from raw
weights alone. It cannot establish added financial or multilingual capability.

## Statistics and operational limits

Rankings are exact cosine. nDCG@10 uses `2^grade - 1`; MRR@10 and Recall@10 count
positive grades as relevant. Short-hit shares use unprefixed text below 80/400
Unicode characters. A lower short-hit share alone is not an improvement; useful
short answers must retain relevance. All five metrics and all seven variants
appear in [TABLES.md](TABLES.md), with per-query arrays under `results/`.

Every reported metric difference has a seeded 4,000-draw paired bootstrap 95%
interval. RU and rig-known queries resample target groups. Wiki article-cluster
intervals supplement query intervals; language-restricted rankings check
cross-language distractor effects. Intervals are pointwise, without multiplicity
correction. An interval spanning zero does not prove equivalence or non-inferiority.
Related wiki variants and two FiQA corpus sizes are not independent replications.
An audit found that the short-positive subsets of RU and rig-known had initially
omitted target grouping. Their intervals were corrected from saved per-query
arrays, without rescoring; point estimates and all main-set intervals are
unchanged. The original/corrected bounds are in [interval-audit.json](results/interval-audit.json).

Exact search does not establish IVF-SQ recall or BM25/RRF hybrid gains. Any weight
change requires re-embedding documents and rebuilding the vector index. The
cleared English encyclopedia spaces plus RU paraphrases do not establish behavior
on Russian chats. The teammate's full workspace test is still needed before
deployment. No full MIRACL, broad bilingual alignment edit, attention-head
zeroing sweep, or combined writer-plus-head candidate was tested.

## Reproduction

The [script README](../../scripts/embedbench/research/eg2_patch/README.md) gives
source preparation, all dev passes, exact reconstruction from spec/direction
sidecars, frozen confirmation, native harness commands and cleanup. Scripts
refuse to overwrite frozen GGUFs or rescore already-scored test sets. The
[progress log](PROGRESS.md) records attempts, exceptions and checkpoints.

From the repository root, reconstruct the primary head candidate with a new
basename (avoiding overwrite):

```sh
export PYTHONDONTWRITEBYTECODE=1
export TMPDIR="$PWD/bench/eg2-patch/tmp"
bench/mlx/.venv/bin/python scripts/embedbench/research/eg2_patch/artifact.py \
  bench/eg2-patch/out/head-spec.json embeddinggemma-2-rebuilt \
  --directions bench/eg2-patch/out/patch-directions.npz
```

Use `writer-spec.json` and another basename for the separate writer patch.
The manifests identify the source model hashes, full output hashes and exact
differing tensors. Neither reconstruction requires MLX inference or optimization.

