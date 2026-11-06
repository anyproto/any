"""convert.py <hf dir> <out dir> [--buckets 128,256,512,1024] [--ln-scale 0.03125] [--precision tail32|fp16]
One fp16 mlprogram per fixed length, then one multifunction package
(functions s<L>, shared weights) and its compiled .mlmodelc.
Inputs input_ids int32 [1, L] (pad 0) and attention_mask float32 [1, L]
(the iPhone app's contract); output `embedding` float32 [1, 768],
CLS-pooled and L2-normalised. Deployment target iOS 18 / macOS 15."""
import argparse
import os
import shutil
import subprocess
import time

import coremltools as ct
import numpy as np
import torch

from granite_ane import Granite, Pooled

ap = argparse.ArgumentParser()
ap.add_argument("hf")
ap.add_argument("out")
ap.add_argument("--buckets", default="128,256,512,1024")
ap.add_argument("--ln-scale", type=float, default=1 / 32)
ap.add_argument("--precision", default="tail32")
ap.add_argument("--name", default="GraniteANE")
a = ap.parse_args()
os.makedirs(a.out, exist_ok=True)
torch.set_grad_enabled(False)

# "fp16": every op fp16. "tail32" (default): the pooled tail after the last
# layer (final LayerNorm on the CLS row and the L2 normalisation, all [1, 768]
# or [1, 1]) stays fp32 on the CPU; in fp16 the output norm drifts by +-1e-3.
TAIL = {"layer_norm", "mul", "reduce_sum", "sqrt", "real_div"}


def fp16_op(op):
    o = op.outputs[0]
    return not (op.op_type in TAIL and o.rank == 2 and o.shape[-1] in (768, 1))


prec = ct.precision.FLOAT16 if a.precision == "fp16" else ct.transform.FP16ComputePrecision(op_selector=fp16_op)

parts = []
for L in [int(x) for x in a.buckets.split(",")]:
    t = time.time()
    m = Pooled(Granite(a.hf, L, ln_scale=a.ln_scale)).eval()
    ids = torch.zeros(1, L, dtype=torch.int32)
    ids[0, :8] = torch.tensor([2, 651, 1234, 99, 7, 3000, 42, 5])
    mk = torch.zeros(1, L, dtype=torch.float32)
    mk[0, :8] = 1
    tr = torch.jit.trace(m, (ids, mk), strict=True)
    ml = ct.convert(
        tr, convert_to="mlprogram", minimum_deployment_target=ct.target.iOS18,
        inputs=[ct.TensorType(name="input_ids", shape=(1, L), dtype=np.int32),
                ct.TensorType(name="attention_mask", shape=(1, L), dtype=np.float32)],
        outputs=[ct.TensorType(name="embedding", dtype=np.float32)],
        compute_precision=prec, compute_units=ct.ComputeUnit.ALL, skip_model_load=True,
    )
    p = os.path.join(a.out, f"part-s{L}.mlpackage")
    shutil.rmtree(p, ignore_errors=True)
    ml.save(p)
    parts.append((L, p))
    print(f"s{L}: converted in {time.time() - t:.0f}s", flush=True)
    del m, tr, ml

desc = ct.utils.MultiFunctionDescriptor()
for L, p in parts:
    desc.add_function(p, src_function_name="main", target_function_name=f"s{L}")
desc.default_function_name = f"s{parts[-1][0]}"
pkg = os.path.join(a.out, f"{a.name}.mlpackage")
shutil.rmtree(pkg, ignore_errors=True)
ct.utils.save_multifunction(desc, pkg)
mlc = os.path.join(a.out, f"{a.name}.mlmodelc")
shutil.rmtree(mlc, ignore_errors=True)
subprocess.run(["xcrun", "coremlcompiler", "compile", pkg, a.out], check=True, capture_output=True)
for L, p in parts:
    shutil.rmtree(p)
sz = lambda d: sum(os.path.getsize(os.path.join(r, f)) for r, _, fs in os.walk(d) for f in fs) / 1e6  # noqa: E731
print(f"{pkg}: {sz(pkg):.0f} MB; {mlc}: {sz(mlc):.0f} MB")
