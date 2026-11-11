# Recall gate on blend scores (Roma's M1 judged candidates)

1243 judged content candidates from 126 queries (the w1 page's top 10 content records, as in M1's gate); base rate relevant 0.31. Each candidate is scored by the fusion's own score: RRF normalized to fused / max for that query's weights and k, or the min-max blend. One global threshold across queries, cap 3 per query.

| score | AUC | precision at 0.7 injected / query | at 1.6 / query | top hit ≥ 0.9 (share of queries) |
|---|---|---|---|---|
| RRF w1 (today) | 0.735 | 0.85 (th 0.976) | 0.64 (th 0.914) | 0.76 |
| RRF w2 | 0.703 | 0.86 (th 0.974) | 0.65 (th 0.917) | 0.76 |
| blend α0.3 | 0.728 | 0.80 (th 0.857) | 0.61 (th 0.616) | 0.48 |
| blend α0.7 | 0.665 | 0.82 (th 0.855) | 0.56 (th 0.682) | 0.49 |
| F2 two-branch | 0.658 | 0.73 (th 0.948) | 0.60 (th 0.700) | 0.58 |
| learned depth-2, blends | 0.688 | 0.81 (th 0.952) | 0.62 (th 0.700) | 0.63 |
| learned depth-2, RRF only | 0.730 | 0.80 (th 0.970) | 0.67 (th 0.872) | 0.76 |

learned depth-2, blends: rrf branch, 577 candidates, AUC within branch 0.754, mean score relevant 0.762 / not 0.561

learned depth-2, blends: blend branch, 666 candidates, AUC within branch 0.709, mean score relevant 0.507 / not 0.295

F2 two-branch: blend branch, 728 candidates, AUC within branch 0.737, mean score relevant 0.511 / not 0.275

F2 two-branch: rrf branch, 515 candidates, AUC within branch 0.715, mean score relevant 0.797 / not 0.667
