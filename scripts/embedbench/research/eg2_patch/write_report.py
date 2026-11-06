"""Assemble the interpreted report for this frozen experiment from saved results."""
from common import *


def main():
    r = json.loads((BASE/'results/all.json').read_text())
    validation = json.loads((BASE/'validation/results.json').read_text())
    audit = json.loads((BASE/'results/final-audit.json').read_text())
    assert len(r) == 12 and audit['development_passes'] == 47
    def ci(x, scale=1):
        return f'{x["delta"]*scale:+.4f} [{x["lo"]*scale:+.4f}, {x["hi"]*scale:+.4f}]'
    def d(s, v='head', ref='original', metric='ndcg10'):
        return ci(r[s]['comparisons'][v][ref][metric])
    lines = [f'''# EmbeddingGemma 2 weight patch: frozen held-out results

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
{r['fiqa-full']['scores']['original']['ndcg10']:.4f} to
{r['fiqa-full']['scores']['head']['ndcg10']:.4f}, difference {d('fiqa-full')}.
On the short topical-distractor mix the difference is {d('wiki-mix')}.
The cleared rig pools show {d('rig-known')} for known items and
{d('rig-topical')} for topical questions. All brackets in this report are paired
95% bootstrap intervals unless explicitly described otherwise.

These gains do not close FiQA's gap to Qwen: head minus Qwen on full FiQA is
{d('fiqa-full', ref='qwen')}. Small mean losses remain on folded wiki, SciFact,
and the RU/DE MIRACL pools, with main-set intervals spanning zero. A folded-RU
slice has a larger negative result. The tables disclose both wins and losses.

## Per-set confirmation

All results use the actual frozen Q8 artifacts. The same 648 queries appear in
the two FiQA rows; the wiki variants also share queries. Do not count these as
independent replications. MIRACL and cleared-rig results are closed-pool tests.

| Set | Original nDCG | Head nDCG | Head minus original | Head minus Qwen | Writer minus original |
|---|---:|---:|---|---|---|''']
    for s, x in r.items():
        lines.append(f'| {s} | {x["scores"]["original"]["ndcg10"]:.4f} | {x["scores"]["head"]["ndcg10"]:.4f} | {d(s)} | {d(s, ref="qwen")} | {d(s,"writer")} |')
    lines += [f'''
The stress-set head gain survives article-cluster resampling:
{ci(r['wiki-mix']['comparisons']['head']['article_cluster_vs_original']['ndcg10'])}.
Restricting each wiki query to its own language gives
{ci(r['wiki-mix']['language_restricted']['comparisons']['head']['original']['ndcg10'])}.
This supports transfer beyond the photosynthesis calibration topic and beyond
cross-language distractor effects, within this small synthetic wiki experiment.

The [complete five-metric tables](TABLES.md) report nDCG@10, MRR@10, Recall@10,
and top-ten shares below 80/400 characters for original EG2, Qwen, explicit
centering, both patches and both random controls. Every variant has paired
intervals against both original EG2 and Qwen. Direct head/writer/centering
comparisons are included. Per-query rankings and metrics are saved under `results/`.

## Controls, centering and short-answer risks

| Set | Head minus matched random, nDCG | Writer minus matched random, nDCG | Head minus explicit centering, nDCG |
|---|---|---|---|''']
    for s, x in r.items():
        lines.append(f'| {s} | {ci(x["target_minus_random"]["head"]["ndcg10"])} | {ci(x["target_minus_random"]["writer"]["ndcg10"])} | {ci(x["candidate_comparisons"]["head_minus_centered"]["ndcg10"])} |')
    lines += [f'''
The targeted head effect exceeds its norm-matched random control on FiQA, the
stress set and the cleared pools. The frozen query-centering baseline is stronger
on the stress set: head minus centering is
{ci(r['wiki-mix']['candidate_comparisons']['head_minus_centered']['ndcg10'])}.
On full FiQA their difference is
{ci(r['fiqa-full']['candidate_comparisons']['head_minus_centered']['ndcg10'])}.
The weight patch is therefore a useful stock-GGUF route, not evidence that
shared head projection dominates role-specific query centering.

Head MRR@10 and Recall@10 differences on full FiQA are
{d('fiqa-full', metric='mrr10')} and {d('fiqa-full', metric='recall10')}; on the
stress set they are {d('wiki-mix', metric='mrr10')} and
{d('wiki-mix', metric='recall10')}. The effect is not confined to one relevance metric.

On the stress set, head changes the under-80 share by
{ci(r['wiki-mix']['comparisons']['head']['original']['short80'],100)} percentage
points. On full FiQA it changes the under-400 share by
{ci(r['fiqa-full']['comparisons']['head']['original']['short400'],100)} percentage
points. Suppressing short hits is not sufficient evidence of quality. Relevant
short-answer subsets show the following tradeoffs (exploratory slices):

| Set, all relevant answers under 400 chars | Queries | Head minus original nDCG | Writer minus original nDCG |
|---|---:|---|---|''']
    for s in ['fiqa10k','fiqa-full','ru','miracl-ru-pool','miracl-de-pool','rig-known']:
        x=r[s]['slices']['all_positive_under400']
        lines.append(f'| {s} | {x["n"]} | {ci(x["comparisons"]["head"]["original"]["ndcg10"])} | {ci(x["comparisons"]["writer"]["original"]["ndcg10"])} |')
    lines += [f'''
The writer's whole-set SciFact change is {d('scifact','writer')}; its MRR change
is {d('scifact','writer',metric='mrr10')}. It also loses on useful short FiQA and
DE-pool answers. Its folded-wiki query-level interval is negative, but the
article-cluster interval is
{ci(r['wiki-fold']['comparisons']['writer']['article_cluster_vs_original']['ndcg10'])}.
The writer performs better than the head in the small cleared topical pool
(head minus writer {ci(r['rig-topical']['candidate_comparisons']['head_minus_writer']['ndcg10'])}),
but those wider regressions make it a less attractive default patch.

Folding headings alone improves original EG2 on wiki by
{ci(json.loads((BASE/'results/cross-condition.json').read_text())['wiki-fold']['original']['ndcg10'])}
with article-cluster intervals. Adding short topical non-answers still hurts the
patched head relative to its clean corpus by
{ci(json.loads((BASE/'results/cross-condition.json').read_text())['wiki-mix']['head']['ndcg10'])}.
The patch mitigates this failure; it does not remove it. All condition contrasts
are in [CROSS_CONDITION.md](CROSS_CONDITION.md).

## Russian and multilingual interpretation

The 48-query RU-target set mixes 24 Russian paraphrases and 24 English queries
for Russian passages. Its aggregate gain must not be described as a pure
Russian-query gain. The cleared rig's `cross` queries are Russian paraphrases
of English gold passages.

| Slice | Queries | Head minus original nDCG | Writer minus original nDCG |
|---|---:|---|---|''']
    for s, label, title in [
        ('ru','kind/ru-para','Russian queries / Russian passages'),
        ('ru','kind/en-cross','English queries / Russian passages'),
        ('rig-known','kind/cross','Cleared rig Russian queries / English passages'),
        ('wiki-mix','lang/ru','Russian wiki with topical distractors'),
        ('wiki-fold','lang/ru','Russian folded wiki')]:
        x=r[s]['slices'][label]
        lines.append(f'| {title} | {x["n"]} | {ci(x["comparisons"]["head"]["original"]["ndcg10"])} | {ci(x["comparisons"]["writer"]["original"]["ndcg10"])} |')
    lines += [f'''
MIRACL RU has head change {d('miracl-ru-pool')}; MIRACL DE has
{d('miracl-de-pool')}. These are not positive multilingual confirmations.
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
|---|---|---|---:|''']
    for name in ['head','writer']:
        for dtype in ['BF16','Q8_0']:
            for version in ['b11454','b11600']:
                x=validation[f'{name}-{dtype}-{version}']
                lines.append(f'| {name} | {dtype} | {version} | {x["min_cosine"]:.8f} |')
    lines += ['''
All exceed the requested 0.9999 threshold. Qwen's float16 arithmetic path also
passes native validation (minimum 0.99999630). Shared-prefix writer inference
matches independent full forwards at >=0.999999. The cached head transformation
uses a pseudoinverse of the **fixed original output matrix** to recover pooled
pre-head states, not a head fitted to data. It matches direct forward at
0.99999988 or better.

| Q8 versus BF16, same candidate | Minimum cosine | Mean cosine |
|---|---:|---:|''']
    for name in ['original','head','writer']:
        x=validation[f'{name}_q8_vs_bf16']
        lines.append(f'| {name} | {x["min_cosine"]:.8f} | {x["mean_cosine"]:.8f} |')
    lines += ['''
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
''']
    for name in ['embeddinggemma-2-patched-Q8_0.gguf','embeddinggemma-2-patched-BF16.gguf']:
        lines.append(f'- `{name}`: `{audit["verified_sha256"][name]}`')
    lines += ['', (BASE/'report-methods.md').read_text()]
    (BASE/'REPORT.md').write_text('\n'.join(lines)+'\n')
    log('Final report recommends the frozen head candidate for independent workspace evaluation; records all per-set gains/losses, writer regressions, Russian limits, centering comparison, controls, native parity, hashes and reproduction.')


if __name__ == '__main__':
    main()
