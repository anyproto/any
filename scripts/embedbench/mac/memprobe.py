"""memprobe.py <model.mlmodelc> <spec> [units]
spec: comma-separated functions to load in one process, repeats allowed
("s512,s512"); "-" = the default function (FilBona). Loads them one by one,
runs one predict on each, and prints this process's neural memory
(footprint's "(neural)" rows) after every step."""
import os, re, subprocess, sys
import coremltools as ct
import numpy as np

path, spec = sys.argv[1], sys.argv[2]
units = getattr(ct.ComputeUnit, sys.argv[3] if len(sys.argv) > 3 else "CPU_AND_NE")


def neural():
    out = subprocess.run(["footprint", str(os.getpid())], capture_output=True, text=True).stdout
    tot, unit = 0.0, {"B": 1 / 1048576, "KB": 1 / 1024, "MB": 1, "GB": 1024}
    for line in out.splitlines():
        if "(neural)" in line:
            nums = re.findall(r"([\d.]+)\s*(B|KB|MB|GB)\b", line)
            tot += sum(float(v) * unit[u] for v, u in nums[:3])
    fp = re.search(r"phys_footprint:\s*([\d.]+)\s*(KB|MB|GB)", out)
    return tot, (float(fp.group(1)) * unit[fp.group(2)] if fp else 0)


models = []
print(f"start neural {neural()[0]:.0f} MB")
for fn in spec.split(","):
    kw = {} if fn == "-" else {"function_name": fn}
    m = ct.models.CompiledMLModel(path, compute_units=units, **kw)
    n, f = neural()
    print(f"load {fn:6} neural {n:6.0f} MB  phys {f:5.0f} MB")
    L = 512 if fn == "-" else int(fn[1:])
    x = {"input_ids": np.zeros((1, L), dtype=np.int32)}
    x["input_ids"][0, :4] = [2, 100, 200, 1]
    if fn != "-":
        mk = np.zeros((1, L), dtype=np.float32)
        mk[0, :4] = 1
        x["attention_mask"] = mk
    m.predict(x)
    n, f = neural()
    print(f"pred {fn:6} neural {n:6.0f} MB  phys {f:5.0f} MB")
    models.append(m)
