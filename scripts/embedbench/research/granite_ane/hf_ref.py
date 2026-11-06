# hf_ref.py <corpus.jsonl> <n> <out.docs.f32> [hf dir]: HF fp32 reference (sentence-transformers, CLS, no prompt), TestEmbedBench dump format.
import json, sys, struct, numpy as np
from sentence_transformers import SentenceTransformer
corpus, n, out = sys.argv[1], int(sys.argv[2]), sys.argv[3]
texts = [json.loads(l)["text"] for l in open(corpus)][:n]
m = SentenceTransformer(sys.argv[4] if len(sys.argv) > 4 else "hf", device="cpu")
m.max_seq_length = 2048
v = m.encode(texts, batch_size=8, normalize_embeddings=True, convert_to_numpy=True).astype("<f4")
with open(out, "wb") as f:
    f.write(struct.pack("<II", *v.shape)); f.write(v.tobytes())
print(v.shape)
