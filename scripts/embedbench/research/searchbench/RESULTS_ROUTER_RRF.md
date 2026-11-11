# Learned query router, RRF actions only (Qwen3 production legs)

Features per query: cov, fgap, nfts, vtop, vgap, agree, qlen, titles (cov = FTS coverage; fgap = (bm25 top1 − top2)/top1; vtop = top cosine; vgap = top1 − top5 cosine; agree = Jaccard of the legs' top-5 records). Actions: fts only, vector only, w0.25, w0.5, w1, w2, w3, w0.5 k20, w2 k20, blend.3, blend.7.
Greedy tree, leaves pick the action with the best equal-weight mean over M0 (MRR), AirM (article MRR) and the 9 public sets (nDCG@10); trained on tune halves (1896 queries), scored on held-out halves (1920). Held-out = new targets for M0/AirM, a hash half of queries for public.

Learned depth 1: cov < 0.8 → w2 k20, else → w0.5 k20.
Learned depth 2: left (cov < 0.8): vgap < 0.1031 → w2 k20, else vector only; right: fgap < 0.1838 → w0.5 k20, else fts only.

| rule | M0 held-out | Δ vs w1 | AirM held-out | Δ vs w1 | public held-out | Δ vs w1 | train objective |
|---|---|---|---|---|---|---|---|
| w1 | 0.662 |  | 0.645 |  | 0.602 |  | 0.644 |
| w2 | 0.611 | -0.051* | 0.592 | -0.054* | 0.615 | +0.014* | 0.608 |
| best single action | 0.662 | +0.000 | 0.645 | +0.000 | 0.602 | +0.000 | 0.644 |
| F2 two-branch | 0.709 | +0.047* | 0.682 | +0.037* | 0.617 | +0.015* | 0.669 |
| F2 lo0.5 | 0.717 | +0.054* | 0.681 | +0.036* | 0.630 | +0.029* | 0.668 |
| learned depth 1 (RRF only) | 0.689 | +0.027* | 0.664 | +0.018* | 0.622 | +0.021* | 0.672 |
| learned depth 2 (RRF only) | 0.711 | +0.048* | 0.675 | +0.030* | 0.638 | +0.036* | 0.685 |
| published depth 2 (blends) | 0.721 | +0.059* | 0.680 | +0.034* | 0.647 | +0.046* | 0.686 |

## Against F2 two-branch (held-out; * = 95% CI excludes 0)

| rule | M0 | AirM | public |
|---|---|---|---|
| learned depth 1 (RRF only) | -0.020* | -0.018* | +0.006* |
| learned depth 2 (RRF only) | +0.002 | -0.007 | +0.022* |
| published depth 2 (blends) | +0.012* | -0.003 | +0.031* |

| per-query oracle (RRF actions) | 0.770 | | 0.740 | | 0.721 | | |

