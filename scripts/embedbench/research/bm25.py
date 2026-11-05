"""bm25.py <set dir> <out.npy>: BM25 scores [nq, nd] for a set (simple
unicode-word tokenizer, lowercase, CJK split per char), saved as float32.
A proxy for the production FTS leg (which is BM25F with stopwords)."""
import json, re, sys
import numpy as np
from rank_bm25 import BM25Okapi

d, out = sys.argv[1], sys.argv[2]
docs = [json.loads(l) for l in open(f"{d}/docs.jsonl")]
qs = [json.loads(l) for l in open(f"{d}/queries.jsonl")]
W = re.compile(r"[\w']+|[぀-ヿ一-鿿]", re.UNICODE)
STOP = set("a an the of to in on for and or is are was were be by with as at from that this it its what which who how why when where do does did not no can".split())


def tok(s):
    out = []
    for w in W.findall(s.lower()):
        if re.match(r"[぀-ヿ一-鿿]", w):
            out.append(w)
        elif w not in STOP:
            out.append(w)
    return out


bm = BM25Okapi([tok(x["text"]) for x in docs])
S = np.zeros((len(qs), len(docs)), dtype=np.float32)
for i, q in enumerate(qs):
    S[i] = bm.get_scores(tok(q["text"]))
np.save(out, S)
print("bm25", S.shape)
