"""validate.py <model.mlmodelc> <hf dir> <corpus.jsonl> [--n 60] [--units CPU_AND_NE] [--buckets 128,256,512,1024] [--plan]
Core ML vectors vs the HF fp32 reference (unpadded, CLS, L2), per bucket;
with --plan, MLComputePlan placement (op count and estimated cost share per device) per function."""
import argparse
import collections
import json
import os

import coremltools as ct
import numpy as np
from tokenizers import Tokenizer

ap = argparse.ArgumentParser()
ap.add_argument("model")
ap.add_argument("hf")
ap.add_argument("corpus")
ap.add_argument("--n", type=int, default=60)
ap.add_argument("--units", default="CPU_AND_NE")
ap.add_argument("--buckets", default="128,256,512,1024")
ap.add_argument("--plan", action="store_true")
a = ap.parse_args()
B = [int(x) for x in a.buckets.split(",")]
units = getattr(ct.ComputeUnit, a.units)

tok = Tokenizer.from_file(f"{a.hf}/tokenizer.json")
tok.no_padding()
tok.no_truncation()
texts = [json.loads(l)["text"] for l in open(a.corpus)]
enc = [tok.encode(t).ids for t in texts]
# a spread of lengths, plus a few from each bucket so every function is hit
lens = np.array([len(e) for e in enc])
pick = []
lo = 0
per = max(1, a.n // len(B))
for L in B:
    idx = np.where((lens > lo) & (lens <= L))[0]
    if len(idx) == 0:  # no doc that long: use the longest docs, truncated to L
        idx = np.argsort(-lens)[:per]
    pick += list(idx[np.linspace(0, len(idx) - 1, min(per, len(idx))).astype(int)])
    lo = L
pick = sorted(set(int(i) for i in pick))

cache = os.path.join(os.path.dirname(os.path.abspath(__file__)), f"ref-{a.n}-{a.buckets}.npz")
if os.path.exists(cache):
    R = dict(np.load(cache))
else:
    import torch
    from transformers import AutoModel
    torch.set_grad_enabled(False)
    ref = AutoModel.from_pretrained(a.hf, dtype=torch.float32, attn_implementation="eager").eval()
    ref.config.reference_compile = False
    R = {}
    for i in pick:
        ids = enc[i][: B[-1]]
        r = ref(input_ids=torch.tensor([ids])).last_hidden_state[0, 0].numpy()
        R[str(i)] = r / np.linalg.norm(r)
    np.savez(cache, **R)

res = collections.defaultdict(list)
for L in B:
    m = ct.models.CompiledMLModel(a.model, compute_units=units, function_name=f"s{L}")
    for i in pick:
        ids = enc[i][: B[-1]]
        if next(b for b in B if b >= len(ids)) != L:
            continue
        x = np.zeros((1, L), np.int32)
        x[0, : len(ids)] = ids
        mk = np.zeros((1, L), np.float32)
        mk[0, : len(ids)] = 1
        v = np.asarray(m.predict({"input_ids": x, "attention_mask": mk})["embedding"], np.float32).reshape(-1)
        ok = np.isfinite(v).all()
        c = float(v @ R[str(i)] / np.linalg.norm(v)) if ok else float("nan")
        res[L].append(c)
    del m
allc = [c for v in res.values() for c in v]
for L in B:
    v = res[L]
    if v:
        print(f"s{L}: n={len(v)} min {np.nanmin(v):.5f} median {np.nanmedian(v):.5f} NaN {int(np.isnan(v).sum())}")
print(f"all: n={len(allc)} min {np.nanmin(allc):.5f} median {np.nanmedian(allc):.5f}")

if a.plan:
    from coremltools.models.compute_plan import MLComputePlan
    from coremltools.models.compute_device import MLCPUComputeDevice, MLGPUComputeDevice, MLNeuralEngineComputeDevice
    plan = MLComputePlan.load_from_path(path=a.model, compute_units=units)
    prog = plan.model_structure.program
    name = lambda d: ("ANE" if isinstance(d, MLNeuralEngineComputeDevice) else "GPU" if isinstance(d, MLGPUComputeDevice) else "CPU" if isinstance(d, MLCPUComputeDevice) else "?")  # noqa: E731
    for fn_name, fn in prog.functions.items():
        cnt, cost, offane = collections.Counter(), collections.Counter(), collections.Counter()
        for op in fn.block.operations:
            if op.operator_name in ("const",):
                continue
            u = plan.get_compute_device_usage_for_mlprogram_operation(op)
            dev = name(u.preferred_compute_device) if u else "none"
            cnt[dev] += 1
            e = plan.get_estimated_cost_for_mlprogram_operation(op)
            cost[dev] += e.weight if e else 0
            if dev != "ANE":
                offane[f"{op.operator_name}@{dev}"] += 1
        tot = sum(cost.values()) or 1
        print(f"{fn_name}: ops {dict(cnt)}  cost share " + " ".join(f"{k} {v / tot * 100:.1f}%" for k, v in cost.items()))
        print("   off-ANE ops:", dict(offane.most_common(12)))
