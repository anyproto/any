"""fusion.py <set dir> <docs.f32> <queries.f32> <dim> <label> [--center]

Production-shaped hybrid: each leg returns its top `fetch` = min(max(30,10*3),100)
= 30 hits, fused by weighted RRF (k=60), and the adaptive variant that
scales the BM25 weight by legConfidence (1 - mean(rest)/top over the
fetched BM25 scores). Reports vector-only, BM25-only, and RRF at several
vector:fts weights. --center applies mean-doc-vector centering to the
dense leg first."""
import sys
import numpy as np
from evallib import load_set, load_vecs, per_query, summary, fmt_delta

d, dp, qp, dim, label = sys.argv[1:6]
dim = int(dim)
center = "--center" in sys.argv
docs, qs, qrels = load_set(d)
D = load_vecs(dp, len(docs), dim)
Q = load_vecs(qp, len(qs), dim)
if center:
    mu = D.mean(0)
    D = D - mu; D /= np.linalg.norm(D, axis=1, keepdims=True)
    Q = Q - mu; Q /= np.linalg.norm(Q, axis=1, keepdims=True)
S = Q @ D.T
B = np.load(f"{d}/bm25.npy")
FETCH, K = 30, 60


def toplist(M, n):
    idx = np.argpartition(-M, n, axis=1)[:, :n]
    return np.take_along_axis(idx, np.argsort(-np.take_along_axis(M, idx, 1), 1), 1)


tv, tb = toplist(S, FETCH), toplist(B, FETCH)


def conf(i):
    sc = B[i, tb[i]]
    if sc[0] <= 0:
        return 0.0
    return float(np.clip(1 - sc[1:].mean() / sc[0], 0, 1))


def rrf(wv, wf, adaptive=False):
    F = np.zeros_like(S)
    for i in range(len(qs)):
        wfi = wf * conf(i) if adaptive else wf
        F[i, tv[i]] += wv / (K + np.arange(FETCH) + 1)
        F[i, tb[i]] += wfi / (K + np.arange(FETCH) + 1)
    return F


base = per_query(S, docs, qs, qrels)
print(f"{label}{' centered' if center else ''}: vector-only {summary(base)}")
print(f"  bm25-only        {summary(per_query(B, docs, qs, qrels))}")
for wv, wf, ad in [(1, 1, False), (1.5, 1, False), (2, 1, False), (3, 1, False), (1, 1, True), (2, 1, True)]:
    r = per_query(rrf(wv, wf, ad), docs, qs, qrels)
    print(f"  rrf vec {wv}: fts {wf}{' adaptive' if ad else ''}  {summary(r)}   vs vector {fmt_delta(r['ndcg'], base['ndcg'])}")
