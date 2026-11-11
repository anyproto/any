# Learned query router (Qwen3 production legs)

Features per query: cov, fgap, nfts, vtop, vgap, agree, qlen, titles (cov = FTS coverage; fgap = (bm25 top1 − top2)/top1; vtop = top cosine; vgap = top1 − top5 cosine; agree = Jaccard of the legs' top-5 records). Actions: fts only, vector only, w0.5, w1, w2, blend.3, blend.7.
Greedy tree, leaves pick the action with the best equal-weight mean over M0 (MRR), AirM (article MRR) and the 9 public sets (nDCG@10); trained on tune halves (1896 queries), scored on held-out halves (1920). Held-out = new targets for M0/AirM, a hash half of queries for public.

Learned depth 1: cov < 1.0 → blend.7, else → blend.3.
Learned depth 2: left (cov < 1.0): vgap < 0.0714 → w2, else blend.7; right: qlen < 2.0 → w0.5, else blend.3.

| rule | M0 held-out | Δ vs w1 | AirM held-out | Δ vs w1 | public held-out | Δ vs w1 | train objective |
|---|---|---|---|---|---|---|---|
| w1 | 0.662 |  | 0.645 |  | 0.602 |  | 0.644 |
| w2 | 0.611 | -0.051* | 0.592 | -0.054* | 0.615 | +0.014* | 0.608 |
| best single action | 0.690 | +0.028* | 0.685 | +0.040* | 0.565 | -0.036* | 0.644 |
| F2 two-branch | 0.709 | +0.047* | 0.682 | +0.037* | 0.617 | +0.015* | 0.669 |
| F2 lo0.5 | 0.717 | +0.054* | 0.681 | +0.036* | 0.630 | +0.029* | 0.668 |
| learned depth 1 | 0.712 | +0.050* | 0.669 | +0.024* | 0.657 | +0.056* | 0.678 |
| learned depth 2 | 0.721 | +0.059* | 0.680 | +0.034* | 0.647 | +0.046* | 0.686 |

## Against F2 two-branch (held-out; * = 95% CI excludes 0)

| rule | M0 | AirM | public |
|---|---|---|---|
| learned depth 1 | +0.003 | -0.013* | +0.041* |
| learned depth 2 | +0.012* | -0.003 | +0.031* |
| two blends (cov=1 → α0.3, else α0.7) | +0.003 | -0.013* | +0.041* |
| F2 lo0.5 | +0.008* | -0.001 | +0.014* |

| per-query oracle | 0.769 | | 0.738 | | 0.722 | | |

## Which action wins per query (oracle ties broken toward w1), by coverage bucket, held-out + train

- M0 full (497): w1 (tie) 364, fts only 97, vector only 26, w0.5 5, w2 4
- M0 partial (236): w1 (tie) 178, vector only 28, fts only 16, w0.5 9, w2 4
- M0 <0.5 (142): w1 (tie) 132, vector only 10
- AirM full (469): w1 (tie) 358, fts only 102, vector only 4, w0.5 2, blend.3 2
- AirM partial (272): w1 (tie) 201, fts only 40, vector only 21, blend.3 4, w2 3
- AirM <0.5 (136): w1 (tie) 133, vector only 2, fts only 1
- public full (272): w1 (tie) 137, vector only 56, fts only 39, w2 14, w0.5 10
- public partial (1302): w1 (tie) 602, vector only 466, fts only 79, w2 52, blend.7 51
- public <0.5 (490): w1 (tie) 289, vector only 161, w2 14, fts only 10, w0.5 8

