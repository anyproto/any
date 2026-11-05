"""compare.py <set dir> <A.docs.f32> <A.q.f32> <dimA> <labelA> <B.docs.f32> <B.q.f32> <dimB> <labelB>

Paired comparison of two runs on the same set: nDCG/R/MRR, paired bootstrap
of the nDCG delta (A - B), wins/losses, and top-10 hit-length stats."""
import sys
import numpy as np
from evallib import load_set, load_vecs, per_query, summary, fmt_delta

d = sys.argv[1]
docs, qs, qrels = load_set(d)
L = np.array([len(x["text"]) for x in docs])
runs = []
for i in (2, 6):
    dp, qp, dim, label = sys.argv[i], sys.argv[i + 1], int(sys.argv[i + 2]), sys.argv[i + 3]
    D = load_vecs(dp, len(docs), dim); Q = load_vecs(qp, len(qs), dim)
    r = per_query(Q @ D.T, docs, qs, qrels)
    h = L[r["top"]].ravel()
    print(f"{label:28s} {summary(r)}  hit len median {np.median(h):.0f}, <80ch {np.mean(h<80)*100:.1f}%, <200ch {np.mean(h<200)*100:.1f}%")
    runs.append((label, r))
(la, a), (lb, b) = runs
w = int(np.sum(a["ndcg"] > b["ndcg"])); l = int(np.sum(a["ndcg"] < b["ndcg"]))
print(f"dNDCG ({la} - {lb}) {fmt_delta(a['ndcg'], b['ndcg'])}  wins {w} / losses {l} / ties {len(qs)-w-l}   dMRR {fmt_delta(a['rr'], b['rr'])}")
