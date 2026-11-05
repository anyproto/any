"""centerx.py <set dir> <docs.f32> <queries.f32> <dim> <label> [mu-source.f32 ...]

How robust is mean-vector centering?
  - mu from all docs (oracle for the index), from the first 1000 / 200 docs,
    from a random 100, and from each extra .f32 file given (another corpus,
    same model) — applied to docs and queries, renormalized
  - mu from the queries only
  - centering at 256 Matryoshka dims (truncate -> center -> renormalize)
  - partial centering: D - a*mu for a in 0.5, 0.75, 1, 1.25
"""
import sys
import numpy as np
from evallib import load_set, load_vecs, per_query, summary, fmt_delta

d, dp, qp, dim, label = sys.argv[1:6]
dim = int(dim)
extra = sys.argv[6:]
docs, qs, qrels = load_set(d)
D = load_vecs(dp, len(docs), dim)
Q = load_vecs(qp, len(qs), dim)
base = per_query(Q @ D.T, docs, qs, qrels)
print(f"{label} raw: {summary(base)}")


def ev(mu, name, a=1.0, dims=None):
    Dd, Qq = D, Q
    m = mu
    if dims:
        Dd, Qq, m = D[:, :dims], Q[:, :dims], mu[:dims]
        Dd = Dd / np.linalg.norm(Dd, axis=1, keepdims=True)
        Qq = Qq / np.linalg.norm(Qq, axis=1, keepdims=True)
    Dc = Dd - a * m; Dc /= np.linalg.norm(Dc, axis=1, keepdims=True)
    Qc = Qq - a * m; Qc /= np.linalg.norm(Qc, axis=1, keepdims=True)
    r = per_query(Qc @ Dc.T, docs, qs, qrels)
    ref = base
    if dims:
        D2 = Dd / np.linalg.norm(Dd, axis=1, keepdims=True); Q2 = Qq / np.linalg.norm(Qq, axis=1, keepdims=True)
        ref = per_query(Q2 @ D2.T, docs, qs, qrels)
        print(f"  {'raw @'+str(dims):34s} {summary(ref)}")
    print(f"  {name:34s} {summary(r)}   dNDCG {fmt_delta(r['ndcg'], ref['ndcg'])}")


mu = D.mean(0)
print(f"  |mu| = {np.linalg.norm(mu):.3f} (mean cos(doc, mu-hat) = {np.mean(D @ (mu/np.linalg.norm(mu))):.3f})")
ev(mu, "mu = all docs")
ev(D[:1000].mean(0), "mu = first 1000 docs")
ev(D[:200].mean(0), "mu = first 200 docs")
rng = np.random.default_rng(1)
ev(D[rng.choice(len(D), 100, replace=False)].mean(0), "mu = random 100 docs")
ev(Q.mean(0), "mu = queries")
for p in extra:
    X = np.fromfile(p, dtype=np.float32).reshape(-1, dim)
    X /= np.linalg.norm(X, axis=1, keepdims=True)
    m2 = X.mean(0)
    print(f"  cos(mu, mu_{p.split('/')[-1]}) = {float(mu @ m2 / np.linalg.norm(mu) / np.linalg.norm(m2)):.3f}")
    ev(m2, f"mu = {p.split('/')[-2]}/{p.split('/')[-1]}")
for a in (0.5, 0.75, 1.25):
    ev(mu, f"mu = all docs, a={a}", a=a)
ev(mu, "mu = all docs @256", dims=256)
