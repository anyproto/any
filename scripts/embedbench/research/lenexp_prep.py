"""lenexp_prep.py <tokenizer.json> <wiki dir> <out lenexp.json> [--texts <fiqa.jsonl> <go dir> <out texts.json>]

Inputs for go/lenexp: the wiki articles cut by any's 2000-rune splitter
(chunk.go), with sentence queries from each chunk <=512 EG2 tokens ("short"),
and from the head (< token 400) and the tail (> token 522) of each longer
chunk. --texts also writes the [[group, text], ...] corpus for go/toktime."""
import glob, json, os, re, sys
from tokenizers import Tokenizer

tok = Tokenizer.from_file(sys.argv[1]); tok.no_truncation()
DOC = "title: none | text: "
ids_doc = lambda t: tok.encode(DOC + t).ids


def break_point(w):
    half = len(w) // 2
    def best(ok):
        for i in range(len(w) - 1, half - 1, -1):
            if ok(i):
                return i + 1
        return -1
    for ok in (lambda i: w[i] == "\n" and i > 0 and w[i - 1] == "\n", lambda i: w[i] == "\n",
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


SENT = re.compile(r"[^\n.!?。！？]{25,220}[.!?。！？]")


def pick(chunk, lo, hi):
    enc = tok.encode(chunk, add_special_tokens=False)
    if len(enc.offsets) <= lo:
        return None
    a, b = enc.offsets[lo][0], enc.offsets[min(hi, len(enc.offsets)) - 1][1]
    for m in SENT.finditer(chunk, a, b):
        s = m.group(0).strip()
        if len(s) >= 25 and chunk.count(s) == 1:
            return s
    return None


arts = []
for f in sorted(glob.glob(os.path.join(sys.argv[2], "*.json"))):
    lang = os.path.basename(f).split("-")[0]
    for p in json.load(open(f))["query"]["pages"].values():
        if p.get("extract"):
            arts.append((lang, p["extract"]))
base = [(lang, c) for lang, a in arts for c in runes2000(a)]
Q = []
for i, (lang, c) in enumerate(base):
    n = len(ids_doc(c))
    if n <= 512:
        s = pick(c, 0, n)
        if s:
            Q.append({"lang": lang, "kind": "short", "text": s, "target": i})
        continue
    h, t = pick(c, 20, 400), pick(c, 522, n)
    if h:
        Q.append({"lang": lang, "kind": "head", "text": h, "target": i})
    if t:
        Q.append({"lang": lang, "kind": "tail", "text": t, "target": i})
json.dump({"chunks": [{"lang": l, "text": c, "eg2Tokens": len(ids_doc(c))} for l, c in base], "queries": Q},
          open(sys.argv[3], "w"))
print(f"{len(base)} chunks, {len(Q)} queries -> {sys.argv[3]}")
if "--texts" in sys.argv:
    i = sys.argv.index("--texts")
    fiqa, godir, out = sys.argv[i + 1:i + 4]
    texts = [["fiqa", json.loads(l)["text"]] for l in open(fiqa)]
    texts += [["wiki-" + lang, a] for lang, a in arts]
    texts += [["go", open(f).read()] for f in sorted(glob.glob(os.path.join(godir, "*.go")))]
    json.dump(texts, open(out, "w"))
    print(f"{len(texts)} texts -> {out}")
