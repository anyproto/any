# Recall-gate study (L4) on public sets with graded qrels (Qwen3 legs)

Gate = inject a content hit (title hits excluded) when its fused score >= θ, up to the cap. Positive = qrel grade >= 1. Normalized score = raw / ((ftsW + vectorWeight) / 61).

| gate | weights | θ (raw) | injected / query | share of queries injecting | precision | relevant injected / relevant in top cap |
|---|---|---|---|---|---|---|
| bao (cap 5) | w1 | 0.0150 | 5.00 | 100% | 0.248 | 1.000 |
| bao (cap 5) | w2 | 0.0150 | 5.00 | 100% | 0.253 | 1.000 |
| bao (cap 5) | w2 | 0.0225 | 5.00 | 100% | 0.253 | 1.000 |
| bao (cap 5) | w1 | 0.0250 | 4.08 | 96% | 0.280 | 0.922 |
| bao (cap 5) | w2 | 0.0375 | 4.06 | 96% | 0.289 | 0.926 |
| any-agents (cap 3) | w1 | 0.0300 | 2.09 | 89% | 0.413 | 0.889 |
| any-agents (cap 3) | w2 | 0.0300 | 3.00 | 100% | 0.338 | 1.000 |
| any-agents (cap 3) | w2 | 0.0450 | 2.12 | 90% | 0.428 | 0.895 |
| any-agents (cap 3) | w1 | 0.0320 | 0.99 | 70% | 0.598 | 0.612 |
| any-agents (cap 3) | w2 | 0.0480 | 0.92 | 68% | 0.632 | 0.574 |

w1 top-5 fused scores: relevant median 0.0318 (IQR 0.0306-0.0325, n=2559); non-relevant median 0.0294 (IQR 0.0263-0.0310, n=7761)

## Which signal separates relevant from non-relevant injected candidates? (top-5 content hits at w1, all 9 sets)

| signal | AUC |
|---|---|
| fused score (w1) | 0.773 |
| vector cosine | 0.786 |
| cosine minus query top-1 cosine | 0.794 |
| hit coverage of query words | 0.686 |
| cosine + coverage (z-sum) | 0.783 |

2559 relevant, 7761 non-relevant candidates.
