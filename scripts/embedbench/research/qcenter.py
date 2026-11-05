"""qcenter.py <set dir> <docs.f32> <queries.f32> <dim> <label> [--mu-docs N] [--a 0.25,0.5,1.0] [--seeds 5]

Fix #1: query-side centering, index untouched: q' = normalize(q - a*mu),
mu = the UNNORMALIZED mean of N random doc vectors from the index (L2-normalized
rows). Reports nDCG@10 / R@10 / MRR per strength a with a paired bootstrap
of the nDCG delta vs raw, averaged over several random mu draws (the spread
shows how much the choice of the N docs matters), plus the top-10 hit-length
share (<80 chars) and the cosine slope on log10(chars)."""
import argparse
import numpy as np
from evallib import load_set, load_vecs, per_query, summary, fmt_delta

ap = argparse.ArgumentParser()
ap.add_argument("set"); ap.add_argument("docs"); ap.add_argument("queries")
ap.add_argument("dim", type=int); ap.add_argument("label")
ap.add_argument("--mu-docs", type=int, default=100)
ap.add_argument("--a", default="0.25,0.5,1.0")
ap.add_argument("--seeds", type=int, default=5)
o = ap.parse_args()

docs, qs, qrels = load_set(o.set)
D = load_vecs(o.docs, len(docs), o.dim)
Q = load_vecs(o.queries, len(qs), o.dim)
L = np.array([len(x["text"]) for x in docs], dtype=np.float64)
lg = np.log10(np.maximum(L, 1))
X = np.stack([lg, np.ones_like(lg)], 1)


def stats(S, r):
    slope = np.linalg.lstsq(X, S.T, rcond=None)[0][0].mean()
    return f"hits<80ch {np.mean(L[r['top'].ravel()] < 80) * 100:4.1f}%  slope {slope:+.4f}/decade"


S0 = Q @ D.T
base = per_query(S0, docs, qs, qrels)
print(f"{o.label} raw: {summary(base)}  {stats(S0, base)}  (n={len(qs)} queries, {len(docs)} docs)")
for a in [float(x) for x in o.a.split(",")]:
    for seed in range(o.seeds):
        rng = np.random.default_rng(seed)
        mu = D[rng.choice(len(D), min(o.mu_docs, len(D)), replace=False)].mean(0)
        Qc = Q - a * mu
        Qc /= np.linalg.norm(Qc, axis=1, keepdims=True)
        S = Qc @ D.T
        r = per_query(S, docs, qs, qrels)
        print(f"  a={a:<4} mu=random {o.mu_docs} (seed {seed})  {summary(r)}  dNDCG {fmt_delta(r['ndcg'], base['ndcg'])}  {stats(S, r)}")
