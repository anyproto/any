"""cos.py <ref.f32> <run.f32> [skip-idx-file]: row-wise cosine of two
vector dumps (uint32 count, uint32 dim, float32 LE rows)."""
import sys
import numpy as np


def load(p):
    b = open(p, "rb").read()
    n, d = np.frombuffer(b[:8], "<u4")
    return np.frombuffer(b[8:], "<f4").reshape(int(n), int(d)).astype(np.float64)


def cmp(a, b, mask=None):
    assert a.shape == b.shape, (a.shape, b.shape)
    nan = int((~np.isfinite(b).all(1)).sum())
    ok = np.isfinite(a).all(1) & np.isfinite(b).all(1)
    if mask is not None:
        ok &= mask
    a, b = a[ok], b[ok]
    c = (a * b).sum(1) / (np.linalg.norm(a, axis=1) * np.linalg.norm(b, axis=1))
    i = int(np.argmin(c))
    return dict(rows=int(ok.sum()), mean=round(float(c.mean()), 6),
                min=round(float(c.min()), 6), p01=round(float(np.quantile(c, .01)), 6),
                nanRows=nan)


if __name__ == "__main__":
    ref, run = load(sys.argv[1]), load(sys.argv[2])
    print("all", cmp(ref, run))
    if len(sys.argv) > 3:
        trunc = set(int(x) for x in open(sys.argv[3]).read().split())
        m = np.array([i not in trunc for i in range(len(ref))])
        print("untruncated", cmp(ref, run, m))
        print("truncated", cmp(ref, run, ~m) if (~m).any() else None)
