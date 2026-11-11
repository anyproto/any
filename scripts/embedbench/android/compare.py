"""cos(LiteRT int4, reference stock EG2) per text, before and after the head projection."""
import sys, json
import numpy as np
OUT, B, s = sys.argv[1], sys.argv[2], sys.argv[3]
raw = np.load(f'{B}/vectors/original/raw.npy', mmap_mode='r'); idx = {}
for i, l in enumerate(open(f'{B}/vectors/original/records.jsonl')):
    r = json.loads(l); idx[(r['role'], r['text'])] = i
u = np.load('/Users/roman/anytype/any-wt-syn391/scripts/embedbench/research/eg2_head_patch/head_mean.npy').astype(np.float32)
n = lambda v: v / np.linalg.norm(v, axis=-1, keepdims=True)
hp = lambda V: n(V - np.outer(V @ u, u))
for role in ('doc', 'query'):
    V = np.load(f'{OUT}/{s}/{role}.npy'); T = [json.loads(l)['text'] for l in open(f'{OUT}/{s}/{role}.texts.jsonl')]
    keep = [i for i, t in enumerate(T) if (role, t) in idx]
    A = n(V[keep]); R = n(np.stack([np.asarray(raw[idx[(role, T[i])]], np.float32) for i in keep]))
    c0 = (A * R).sum(1); c1 = (hp(A) * hp(R)).sum(1)
    print(f'{s} {role}: n={len(keep)} cos stock {c0.mean():.4f} (min {c0.min():.4f}); after head projection {c1.mean():.4f} (min {c1.min():.4f}); |u·v| litert {np.abs(A @ u).mean():.3f} ref {np.abs(R @ u).mean():.3f}')
