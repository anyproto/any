"""lenbias.py <set dir> <docs.f32> <queries.f32> <dim> <label>

Length bias and label-free calibrations for one model:
  - slope of cosine on log10(chars) per query (mean over queries)
  - recall@10 of relevant docs by relevant-doc length bucket
  - calibrations (each re-evaluated, paired bootstrap vs raw):
      bucket   : subtract the per-length-bucket mean cosine (fit on all queries x docs, no labels)
      logfit   : subtract b*log10(len) with b = mean per-query slope
      center   : subtract the mean doc vector from docs and queries, renormalize
      hub      : subtract a*mean_q cos(q,d) per doc (query-side hubness), a in {0.5,1}
      dochub   : subtract a*mean_d' cos(d',d) per doc (doc-side centrality, no queries), a in {0.5,1}
"""
import sys
import numpy as np
from evallib import load_set, load_vecs, per_query, summary, fmt_delta

d, dp, qp, dim, label = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), sys.argv[5]
docs, qs, qrels = load_set(d)
D = load_vecs(dp, len(docs), dim)
Q = load_vecs(qp, len(qs), dim)
S = Q @ D.T
L = np.array([len(x["text"]) for x in docs], dtype=np.float64)
lg = np.log10(np.maximum(L, 1))
base = per_query(S, docs, qs, qrels)
print(f"{label} raw: {summary(base)}")

# slope of cos on log10(len), per query
X = np.stack([lg, np.ones_like(lg)], 1)
coef = np.linalg.lstsq(X, S.T, rcond=None)[0]  # [2, nq]
print(f"  cosine ~ log10(chars): mean slope {coef[0].mean():+.4f} per decade (sd over queries {coef[0].std():.4f})")

# recall by relevant-doc length
ids = {x["id"]: i for i, x in enumerate(docs)}
edges = [0, 200, 400, 800, 1600, 10**9]
top = base["top"]
print("  relevant-doc recall@10 by its length:")
for a, b in zip(edges, edges[1:]):
    hit = tot = 0
    for qi, q in enumerate(qs):
        for r in qrels[q["id"]]:
            i = ids[r]
            if a <= L[i] < b:
                tot += 1
                hit += i in top[qi]
    if tot:
        print(f"    [{a:>5},{b:>5}) n={tot:4d} recall {hit/tot:.3f}")


def ev(S2, name):
    r = per_query(S2, docs, qs, qrels)
    print(f"  {name:28s} {summary(r)}   dNDCG {fmt_delta(r['ndcg'], base['ndcg'])}")
    return r


# bucket calibration
cal = np.zeros(len(docs))
for a, b in zip(edges, edges[1:]):
    m = (L >= a) & (L < b)
    if m.any():
        cal[m] = S[:, m].mean()
ev(S - cal[None, :], "bucket mean")
b = coef[0].mean()
ev(S - b * lg[None, :], f"logfit b={b:+.3f}")
for f in (0.5, 1.5, 2.0):
    ev(S - f * b * lg[None, :], f"logfit x{f}")
mu = D.mean(0)
Dc = D - mu; Dc /= np.linalg.norm(Dc, axis=1, keepdims=True)
Qc = Q - mu; Qc /= np.linalg.norm(Qc, axis=1, keepdims=True)
ev(Qc @ Dc.T, "center (doc mean)")
hub = S.mean(0)
for a in (0.5, 1.0):
    ev(S - a * hub[None, :], f"hub x{a}")
rng = np.random.default_rng(0)
probe = D[rng.choice(len(D), size=min(2000, len(D)), replace=False)]
dhub = (probe @ D.T).mean(0)
for a in (0.5, 1.0):
    ev(S - a * dhub[None, :], f"dochub x{a}")
print(f"  corr(hub, log len) {np.corrcoef(hub, lg)[0,1]:+.3f}; corr(dochub, log len) {np.corrcoef(dhub, lg)[0,1]:+.3f}")
