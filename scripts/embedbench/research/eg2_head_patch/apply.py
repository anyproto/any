"""apply.py <embeddinggemma-2-BF16.gguf> <embeddinggemma-2-Q8_0.gguf> <out.gguf> [--alpha 1.0] [--bf16-out]

Rebuilds the EG2 "head" weight patch (SYN-391, eg2_patch agent, frozen 2026-11-06 11:30) from
ggml-org's stock files. It needs numpy and gguf-py from llama.cpp b11600 (`gguf-py/` on PYTHONPATH).

The edit is one tensor. `output.weight` is EG2's bias-free 512→768 dense head after mean pooling:

    W' = W − α·u(uᵀW),   u = head_mean.npy (unit, 768), α = 1.0

That projects one output direction out of every vector: the mean direction of the pooled outputs.
W is read from the BF16 GGUF, edited in float, and rounded to BF16. Then:
- the Q8_0 output: every other tensor and all metadata stay byte-identical, and output.weight is
  re-quantized with gguf-py's reference Q8_0 quantizer;
- with `--bf16-out`: the BF16 file is patched instead.

Prompts, mean pooling, the 768-d output and the 2048 context are unchanged. Re-embed the docs after
swapping the model.

Expected sha256 values:
- inputs: BF16 = 68bae29d62fb8c7d23e98d21fd4662753ddd636e6b62b8a70dfe059a9844f216; Q8_0 = 2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135
- output Q8_0: 17b7bf4dd431cb043b0cddd1c24c2c156de9b14e32f49c95f6c9f9fa87b27cb8
"""
import argparse
import hashlib
import os
import shutil
import sys

import numpy as np

import gguf

HERE = os.path.dirname(os.path.abspath(__file__))


def sha(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for b in iter(lambda: f.read(1 << 20), b""):
            h.update(b)
    return h.hexdigest()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("bf16")
    ap.add_argument("q8")
    ap.add_argument("out")
    ap.add_argument("--alpha", type=float, default=1.0)
    ap.add_argument("--bf16-out", action="store_true")
    a = ap.parse_args()

    u = np.load(os.path.join(HERE, "head_mean.npy")).astype(np.float32)
    r = gguf.GGUFReader(a.bf16)
    w = next(gguf.dequantize(t.data, t.tensor_type).copy() for t in r.tensors if t.name == "output.weight")
    new = w.copy()
    new -= a.alpha * np.outer(u, u @ w)
    BF16 = gguf.GGMLQuantizationType.BF16
    new = gguf.dequantize(gguf.quantize(new, BF16), BF16)

    src = a.bf16 if a.bf16_out else a.q8
    shutil.copyfile(src, a.out)
    rr = gguf.GGUFReader(a.out, "r+")
    for t in rr.tensors:
        if t.name == "output.weight":
            q = gguf.quantize(new, t.tensor_type)
            assert q.shape == t.data.shape
            t.data[:] = q
    rr.data.flush()
    del rr
    print("output", a.out, "sha256", sha(a.out))


if __name__ == "__main__":
    sys.exit(main())
