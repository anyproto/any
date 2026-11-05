"""chunktok.py: EG2 token counts of any's 2000-rune index chunks.
Ports internal/indexer/chunk.go splitText/breakPoint; tokenizes each chunk
with the bench doc prompt (BOS/EOS included) and reports which Nemosine
bucket it needs."""
import glob, json, sys
from tokenizers import Tokenizer

def break_point(w):
    half = len(w) // 2
    def best(ok):
        for i in range(len(w) - 1, half - 1, -1):
            if ok(i):
                return i + 1
        return -1
    for ok in (lambda i: w[i] == "\n" and i > 0 and w[i - 1] == "\n",
               lambda i: w[i] == "\n",
               lambda i: w[i] in ".!?" and i + 1 < len(w) and w[i + 1].isspace(),
               lambda i: w[i].isspace()):
        i = best(ok)
        if i > 0:
            return i
    return len(w)

def split_text(text, n=2000):
    if len(text) <= n:
        return [text]
    out, rest = [], text
    while len(rest) > n:
        cut = break_point(rest[:n])
        p = rest[:cut].strip()
        if p:
            out.append(p)
        rest = rest[cut:]
    if rest.strip():
        out.append(rest.strip())
    return out

tok = Tokenizer.from_file(sys.argv[1])
DOC = "title: none | text: "
def leaves(x):
    if isinstance(x, dict):
        for v in x.values(): yield from leaves(v)
    elif isinstance(x, list):
        for v in x: yield from leaves(v)
    elif isinstance(x, str):
        yield x

srcs = {"fiqa (each doc)": [json.loads(l)["text"] for l in open(sys.argv[2])]}
lang = sys.argv[3]
for code in ["ru-RU", "de-DE", "ja-JP", "zh-CN"]:
    srcs[code + " (UI strings)"] = ["\n".join(leaves(json.load(open(f"{lang}/{code}.json"))))]
srcs["Go source"] = ["\n".join(open(f).read() for f in sorted(glob.glob(sys.argv[4] + "/*.go")))]

for name, texts in srcs.items():
    chunks = [c for t in texts for c in split_text(t)]
    n = sorted(len(tok.encode(DOC + c).ids) for c in chunks)
    full = [len(c) for c in chunks]
    big = [x for x, r in zip((len(tok.encode(DOC + c).ids) for c in chunks), full) if r > 1500]
    b = lambda L: sum(1 for x in n if x > L)
    print(f"{name:20} chunks {len(n):5}  tokens p50 {n[len(n)//2]:5} max {n[-1]:5}  "
          f"full-size chunks: {min(big) if big else '-'}-{max(big) if big else '-'} tokens  "
          f">512: {b(512):4} >1024: {b(1024):3} >2048: {b(2048)}")
