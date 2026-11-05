"""wikieval.py <wiki set dir> <docs.f32> <queries.f32> <dim> <label> [--center] [--drop-ho] [--qrels-from DIR]

Topical-query proxy over editor-style windows: nDCG@10, share of top-10
hits that are heading-only windows, median hit length, per-language nDCG.
--center: mean-doc-vector centering. --drop-ho: remove heading-only windows
from the candidate set (what a chunker fix would do)."""
import sys, json
import numpy as np
from evallib import load_set, load_vecs, per_query, summary, fmt_delta

d, dp, qp, dim, label = sys.argv[1:6]
dim = int(dim)
docs, qs, qrels = load_set(d)
D = load_vecs(dp, len(docs), dim)
Q = load_vecs(qp, len(qs), dim)
if "--center" in sys.argv:
    mu = D.mean(0)
    D = D - mu; D /= np.linalg.norm(D, axis=1, keepdims=True)
    Q = Q - mu; Q /= np.linalg.norm(Q, axis=1, keepdims=True)
S = Q @ D.T
ho = np.array([x["heading_only"] for x in docs])
if "--drop-ho" in sys.argv:
    S = S.copy(); S[:, ho] = -9
r = per_query(S, docs, qs, qrels)
L = np.array([len(x["text"]) for x in docs])
hits = r["top"].ravel()
print(f"{label}: {summary(r)}  heading-only hits {ho[hits].mean()*100:.1f}%  hits<80ch {np.mean(L[hits]<80)*100:.1f}%  median hit len {np.median(L[hits]):.0f}  (n={len(qs)})")
langs = sorted(set(q["lang"] for q in qs))
cross = np.array([q["text"].isascii() and q["lang"] in ("ru", "ja", "zh") for q in qs])
for lg in langs:
    m = np.array([q["lang"] == lg for q in qs]) & ~cross
    print(f"   {lg}: nDCG {r['ndcg'][m].mean():.3f} (n={m.sum()}) ho-hits {ho[r['top'][m].ravel()].mean()*100:.0f}%", end="")
print(f"\n   cross-lang: nDCG {r['ndcg'][cross].mean():.3f} (n={cross.sum()})")
np.save(qp + f".{label.replace(' ','_')}.ndcg.npy", r["ndcg"])
