"""Shared retrieval eval helpers (exact cosine over float32 rows)."""
import json
import numpy as np


def load_set(d):
    docs = [json.loads(l) for l in open(f"{d}/docs.jsonl")]
    qs = [json.loads(l) for l in open(f"{d}/queries.jsonl")]
    qrels = json.load(open(f"{d}/qrels.json"))
    return docs, qs, qrels


def load_vecs(path, n, dim):
    v = np.fromfile(path, dtype=np.float32)
    assert v.size == n * dim, (path, v.size, n, dim)
    v = v.reshape(n, dim)
    return v / np.linalg.norm(v, axis=1, keepdims=True)


def ndcg10(ranked, rel, k=10):
    dcg = sum(1 / np.log2(i + 2) for i, d in enumerate(ranked[:k]) if d in rel)
    idcg = sum(1 / np.log2(i + 2) for i in range(min(len(rel), k)))
    return dcg / idcg


def per_query(scores, docs, qs, qrels, k=10):
    """scores: [nq, nd]. Returns dict with per-query ndcg, recall, rr arrays and top-k index matrix."""
    ids = np.array([d["id"] for d in docs])
    top = np.argpartition(-scores, k, axis=1)[:, :k]
    nd = np.zeros(len(qs)); rc = np.zeros(len(qs)); rr = np.zeros(len(qs))
    topi = np.zeros((len(qs), k), dtype=np.int64)
    for i, q in enumerate(qs):
        t = top[i][np.argsort(-scores[i, top[i]])]
        topi[i] = t
        ranked = ids[t].tolist()
        rel = set(qrels[q["id"]])
        nd[i] = ndcg10(ranked, rel, k)
        hits = [j for j, d in enumerate(ranked) if d in rel]
        rc[i] = len(hits) / len(rel)
        rr[i] = 1 / (hits[0] + 1) if hits else 0
    return {"ndcg": nd, "recall": rc, "rr": rr, "top": topi}


def summary(r):
    return f"nDCG@10 {r['ndcg'].mean():.4f}  R@10 {r['recall'].mean():.4f}  MRR {r['rr'].mean():.4f}"


def boot_delta(a, b, n=2000, seed=0):
    """paired bootstrap of mean(a-b): (delta, lo, hi) 95%"""
    rng = np.random.default_rng(seed)
    d = a - b
    m = d.mean()
    idx = rng.integers(0, len(d), size=(n, len(d)))
    bs = d[idx].mean(axis=1)
    return m, np.percentile(bs, 2.5), np.percentile(bs, 97.5)


def fmt_delta(a, b):
    m, lo, hi = boot_delta(a, b)
    star = "*" if lo > 0 or hi < 0 else ""
    return f"{m:+.4f} [{lo:+.4f}, {hi:+.4f}]{star}"
