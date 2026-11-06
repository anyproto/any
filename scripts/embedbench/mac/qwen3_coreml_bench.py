"""qwen3_coreml_bench.py <layout> <model path or format> <tokenizer.json> <corpus.jsonl> <out-prefix>
   [--units CPU_AND_NE] [--buckets 128,256,512,1024] [--max-docs N] [--skip-queries]

Benchmarks the published Core ML builds of Qwen3-Embedding-0.6B the way
coreml_bench.py benchmarks EG2 and Granite: one doc per predict, the
production prompts (documents bare, queries with the Instruct prefix), the
same .docs.f32/.queries.f32 dumps, latency percentiles and a RESULT json.

Layouts:
  sidekick  <dir> holding model_<L>.0.mlmodelc (embedding + layers 0-8,
            -> hidden_out) and model_<L>.1.mlmodelc (layers 9-27, norm,
            last-token pooling -> embedding), per sidekickd/qwen3-embedding-
            0.6b-coreml. Right-padded, int32 mask, output not normalized.
  single    a printf format naming one package per bucket, e.g.
            "erjigit/qwen3-b1_s%d-8bit.mlpackage" (neuradex's layout and its
            8-bit derivative). Left-padded with token 151643, int32 mask,
            output L2-normalized in the model.

Every text ends in <|endoftext|> (151643), as llama.cpp's GGUF appends it;
longer texts are truncated to the largest bucket keeping it terminal.
Vectors are L2-normalized in fp32 before they are dumped.
"""
import argparse
import collections
import json
import os
import resource
import sys
import tempfile
import time

import coremltools as ct
import numpy as np
from tokenizers import Tokenizer

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from coreml_bench import QUERIES, pct, write_vecs  # noqa: E402

EOT = 151643
QRY = "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:"


def compiled(path, units):
    path = path.rstrip("/")
    sec = 0.0
    if not path.endswith(".mlmodelc"):
        cache = os.path.join(tempfile.gettempdir(), "coreml-bench-" + os.path.basename(path) + ".mlmodelc")
        if not os.path.exists(cache):
            t = time.time()
            ct.utils.compile_model(path, cache)
            sec = time.time() - t
        path = cache
    return ct.models.CompiledMLModel(path, compute_units=units), sec


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("layout", choices=["sidekick", "single"])
    ap.add_argument("model")
    ap.add_argument("tokenizer")
    ap.add_argument("corpus")
    ap.add_argument("out")
    ap.add_argument("--units", default="CPU_AND_NE")
    ap.add_argument("--buckets", default="128,256,512,1024")
    ap.add_argument("--max-docs", type=int, default=0)
    ap.add_argument("--skip-queries", action="store_true")
    a = ap.parse_args()

    tok = Tokenizer.from_file(a.tokenizer)
    texts = [json.loads(l)["text"] for l in open(a.corpus)]
    if a.max_docs:
        texts = texts[: a.max_docs]
    buckets = sorted(int(x) for x in a.buckets.split(","))
    maxlen = buckets[-1]
    units = getattr(ct.ComputeUnit, a.units)
    left = a.layout == "single"

    t0 = time.time()
    compile_sec = 0.0
    stages = {}
    for L in buckets:
        if a.layout == "sidekick":
            m0, _ = compiled(os.path.join(a.model, f"model_{L}.0.mlmodelc"), units)
            m1, _ = compiled(os.path.join(a.model, f"model_{L}.1.mlmodelc"), units)
            stages[L] = (m0, m1)
        else:
            m, s = compiled(a.model % L, units)
            compile_sec += s
            stages[L] = (m,)
    load = time.time() - t0

    truncated = []

    def feed(text, idx=None):
        ids = tok.encode(text, add_special_tokens=False).ids + [EOT]
        if len(ids) > maxlen:
            if idx is not None:
                truncated.append(idx)
            ids = ids[: maxlen - 1] + [EOT]
        L = next(b for b in buckets if b >= len(ids))
        return L, pack(ids, L), len(ids)

    def pack(ids, L):
        x = np.full((1, L), EOT if left else 0, dtype=np.int32)
        mk = np.zeros((1, L), dtype=np.int32)
        if left:
            x[0, L - len(ids):] = ids
            mk[0, L - len(ids):] = 1
        else:
            x[0, : len(ids)] = ids
            mk[0, : len(ids)] = 1
        return {"input_ids": x, "attention_mask": mk}

    def predict(L, d):
        st = stages[L]
        if len(st) == 2:
            h = st[0].predict(d)["hidden_out"]
            out = st[1].predict({"hidden_in": h, "attention_mask": d["attention_mask"]})
        else:
            out = st[0].predict(d)
        v = np.asarray(out["embedding"], dtype=np.float32).reshape(-1)
        return v / np.linalg.norm(v)

    # Warm-up: the first predict per bucket loads its programs onto the device.
    first = {}
    warm = tok.encode(texts[0], add_special_tokens=False).ids[:15] + [EOT]
    for L in buckets:
        t1 = time.time()
        predict(L, pack(warm, L))
        first[L] = time.time() - t1
    L, d, _ = feed(QRY + "warm up")
    predict(L, d)

    docs, lat, plat, ntoks = [], [], [], []
    used = collections.Counter()
    start = time.time()
    for i, t in enumerate(texts):
        t1 = time.perf_counter()
        L, d, n = feed(t, i)
        t2 = time.perf_counter()
        docs.append(predict(L, d))
        t3 = time.perf_counter()
        lat.append((t3 - t1) * 1000)
        plat.append((t3 - t2) * 1000)
        ntoks.append(n)
        used[L] += 1
    wall = time.time() - start

    qlat, qv = [], []
    if not a.skip_queries:
        for r in range(3):
            for q in QUERIES:
                t1 = time.perf_counter()
                L, d, _ = feed(QRY + q)
                v = predict(L, d)
                qlat.append((time.perf_counter() - t1) * 1000)
                if r == 0:
                    qv.append(v)

    nan = sum(1 for v in docs + qv if not np.isfinite(v).all())
    res = {
        "layout": a.layout, "model": a.model, "computeUnits": a.units, "buckets": buckets,
        "bucketUse": dict(used), "compileSec": compile_sec, "loadSec": load, "firstPredictSec": first,
        "docs": len(texts), "dim": int(docs[0].shape[0]),
        "docWallSec": wall, "docsPerSec": len(texts) / wall,
        "docLatMs": pct(lat), "docPredictLatMs": pct(plat),
        "queryLatMs": pct(qlat) if qlat else None,
        "tokensPerDoc": pct(ntoks), "truncatedDocs": len(truncated),
        "peakRSSMB": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1048576,
        "vectorsWithNaN": nan,
    }
    write_vecs(a.out + ".docs.f32", docs)
    if qv:
        write_vecs(a.out + ".queries.f32", qv)
    open(a.out + ".truncated", "w").write("\n".join(map(str, truncated)))
    out = json.dumps(res, indent=2)
    open(a.out + ".json", "w").write(out)
    print("RESULT", out)


if __name__ == "__main__":
    main()
