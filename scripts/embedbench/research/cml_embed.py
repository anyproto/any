"""cml_embed.py <in.jsonl> <out.f32> --prefix '<str>' [--field text] [--maxlen 2048] [--title-prefix]

EG2 on the Mac ANE (Nemosine compiled model, functions s128..s2048; set
EG2_COREML and EG2_TOKENIZER): embeds
each line's `field` with the given prefix. --title-prefix builds the doc
prompt from the line's title: 'title: <title or none> | text: '. Output: a
flat float32 array [n, 768], L2-normalized rows, in input order. Also writes
<out>.ntok with the token count per row.
"""
import argparse, json, sys, time
import numpy as np
import coremltools as ct
from tokenizers import Tokenizer

import os
MODEL = os.environ["EG2_COREML"]       # compiled Nemosine .mlmodelc (see ../mac/coreml_bench.py)
TOKJ = os.environ["EG2_TOKENIZER"]     # EG2 tokenizer.json
BUCKETS = [128, 256, 512, 1024, 2048]

ap = argparse.ArgumentParser()
ap.add_argument("inp"); ap.add_argument("out")
ap.add_argument("--prefix", default="")
ap.add_argument("--field", default="text")
ap.add_argument("--maxlen", type=int, default=2048)
ap.add_argument("--title-prefix", action="store_true")
ap.add_argument("--limit", type=int, default=0)
a = ap.parse_args()

tok = Tokenizer.from_file(TOKJ)
tok.no_truncation()
EOS = tok.token_to_id("<eos>")
models = {L: ct.models.CompiledMLModel(MODEL, compute_units=ct.ComputeUnit.CPU_AND_NE, function_name=f"s{L}") for L in BUCKETS}


def embed_ids(ids, maxlen):
    if len(ids) > maxlen:
        ids = ids[: maxlen - 1] + [EOS]
    L = next(b for b in BUCKETS if b >= len(ids))
    x = np.zeros((1, L), dtype=np.int32); x[0, : len(ids)] = ids
    m = np.zeros((1, L), dtype=np.float32); m[0, : len(ids)] = 1
    v = np.asarray(models[L].predict({"input_ids": x, "attention_mask": m})["embedding"], dtype=np.float32).reshape(-1)
    return v / np.linalg.norm(v), len(ids)


rows = [json.loads(l) for l in open(a.inp)]
if a.limit:
    rows = rows[: a.limit]
out = np.zeros((len(rows), 768), dtype=np.float32)
ntok = np.zeros(len(rows), dtype=np.int32)
t0 = time.time()
for i, r in enumerate(rows):
    if a.title_prefix:
        t = r.get("title") or "none"
        pfx = f"title: {t} | text: "
        body = r[a.field]
    else:
        pfx, body = a.prefix, r[a.field]
    out[i], ntok[i] = embed_ids(tok.encode(pfx + body).ids, a.maxlen)
    if i % 2000 == 0 and i:
        print(f"{i}/{len(rows)} {time.time()-t0:.0f}s", file=sys.stderr, flush=True)
out.tofile(a.out)
ntok.tofile(a.out + ".ntok")
print(f"{a.out}: {len(rows)} rows in {time.time()-t0:.0f}s", file=sys.stderr)
