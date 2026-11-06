"""mlx_qwen3_bench.py <hf dir> <corpus.jsonl> <out-prefix> [--dtype bf16|fp16] [--bits 0|8|4] [--max-docs N]

Qwen3-Embedding-0.6B through MLX on the GPU, as a stand-in for llama.cpp's
Metal path: the HF weights loaded into mlx_lm's Qwen3 decoder, last-token
pooling over the final norm, L2-normalized, one doc per call with no
padding (MLX takes any length, so there are no buckets). Same prompts and
dumps as the other benches: docs bare, queries with the Instruct prefix,
<|endoftext|> appended by the tokenizer, texts cut to 2048 tokens keeping
it terminal. --bits quantizes the linear layers and the embedding in
memory (group size 64). The buffer cache is cleared after every doc:
with ~600 distinct lengths in fiqa-1500, MLX's default cache keeps a
buffer set per length and the run slows ~3x (p95 1.3 s against 0.27 s).
"""
import argparse
import json
import os
import resource
import struct
import time

import mlx.core as mx
import mlx.nn as nn
import numpy as np
from mlx_lm.models import qwen3
from tokenizers import Tokenizer

EOT = 151643
QRY = "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:"
# Same queries and helpers as coreml_bench.py (not imported: it needs coremltools).
QUERIES = [
    "bird strike forced landing on a river",
    "which console had the best selling handheld",
    "how does the indexer handle deleted records",
    "почему синхронизация тормозит после логина",
    "push notification keys for the receiver",
    "engine failure after takeoff caused by maintenance error",
    "memory leak in subscription snapshots",
    "как настроить локальный эмбеддер на GPU",
    "release process for the sdk tags",
    "midair collision over Europe",
    "what did we decide about chat read markers",
    "Sega cartridge based 16-bit system",
    "ошибка при открытии базы данных",
    "vector index recall and IVF probes",
    "pilot error in bad weather approach",
    "the type and collection object model",
]


def pct(xs):
    s = sorted(xs)
    at = lambda p: s[min(len(s) - 1, int(p * len(s)))]  # noqa: E731 (same rule as Go pct)
    return {"min": s[0], "p50": at(.5), "p90": at(.9), "p95": at(.95), "p99": at(.99),
            "max": s[-1], "mean": sum(s) / len(s)}


def write_vecs(path, vecs):
    v = np.asarray(vecs, dtype="<f4")
    with open(path, "wb") as f:
        f.write(struct.pack("<II", v.shape[0], v.shape[1]))
        f.write(v.tobytes())


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("hf")
    ap.add_argument("corpus")
    ap.add_argument("out")
    ap.add_argument("--dtype", default="bf16")
    ap.add_argument("--bits", type=int, default=0)
    ap.add_argument("--max-docs", type=int, default=0)
    ap.add_argument("--ctx", type=int, default=2048)
    ap.add_argument("--keep-cache", action="store_true")
    a = ap.parse_args()

    cfg = json.load(open(os.path.join(a.hf, "config.json")))
    model = qwen3.Model(qwen3.ModelArgs.from_dict(cfg))
    w = mx.load(os.path.join(a.hf, "model.safetensors"))
    dt = {"bf16": mx.bfloat16, "fp16": mx.float16}[a.dtype]
    model.load_weights([("model." + k, v.astype(dt)) for k, v in w.items()], strict=False)
    if a.bits:
        nn.quantize(model, group_size=64, bits=a.bits)
    model.eval()
    mx.eval(model.parameters())
    body = model.model

    tok = Tokenizer.from_file(os.path.join(a.hf, "tokenizer.json"))
    texts = [json.loads(l)["text"] for l in open(a.corpus)]
    if a.max_docs:
        texts = texts[: a.max_docs]

    truncated = []

    def ids_of(text, idx=None):
        ids = tok.encode(text, add_special_tokens=True).ids
        if ids[-1] != EOT:
            ids.append(EOT)
        if len(ids) > a.ctx:
            if idx is not None:
                truncated.append(idx)
            ids = ids[: a.ctx - 1] + [EOT]
        return ids

    def embed(ids):
        h = body(mx.array([ids]))[0, -1].astype(mx.float32)
        h = h / mx.linalg.norm(h)
        mx.eval(h)
        v = np.array(h)
        if not a.keep_cache:
            mx.clear_cache()
        return v

    for t in texts[:8]:
        embed(ids_of(t))
    embed(ids_of(QRY + "warm up"))

    docs, lat, ntoks = [], [], []
    start = time.time()
    for i, t in enumerate(texts):
        t1 = time.perf_counter()
        ids = ids_of(t, i)
        docs.append(embed(ids))
        lat.append((time.perf_counter() - t1) * 1000)
        ntoks.append(len(ids))
    wall = time.time() - start

    qlat, qv = [], []
    for r in range(3):
        for q in QUERIES:
            t1 = time.perf_counter()
            v = embed(ids_of(QRY + q))
            qlat.append((time.perf_counter() - t1) * 1000)
            if r == 0:
                qv.append(v)

    res = {
        "engine": "mlx " + mx.__version__, "dtype": a.dtype, "bits": a.bits, "docs": len(texts),
        "docWallSec": wall, "docsPerSec": len(texts) / wall, "docLatMs": pct(lat),
        "queryLatMs": pct(qlat), "tokensPerDoc": pct(ntoks), "truncatedDocs": len(truncated),
        "peakRSSMB": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1048576,
        "mlxPeakMemMB": mx.get_peak_memory() / 1048576,
        "vectorsWithNaN": sum(1 for v in docs + qv if not np.isfinite(v).all()),
    }
    write_vecs(a.out + ".docs.f32", docs)
    write_vecs(a.out + ".queries.f32", qv)
    open(a.out + ".truncated", "w").write("\n".join(map(str, truncated)))
    out = json.dumps(res, indent=2)
    open(a.out + ".json", "w").write(out)
    print("RESULT", out)


if __name__ == "__main__":
    main()
