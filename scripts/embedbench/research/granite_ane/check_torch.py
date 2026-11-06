"""check_torch.py <hf dir> <corpus.jsonl> [n]
Torch-side checks before conversion: the re-implementation (padded to the
smallest bucket) vs HF's ModernBertModel (unpadded, eager, fp32), per LN eps,
plus activation ranges for fp16 safety."""
import json
import sys

import numpy as np
import torch
from tokenizers import Tokenizer
from transformers import AutoModel

from granite_ane import Granite, Pooled

hf, corpus = sys.argv[1], sys.argv[2]
n = int(sys.argv[3]) if len(sys.argv) > 3 else 50
B = [128, 256, 512, 1024]
tok = Tokenizer.from_file(f"{hf}/tokenizer.json")
tok.no_padding()
tok.no_truncation()
texts = [json.loads(l)["text"] for l in open(corpus)]
enc = [tok.encode(t).ids for t in texts]
# mixed lengths: spread over buckets
order = np.argsort([len(e) for e in enc])
pick = order[np.linspace(0, len(order) - 1, n).astype(int)]

ref = AutoModel.from_pretrained(hf, dtype=torch.float32, attn_implementation="eager").eval()
ref.config.reference_compile = False
print("pad row norm", ref.embeddings.tok_embeddings.weight[0].norm().item())

mods = {}
res = {}
torch.set_grad_enabled(False)
for i in pick:
    ids = enc[i][:1024]
    L = next(b for b in B if b >= len(ids))
    r = ref(input_ids=torch.tensor([ids])).last_hidden_state[0, 0]
    r = (r / r.norm()).numpy()
    x = torch.zeros(1, L, dtype=torch.long)
    x[0, : len(ids)] = torch.tensor(ids)
    m = torch.zeros(1, L, dtype=torch.int32)
    m[0, : len(ids)] = 1
    for eps in (1e-12, 1e-5, 1e-4):
        key = (L, eps)
        if key not in mods:
            mods[key] = Granite(hf, L, ln_eps=eps).eval()
        c, hs = mods[key](x, m, return_hidden=True)
        v = (c / c.norm())[0].numpy()
        res.setdefault(eps, {}).setdefault(L, []).append(float(v @ r))
        if eps == 1e-12:
            mx = max(h.abs().max().item() for h in hs)
            res.setdefault("maxact", []).append(mx)
            res.setdefault("clsnorm", []).append(c.norm().item())
    for key in list(mods):
        if key[0] != L:
            pass
for eps in (1e-12, 1e-5, 1e-4):
    print(f"eps {eps}: " + "  ".join(f"s{L} n={len(v)} min {min(v):.6f} med {np.median(v):.6f}" for L, v in sorted(res[eps].items())))
print("max |hidden| over layers: max %.1f  p50 %.1f" % (max(res["maxact"]), np.median(res["maxact"])))
print("CLS LN norm: min %.1f max %.1f" % (min(res["clsnorm"]), max(res["clsnorm"])))
