"""qualexp.py <compiled Nemosine .mlmodelc> <tokenizer.json> <wiki dir>

What a 512-token cap does to retrieval of LARGE blocks (one wiki article =
one block, cut by any's 2000-rune splitter), with Nemosine on the Mac ANE:
  full    each 2000-rune chunk embedded whole (today, needs s1024/s2048)
  trunc   same chunks truncated to 512 tokens (cap without re-chunking)
  mean    same chunks; an over-cap chunk is embedded as <=512-token
          segments, token-weighted mean, renormalized (one vector per chunk)
  tok512  re-chunked at 512 tokens (more, smaller chunks)
Queries: sentences from the head (< token 512) and the tail (> token 512)
of every over-cap chunk, embedded with the query prompt. A hit is the chunk
(in that strategy's own chunk set, all languages pooled) holding the
sentence. Reports R@1 and MRR@10, plus cosine of trunc/mean to full."""
import glob, json, os, re, sys, time
import numpy as np
import coremltools as ct
from tokenizers import Tokenizer

MODEL, TOKJ, WIKI = sys.argv[1:4]
tok = Tokenizer.from_file(TOKJ)
tok.no_truncation()
DOC, QRY = "title: none | text: ", "task: search result | query: "
EOS = tok.token_to_id("<eos>")
BUCKETS = [128, 256, 512, 1024, 2048]
CAP = 512
OVER = len(tok.encode(DOC).ids)
C = CAP - OVER

models = {L: ct.models.CompiledMLModel(MODEL, compute_units=ct.ComputeUnit.CPU_AND_NE, function_name=f"s{L}")
          for L in BUCKETS}


def embed_ids(ids, maxlen=2048):
    if len(ids) > maxlen:
        ids = ids[: maxlen - 1] + [EOS]
    L = next(b for b in BUCKETS if b >= len(ids))
    x = np.zeros((1, L), dtype=np.int32)
    x[0, : len(ids)] = ids
    m = np.zeros((1, L), dtype=np.float32)
    m[0, : len(ids)] = 1
    out = models[L].predict({"input_ids": x, "attention_mask": m})
    v = np.asarray(out["embedding"], dtype=np.float64).reshape(-1)
    return v / np.linalg.norm(v)


def ids_doc(t):
    return tok.encode(DOC + t).ids


# ---- any's splitter ----
def break_point(w):
    half = len(w) // 2
    def best(ok):
        for i in range(len(w) - 1, half - 1, -1):
            if ok(i):
                return i + 1
        return -1
    for ok in (lambda i: w[i] == "\n" and i > 0 and w[i - 1] == "\n",
               lambda i: w[i] == "\n",
               lambda i: w[i] in ".!?。！？" and (i + 1 >= len(w) or w[i + 1].isspace() or w[i] in "。！？"),
               lambda i: w[i].isspace()):
        i = best(ok)
        if i > 0:
            return i
    return len(w)


def runes2000(text):
    out, rest = [], text
    while rest:
        if len(rest) <= 2000:
            if rest.strip():
                out.append(rest.strip())
            break
        cut = break_point(rest[:2000])
        p = rest[:cut].strip()
        if p:
            out.append(p)
        rest = rest[cut:]
    return out


def tok_once(text, budget=C):
    enc = tok.encode(text, add_special_tokens=False)
    offs, n = enc.offsets, len(enc.offsets)
    out, start, ti = [], 0, 0
    while start < len(text):
        while ti < n and offs[ti][0] < start:
            ti += 1
        if n - ti <= budget - 4:
            p = text[start:].strip()
            if p:
                out.append(p)
            break
        lim = offs[min(ti + budget - 4, n) - 1][1]
        cut = start + break_point(text[start:lim])
        p = text[start:cut].strip()
        if p:
            out.append(p)
        start = cut
    return out


arts = []
for f in sorted(glob.glob(os.path.join(WIKI, "*.json"))):
    lang = os.path.basename(f).split("-")[0]
    for p in json.load(open(f))["query"]["pages"].values():
        if p.get("extract"):
            arts.append((lang, p["extract"]))

base = [(lang, c) for lang, a in arts for c in runes2000(a)]
t512 = [(lang, c) for lang, a in arts for c in tok_once(a)]
print(f"{len(arts)} articles; runes2000 chunks {len(base)} ({sum(len(ids_doc(c)) > CAP for _, c in base)} over {CAP}); "
      f"tok512 chunks {len(t512)}")

SENT = re.compile(r"[^\n.!?。！？]{25,220}[.!?。！？]")


def pick(chunk, lo, hi):
    """a sentence starting in token range [lo, hi) of the chunk text"""
    enc = tok.encode(chunk, add_special_tokens=False)
    if len(enc.offsets) <= lo:
        return None
    a = enc.offsets[lo][0]
    b = enc.offsets[min(hi, len(enc.offsets)) - 1][1]
    for m in SENT.finditer(chunk, a, b):
        s = m.group(0).strip()
        if len(s) >= 25 and chunk.count(s) == 1:
            return s
    return None


queries = []  # (lang, kind, sentence, base chunk index)
for i, (lang, c) in enumerate(base):
    n = len(ids_doc(c))
    if n <= CAP:
        continue
    h = pick(c, 20, 400)
    t = pick(c, CAP + 10, n)
    if h:
        queries.append((lang, "head", h, i))
    if t:
        queries.append((lang, "tail", t, i))
print(f"queries: {sum(q[1] == 'head' for q in queries)} head, {sum(q[1] == 'tail' for q in queries)} tail")

t0 = time.time()
V_full, V_trunc, V_mean = [], [], []
for lang, c in base:
    ids = ids_doc(c)
    vf = embed_ids(ids)
    V_full.append(vf)
    if len(ids) <= CAP:
        V_trunc.append(vf)
        V_mean.append(vf)
        continue
    V_trunc.append(embed_ids(ids, maxlen=CAP))
    segs = tok_once(c)
    acc = np.zeros_like(vf)
    for s in segs:
        si = ids_doc(s)
        acc += len(si) * embed_ids(si, maxlen=CAP)
    V_mean.append(acc / np.linalg.norm(acc))
V_t512 = [embed_ids(ids_doc(c), maxlen=CAP) for _, c in t512]
Q = [embed_ids(tok.encode(QRY + q).ids, maxlen=CAP) for _, _, q, _ in queries]
print(f"embedded in {time.time() - t0:.0f} s")

over = [i for i, (_, c) in enumerate(base) if len(ids_doc(c)) > CAP]
for name, V in (("trunc", V_trunc), ("mean", V_mean)):
    cs = [float(V[i] @ V_full[i]) for i in over]
    print(f"cos({name}, full) over-cap chunks: mean {np.mean(cs):.4f} min {np.min(cs):.4f}")


def evaluate(V, chunks, target_of):
    M = np.stack(V)
    res = {}
    for (lang, kind, q, bi), qv in zip(queries, Q):
        tgt = target_of(q, bi)
        if not tgt:
            continue
        sc = M @ qv
        order = np.argsort(-sc)
        rank = min(int(np.where(order == t)[0][0]) for t in tgt) + 1
        r = res.setdefault(kind, [])
        r.append(rank)
        res.setdefault(f"{kind}-{lang}", []).append(rank)
    return res


def summary(res):
    out = {}
    for k, ranks in res.items():
        ranks = np.array(ranks)
        out[k] = (len(ranks), float(np.mean(ranks == 1)), float(np.mean(np.where(ranks <= 10, 1 / ranks, 0))))
    return out


same = lambda q, bi: [bi]
in512 = lambda q, bi: [j for j, (_, c) in enumerate(t512) if q in c] or None
S = {"full (today, s2048)": summary(evaluate(V_full, base, same)),
     "trunc 512": summary(evaluate(V_trunc, base, same)),
     "mean of 512 segments": summary(evaluate(V_mean, base, same)),
     "re-chunk 512": summary(evaluate(V_t512, t512, in512))}
keys = ["head", "tail"] + sorted(k for k in next(iter(S.values())) if "-" in k)
print(f"\n{'strategy':24}" + "".join(f"{k:>16}" for k in keys))
for name, s in S.items():
    print(f"{name:24}" + "".join(f"{s[k][1]:7.2f}/{s[k][2]:.2f} n{s[k][0]:<3}" if k in s else f"{'-':>16}" for k in keys))
print("cells: R@1 / MRR@10, n queries")
