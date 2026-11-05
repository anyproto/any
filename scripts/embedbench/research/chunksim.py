"""chunksim.py <tokenizer.json> <fiqa.jsonl> <wiki dir> <go dir>

Compares chunking strategies for the local embedder's Core ML buckets:
- runes2000: any's splitText at 2000 runes (internal/indexer/chunk.go)
- tokT:      same break preferences, window bounded by exact EG2 tokens
- estT:      window bounded by a script-weighted token estimate
and costs each with the iPhone 17 Pro's measured Neural Engine time per
bucket. Also: token-estimate accuracy, tokenizer speed, and packing several
chunks into one s512 call (block-diagonal attention)."""
import glob, json, os, sys, time
import numpy as np
from tokenizers import Tokenizer

tok = Tokenizer.from_file(sys.argv[1])
DOC = "title: none | text: "
OVER = len(tok.encode(DOC).ids)  # prompt + BOS + EOS

# iPhone 17 Pro ANE ms per call (pp-nemosine4 ane-hw-intervals); s2048 scaled
# from the Mac's 261 ms by the s1024 ratio (66.31 / 81.96).
ANE = {128: 5.46, 256: 11.14, 512: 25.30, 1024: 66.31, 2048: 261.05 * 66.31 / 81.96}
GAP = 0.2
MEM = {128: 283, 256: 307, 512: 350, 1024: 445, 2048: 641}  # standalone neural MB (memprobe)
SETS = {"A5 128-2048": [128, 256, 512, 1024, 2048], "B4 128-1024": [128, 256, 512, 1024],
        "C3 128-512": [128, 256, 512], "D2 128+512": [128, 512], "E1 512": [512]}


def ntok(s):
    return len(tok.encode(DOC + s).ids)


# ---- any's splitter (chunk.go) ----
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


def split_by(text, window_end):
    """window_end(rest) -> largest rune count the next chunk may take."""
    out, rest = [], text
    while rest:
        n = window_end(rest)
        if n >= len(rest):
            if rest.strip():
                out.append(rest.strip())
            break
        cut = break_point(rest[:n])
        p = rest[:cut].strip()
        if p:
            out.append(p)
        rest = rest[cut:]
    return out


def runes(n):
    return lambda rest: n


def tok_window(T):
    B = T - OVER
    def f(rest):
        head = rest[: B * 8]
        enc = tok.encode(head, add_special_tokens=False)
        if len(enc.ids) <= B and len(head) == len(rest):
            return len(rest)
        end = enc.offsets[B - 1][1] if len(enc.ids) > B else len(head)
        # offsets are char offsets into head; verify and shrink on boundary effects
        while end > 1 and ntok(rest[:end]) > T:
            end -= max(1, end // 50)
        return end
    return f


# ---- script-weighted estimate ----
CLASSES = ["latin", "cyr", "han", "kana", "hangul", "digit", "space", "punct", "other"]


def cls(c):
    o = ord(c)
    if c.isspace():
        return 6
    if c.isdigit():
        return 5
    if o < 0x80:
        return 0 if c.isalpha() else 7
    if 0x0400 <= o <= 0x052F:
        return 1
    if 0x4E00 <= o <= 0x9FFF or 0x3400 <= o <= 0x4DBF or 0xF900 <= o <= 0xFAFF:
        return 2
    if 0x3040 <= o <= 0x30FF:
        return 3
    if 0xAC00 <= o <= 0xD7AF:
        return 4
    if c.isalpha():
        return 0  # accented Latin and other alphabets
    return 7 if not c.isalnum() else 8


def counts(s):
    v = np.zeros(len(CLASSES))
    for c in s:
        v[cls(c)] += 1
    return v


def est_window(T, W, margin):
    B = (T - OVER) * (1 - margin)
    def f(rest):
        acc = 0.0
        for i, c in enumerate(rest):
            acc += W[cls(c)]
            if acc > B:
                return i
        return len(rest)
    return f


# ---- corpora ----
fiqa = [json.loads(l)["text"] for l in open(sys.argv[2])]
wiki = {}
for f in sorted(glob.glob(os.path.join(sys.argv[3], "*.json"))):
    lang = os.path.basename(f).split("-")[0]
    pages = json.load(open(f))["query"]["pages"]
    for p in pages.values():
        if p.get("extract"):
            wiki.setdefault(lang, []).append(p["extract"])
gosrc = [open(f).read() for f in sorted(glob.glob(os.path.join(sys.argv[4], "*.go")))]
corp = {"fiqa": fiqa, **{f"wiki-{k}": v for k, v in wiki.items()}, "go": gosrc}
print("corpora:", {k: (len(v), sum(map(len, v))) for k, v in corp.items()}, "prompt+special tokens", OVER)

# ---- tokenizer speed ----
alltext = [t for v in corp.values() for t in v]
t0 = time.perf_counter()
ntot = sum(len(tok.encode(t).ids) for t in alltext)
dt = time.perf_counter() - t0
print(f"tokenize: {ntot} tokens, {sum(map(len, alltext))} runes in {dt * 1000:.0f} ms = "
      f"{ntot / dt / 1e6:.2f} M tokens/s (one thread, HF tokenizers); "
      f"ANE embeds ~{1000 / ANE[512] * 512 / 1e3:.0f} k tokens/s at s512")

# ---- fit the estimator on runes2000 chunks of half the texts, test on the rest ----
train, test = [], []
for k, v in corp.items():
    for i, t in enumerate(v):
        for c in split_by(t, runes(2000)):
            (train if i % 2 == 0 else test).append((k, c))
X = np.array([counts(c) for _, c in train])
y = np.array([ntok(c) - OVER for _, c in train])
W, *_ = np.linalg.lstsq(X, y, rcond=None)
W = np.maximum(W, 0.01)
print("estimator tokens/rune:", {CLASSES[i]: round(float(W[i]), 3) for i in range(len(CLASSES))})
rows = {}
for k, c in test:
    a = ntok(c) - OVER
    e = float(counts(c) @ W)
    q = len(c) / 4
    rows.setdefault(k, []).append(((e - a) / a, (q - a) / a))
for k, r in rows.items():
    r = np.array(r)
    print(f"  {k:8} n={len(r):4}  weighted err p50 {np.median(np.abs(r[:, 0])) * 100:4.1f}% "
          f"max under {min(r[:, 0].min(), 0) * 100:6.1f}% max over {r[:, 0].max() * 100:5.1f}%   "
          f"runes/4 err p50 {np.median(np.abs(r[:, 1])) * 100:5.1f}% range {r[:, 1].min() * 100:.0f}..{r[:, 1].max() * 100:.0f}%")


# ---- strategies ----
def cost(ns, buckets):
    t = trunc = 0
    pad = 0
    for n in ns:
        L = next((b for b in buckets if b >= n), None)
        if L is None:
            L, trunc = buckets[-1], trunc + 1
        t += ANE[L] + GAP
        pad += min(n, L) / L
    return t / 1000, trunc, pad / len(ns)


def pack(ns, cap=512, smax=16):
    bins = []  # first-fit decreasing
    for n in sorted(ns, reverse=True):
        n = min(n, cap)
        for b in bins:
            if b[0] + n <= cap and b[1] < smax:
                b[0] += n
                b[1] += 1
                break
        else:
            bins.append([n, 1])
    return len(bins)


strats = {"runes2000": runes(2000), "tok1024": tok_window(1024), "tok512": tok_window(512),
          "est512 (m=10%)": est_window(512, W, 0.10), "est512 (m=0)": est_window(512, W, 0.0)}
for k, v in corp.items():
    print(f"\n== {k}: {len(v)} records, {sum(map(len, v))} runes")
    for sname, fn in strats.items():
        chunks = [c for t in v for c in split_by(t, fn)]
        ns = sorted(ntok(c) for c in chunks)
        over512 = sum(n > 512 for n in ns)
        line = f"  {sname:15} chunks {len(ns):5} tok p50 {ns[len(ns) // 2]:4} max {ns[-1]:5} >512 {over512:3} |"
        for setname, bk in SETS.items():
            t, tr, eff = cost(ns, bk)
            line += f" {setname.split()[0]} {t:6.1f}s tr{tr:<3}"
        nb = pack(ns)
        line += f" | pack512 {nb * (ANE[512] + GAP) / 1000:6.1f}s ({nb} calls)"
        print(line)
print("\nmemory MB:", {k: sum(MEM[b] for b in v) for k, v in SETS.items()})
