"""coreml_bench.py <model.mlmodelc|.mlpackage> <tokenizer.json> <corpus.jsonl> <out-prefix>
   [--units CPU_AND_NE] [--buckets 512] [--function-fmt ""] [--max-docs N]
   [--batch N] [--input input_ids] [--output embedding]
   [--mask-input NAME --mask-dtype float32]
   [--doc-prefix "title: none | text: "] [--query-prefix "task: search result | query: "]

Embeds the corpus one doc per predict (plus a batched pass) with the
same prompts as TestEmbedBench (EG2's by default; pass empty prefixes for
models without prompts, e.g. Granite), dumps .docs.f32/.queries.f32 in the
bench format, records truncated doc indices, latency percentiles and
docs/s, and prints a RESULT json. With several --buckets each text goes
to the smallest fixed-length function that holds it (function name =
--function-fmt % L, e.g. "s%d"); longer texts are truncated to the
largest bucket, keeping EOS terminal as embed_local.go does.
"""
import argparse
import collections
import json
import os
import resource
import struct
import tempfile
import time

import coremltools as ct
import numpy as np
from tokenizers import Tokenizer

DOC = "title: none | text: "
QRY = "task: search result | query: "
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
    ap.add_argument("model")
    ap.add_argument("tokenizer")
    ap.add_argument("corpus")
    ap.add_argument("out")
    ap.add_argument("--units", default="CPU_AND_NE")
    ap.add_argument("--buckets", default="512")
    ap.add_argument("--function-fmt", default="")
    ap.add_argument("--max-docs", type=int, default=0)
    ap.add_argument("--batch", type=int, default=8)
    ap.add_argument("--input", default="input_ids")
    ap.add_argument("--output", default="embedding")
    ap.add_argument("--mask-input", default="")
    ap.add_argument("--mask-dtype", default="float32")
    ap.add_argument("--pad-id", type=int, default=0)
    ap.add_argument("--skip-queries", action="store_true")
    ap.add_argument("--doc-prefix", default=DOC)
    ap.add_argument("--query-prefix", default=QRY)
    a = ap.parse_args()

    tok = Tokenizer.from_file(a.tokenizer)
    eos = tok.token_to_id("<eos>")
    texts = [json.loads(l)["text"] for l in open(a.corpus)]
    if a.max_docs:
        texts = texts[: a.max_docs]
    buckets = sorted(int(x) for x in a.buckets.split(","))
    maxlen = buckets[-1]
    units = getattr(ct.ComputeUnit, a.units)

    t0 = time.time()
    path = a.model.rstrip("/")
    compile_sec = 0.0
    if not path.endswith(".mlmodelc"):
        cache = os.path.join(tempfile.gettempdir(), "coreml-bench-" + os.path.basename(path) + ".mlmodelc")
        if not os.path.exists(cache):
            tc = time.time()
            ct.utils.compile_model(path, cache)
            compile_sec = time.time() - tc
        path = cache
    models = {}
    for L in buckets:
        kw = {"function_name": a.function_fmt % L} if a.function_fmt else {}
        models[L] = ct.models.CompiledMLModel(path, compute_units=units, **kw)
    load = time.time() - t0

    truncated = []

    def feed(text, idx=None):
        ids = tok.encode(text, add_special_tokens=True).ids
        if len(ids) > maxlen:
            if idx is not None:
                truncated.append(idx)
            ids = ids[: maxlen - 1] + ([eos] if ids[-1] == eos else [ids[maxlen - 1]])
        L = next(b for b in buckets if b >= len(ids))
        x = np.full((1, L), a.pad_id, dtype=np.int32)
        x[0, : len(ids)] = ids
        d = {a.input: x}
        if a.mask_input:
            mk = np.zeros((1, L), dtype=getattr(np, a.mask_dtype))
            mk[0, : len(ids)] = 1
            d[a.mask_input] = mk
        return L, d, len(ids)

    def emb(out):
        return np.asarray(out[a.output], dtype=np.float32).reshape(-1)

    def predict(L, d):
        return emb(models[L].predict(d))

    # Warm-up: first predict per function loads it onto the device.
    first = {}
    warm = tok.encode(a.doc_prefix + texts[0], add_special_tokens=True).ids[:16]
    for L in buckets:
        x = {a.input: np.full((1, L), a.pad_id, dtype=np.int32)}
        x[a.input][0, : len(warm)] = warm
        if a.mask_input:
            x[a.mask_input] = np.zeros((1, L), dtype=getattr(np, a.mask_dtype))
            x[a.mask_input][0, : len(warm)] = 1
        t1 = time.time()
        models[L].predict(x)
        first[L] = time.time() - t1
    L, d, _ = feed(a.query_prefix + "warm up")
    predict(L, d)

    # One doc per call: tokenize + predict, the production shape.
    docs, lat, plat, ntoks = [], [], [], []
    used = collections.Counter()
    start = time.time()
    for i, t in enumerate(texts):
        t1 = time.perf_counter()
        L, d, n = feed(a.doc_prefix + t, i)
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
                L, d, _ = feed(a.query_prefix + q)
                v = predict(L, d)
                qlat.append((time.perf_counter() - t1) * 1000)
                if r == 0:
                    qv.append(v)

    # Batched pass: predict(list) -> MLBatchProvider, grouped per bucket.
    bres = None
    if a.batch > 1:
        groups = collections.defaultdict(list)
        for i, t in enumerate(texts):
            L, d, _ = feed(a.doc_prefix + t)
            groups[L].append((i, d))
        bdocs = {}
        bstart = time.time()
        for L, items in groups.items():
            for j in range(0, len(items), a.batch):
                chunk = items[j : j + a.batch]
                outs = models[L].predict([d for _, d in chunk])
                for (i, _), o in zip(chunk, outs):
                    bdocs[i] = emb(o)
        bwall = time.time() - bstart
        bd = np.stack([bdocs[i] for i in range(len(texts))])
        dd = np.stack(docs)
        agree = float(((bd * dd).sum(1) / (np.linalg.norm(bd, axis=1) * np.linalg.norm(dd, axis=1))).min())
        bres = {"batch": a.batch, "wallSec": bwall, "docsPerSec": len(texts) / bwall,
                "predictOnly": True, "minCosVsSingle": agree}

    nan = sum(1 for v in docs + qv if not np.isfinite(v).all())
    res = {
        "model": a.model, "computeUnits": a.units, "buckets": buckets, "bucketUse": dict(used),
        "compileSec": compile_sec, "loadSec": load, "firstPredictSec": first,
        "docs": len(texts), "dim": int(docs[0].shape[0]),
        "docWallSec": wall, "docsPerSec": len(texts) / wall,
        "docLatMs": pct(lat), "docPredictLatMs": pct(plat),
        "queryLatMs": pct(qlat) if qlat else None,
        "tokensPerDoc": pct(ntoks), "truncatedDocs": len(truncated),
        "peakRSSMB": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1048576,
        "vectorsWithNaN": nan, "batched": bres,
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
