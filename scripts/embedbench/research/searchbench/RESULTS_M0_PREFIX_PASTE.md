# SearchBench M0: results

Snapshot: Roma's Any Nightly (Team Agents, Agents), 196 queries on 120 targets.
Known-item MRR@10. A page target matches by object (title hits count); memories, chats and config match by record.
Δ = paired cluster bootstrap over targets, 95% CI; * = CI excludes 0.

## L0 integrity

- Offline re-fusion against the production page, all weights: 0 mismatches.
- Pages reporting vectorStatus "used" with an empty vector leg: 0.
- Production Qwen3 vector leg (app build b10620) against exact search (b11600 Metal query vectors), top-10 chunks:
  mean overlap 0.980, identical top-10 on 88/196 queries.

## L1 vector only (exact, no titles, no FTS)

EG2 = EG2 with the head projection; eg2raw = stock EG2 output. Δ against Qwen3.

| shape (n) | L1 qwen | L1 eg2 | L1 eg2raw |
|---|---|---|---|
| prefix (76) | 0.609 | 0.601 (-0.008 [-0.090, +0.074]) | 0.467 (-0.142* [-0.226, -0.062]) |
| paste (120) | 0.579 | 0.659 (+0.080* [+0.028, +0.134]) | 0.640 (+0.061* [+0.011, +0.114]) |
| all (196) | 0.591 | 0.636 (+0.046 [-0.003, +0.096]) | 0.573 (-0.018 [-0.065, +0.031]) |

## L2 FTS only

Production FTS leg and title pass, grouped like production.

| shape (n) | L2 fts+titles | L2 fts no titles |
|---|---|---|
| prefix (76) | 0.581 | 0.509 |
| paste (120) | 0.975 | 0.975 |
| all (196) | 0.822 | 0.794 |

## L3 fusion, qwen vector leg (Δ against w1)

Legs: production title block and FTS leg, exact vector leg with production window rule.

| shape (n) | L3 qwen w1 | L3 qwen w0.5 | L3 qwen w2 | L3 qwen shape | L3 qwen blend.4 |
|---|---|---|---|---|---|
| prefix (76) | 0.663 | 0.670 (+0.008 [-0.016, +0.034]) | 0.690 (+0.028 [-0.000, +0.061]) | 0.690 (+0.028 [-0.000, +0.061]) | 0.632 (-0.031 [-0.072, +0.009]) |
| paste (120) | 0.764 | 0.804 (+0.040* [+0.017, +0.066]) | 0.675 (-0.088* [-0.119, -0.060]) | 0.675 (-0.088* [-0.119, -0.060]) | 0.954 (+0.190* [+0.139, +0.243]) |
| all (196) | 0.725 | 0.752 (+0.028* [+0.010, +0.046]) | 0.681 (-0.043* [-0.064, -0.024]) | 0.681 (-0.043* [-0.064, -0.024]) | 0.829 (+0.105* [+0.068, +0.142]) |

## L3 fusion, eg2 vector leg (Δ against w1)

Legs: production title block and FTS leg, exact vector leg with production window rule.

| shape (n) | L3 eg2 w1 | L3 eg2 w0.5 | L3 eg2 w2 | L3 eg2 shape | L3 eg2 blend.4 |
|---|---|---|---|---|---|
| prefix (76) | 0.648 | 0.656 (+0.008 [-0.028, +0.046]) | 0.659 (+0.010 [-0.001, +0.027]) | 0.659 (+0.010 [-0.001, +0.027]) | 0.652 (+0.003 [-0.037, +0.040]) |
| paste (120) | 0.803 | 0.835 (+0.031* [+0.009, +0.057]) | 0.740 (-0.063* [-0.091, -0.038]) | 0.740 (-0.063* [-0.091, -0.038]) | 0.956 (+0.152* [+0.100, +0.207]) |
| all (196) | 0.743 | 0.765 (+0.022* [+0.002, +0.043]) | 0.709 (-0.035* [-0.054, -0.017]) | 0.709 (-0.035* [-0.054, -0.017]) | 0.838 (+0.095* [+0.060, +0.133]) |

## L3 Qwen3 rules on the tune split

The shape rule and α=0.4 were picked on the first 177 tune queries; the held-out split has new targets.

| shape (n) | L3 qwen w1 | L3 qwen w2 | L3 qwen shape | L3 qwen blend.4 |
|---|---|---|---|---|
| prefix (34) | 0.642 | 0.642 (+0.000 [-0.044, +0.044]) | 0.642 (+0.000 [-0.044, +0.044]) | 0.642 (-0.000 [-0.050, +0.057]) |
| paste (59) | 0.835 | 0.737 (-0.098* [-0.147, -0.053]) | 0.737 (-0.098* [-0.147, -0.053]) | 0.963 (+0.128* [+0.073, +0.193]) |
| all (93) | 0.765 | 0.702 (-0.062* [-0.097, -0.033]) | 0.702 (-0.062* [-0.097, -0.033]) | 0.846 (+0.081* [+0.041, +0.126]) |

## L3 Qwen3 rules on the heldout split

The shape rule and α=0.4 were picked on the first 177 tune queries; the held-out split has new targets.

| shape (n) | L3 qwen w1 | L3 qwen w2 | L3 qwen shape | L3 qwen blend.4 |
|---|---|---|---|---|
| prefix (42) | 0.679 | 0.729 (+0.050* [+0.012, +0.098]) | 0.729 (+0.050* [+0.012, +0.098]) | 0.624 (-0.055* [-0.115, -0.007]) |
| paste (61) | 0.695 | 0.616 (-0.079* [-0.115, -0.048]) | 0.616 (-0.079* [-0.115, -0.048]) | 0.945 (+0.250* [+0.169, +0.337]) |
| all (103) | 0.689 | 0.662 (-0.027* [-0.052, -0.000]) | 0.662 (-0.027* [-0.052, -0.000]) | 0.814 (+0.126* [+0.068, +0.185]) |

## Embedder inside fusion: EG2 − Qwen3 at the same rule

| shape (n) | L3 qwen w1 | L3 eg2 w1 |
|---|---|---|
| prefix (76) | 0.663 | 0.648 (-0.014 [-0.066, +0.035]) |
| paste (120) | 0.764 | 0.803 (+0.039 [-0.006, +0.085]) |
| all (196) | 0.725 | 0.743 (+0.018 [-0.016, +0.053]) |

## Embedder inside fusion at w2

| shape (n) | L3 qwen w2 | L3 eg2 w2 |
|---|---|---|
| prefix (76) | 0.690 | 0.659 (-0.031 [-0.084, +0.015]) |
| paste (120) | 0.675 | 0.740 (+0.065* [+0.002, +0.128]) |
| all (196) | 0.681 | 0.709 (+0.027 [-0.015, +0.069]) |

## Oracle bounds and which leg wins

| shape | Qwen3 w1 | best rule per query (Qwen3) | FTS-only or vector-only, better per query | FTS better | vector better | tie |
|---|---|---|---|---|---|---|
| 1 word (0) | nan | nan | nan | 0 | 0 | 0 |
| id-like (0) | nan | nan | nan | 0 | 0 | 0 |
| name (0) | nan | nan | nan | 0 | 0 | 0 |
| typo (0) | nan | nan | nan | 0 | 0 | 0 |
| prefix (76) | 0.663 | 0.731 | 0.704 | 7 | 22 | 47 |
| 2-3 words (0) | nan | nan | nan | 0 | 0 | 0 |
| 4-5 words (0) | nan | nan | nan | 0 | 0 | 0 |
| question (0) | nan | nan | nan | 0 | 0 | 0 |
| paste (120) | 0.764 | 0.954 | 0.979 | 61 | 1 | 58 |
| all (196) | 0.725 | 0.867 | 0.872 | 68 | 23 | 105 |

## FTS vs vector by query-target word overlap (Qwen3)

| overlap | n | FTS+titles | vector+titles | w1 | w2 |
|---|---|---|---|---|---|
| <1 | 72 | 0.572 | 0.672 | 0.669 | 0.699 |
| 1 (all words) | 124 | 0.968 | 0.577 | 0.757 | 0.671 |

