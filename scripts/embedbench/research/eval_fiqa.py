"""eval_fiqa.py <set dir> <docs.f32> <queries.f32> <dim> [label]

Exact-cosine retrieval + length diagnostics for one model run:
  - nDCG@10 / R@10 / MRR
  - length distribution of top-10 hits vs relevant docs (chars)
  - mean cosine of each query to ALL docs, bucketed by doc length
    (a short-doc bias shows as higher mean similarity for short docs)
  - per-query ndcg saved to <queries.f32>.ndcg.npy for paired tests
"""
import sys
import numpy as np
from evallib import load_set, load_vecs, per_query, summary

d, dp, qp, dim = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
label = sys.argv[5] if len(sys.argv) > 5 else dp
docs, qs, qrels = load_set(d)
D = load_vecs(dp, len(docs), dim)
Q = load_vecs(qp, len(qs), dim)
S = Q @ D.T
r = per_query(S, docs, qs, qrels)
print(f"{label}: {summary(r)}  (n={len(qs)} queries, {len(docs)} docs)")
np.save(qp + ".ndcg.npy", r["ndcg"])

L = np.array([len(x["text"]) for x in docs])
hitL = L[r["top"]].ravel()
relL = np.array([L[i] for q in qs for i in np.where(np.isin([x["id"] for x in docs], qrels[q["id"]]))[0]])
print(f"  top-10 hit length: median {np.median(hitL):.0f}, <80ch {np.mean(hitL<80)*100:.1f}%, <200ch {np.mean(hitL<200)*100:.1f}%")
print(f"  relevant doc length: median {np.median(relL):.0f}, <80ch {np.mean(relL<80)*100:.1f}%, <200ch {np.mean(relL<200)*100:.1f}%")
print(f"  corpus length: median {np.median(L):.0f}, <80ch {np.mean(L<80)*100:.1f}%, <200ch {np.mean(L<200)*100:.1f}%")
edges = [0, 80, 200, 400, 800, 1600, 10**9]
print("  mean / p99 cosine (query x all docs) by doc length:")
for a, b in zip(edges, edges[1:]):
    m = (L >= a) & (L < b)
    if m.sum() == 0:
        continue
    sub = S[:, m]
    print(f"    [{a:>5},{b:>5}) n={m.sum():5d}  mean {sub.mean():.4f}  p99 {np.percentile(sub, 99):.4f}  max {sub.max():.4f}")
# how often a relevant doc is beaten by a shorter non-relevant doc in the top 10
