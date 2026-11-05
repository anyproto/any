"""analyze.py: table + cosines for the iPhone runs in out/.
Cosine is against the Mac BF16 CPU reference (../eg2-bf16-cpu.*.f32) for
EG2 and against the Mac Qwen3 Q8 CPU run for Qwen3, same doc order; the
Mac dumps are read from $EMBEDBENCH_DIR (default <repo>/bench)."""
import json, os, struct, sys
import numpy as np

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "out")
ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
B = os.environ.get("EMBEDBENCH_DIR", os.path.join(ROOT, "bench"))


def rd(p):
    b = open(p, "rb").read()
    n, m = struct.unpack("<II", b[:8])
    return np.frombuffer(b[8:], "<f4").reshape(n, m)


def cos(a, b):
    return (a * b).sum(1) / np.linalg.norm(a, axis=1) / np.linalg.norm(b, axis=1)


def trunc(name):
    p = os.path.join(OUT, name + ".truncated")
    if not os.path.exists(p):
        return set()
    return {int(x) for x in open(p).read().split() if x.strip()}


rows = []
for f in sorted(os.listdir(OUT)):
    if not f.endswith(".json") or f.startswith(("cool-", "smoke")):
        continue
    name = f[:-5]
    d = json.load(open(os.path.join(OUT, f)))
    if d.get("engine") == "plan":
        for fn, v in sorted(d["functions"].items()):
            ops, cost = v["ops"], v["cost"]
            n, c = sum(ops.values()), sum(cost.values()) or 1
            print(f"PLAN {name} {fn}: ops {ops} ({100 * ops.get('ane', 0) / n:.1f}% ane), "
                  f"cost ane {100 * cost.get('ane', 0) / c:.1f}%, cpu ops {v['cpuOps']}")
        continue
    ref = "qwen3-q8-cpu" if d.get("kind") == "qwen3" else "eg2-bf16-cpu"
    r = {"name": name, "docs": d["docs"], "dps": d["docsPerSec"], "p50": d["docLatMs"]["p50"],
         "p95": d["docLatMs"]["p95"], "p99": d["docLatMs"]["p99"],
         "q50": (d.get("queryLatMs") or {}).get("p50"), "load": d["loadSec"],
         "foot": (d.get("footprintAfterLoadMB"), d.get("footprintAfterWarmMB"), d.get("footprintEndMB"),
                  d["sampler"]["maxFootprintMB"]),
         "trunc": d["truncatedDocs"], "nan": d["vectorsWithNaN"], "thermal": d["sampler"]["maxThermalState"],
         "batch": (d.get("batched") or {}).get("docsPerSec"), "ref": ref}
    dp = os.path.join(OUT, name + ".docs.f32")
    if os.path.exists(dp):
        a = rd(dp)
        R = rd(os.path.join(B, ref + ".docs.f32"))[: len(a)]
        c = cos(a, R)
        t = trunc(name)
        un = np.array([i not in t for i in range(len(a))])
        r["cos"] = (float(c.mean()), float(c.min()))
        r["cosU"] = (float(c[un].mean()), float(c[un].min())) if un.any() else None
        r["cosT"] = (float(c[~un].mean()), float(c[~un].min())) if (~un).any() else None
        qp = os.path.join(OUT, name + ".queries.f32")
        rq = os.path.join(B, ref + ".queries.f32")
        if os.path.exists(qp) and os.path.exists(rq):
            cq = cos(rd(qp), rd(rq))
            r["cosQ"] = (float(cq.mean()), float(cq.min()))
    rows.append(r)

for r in rows:
    print(json.dumps(r))
