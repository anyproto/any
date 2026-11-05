"""splitbench.py <tokenizer.json> <fiqa.jsonl> <wiki dir> <go dir>

Token-bounded splitters on any-like inputs, EG2 tokenizer, 512-token cap
including the doc prompt and BOS/EOS:
  speed, tokenizer work, cap compliance, fill, cut quality, and edit
  stability (chunks an edit forces to re-embed: the indexer keeps a chunk's
  vector only when its text hash is unchanged, internal/indexer/worker.go).
Also the editor's 1500-byte block windows (internal/editor/window.go)."""
import glob, json, os, random, sys, time
import numpy as np
from tokenizers import Tokenizer

TOKJ = sys.argv[1]
tok = Tokenizer.from_file(TOKJ)
tok.no_truncation()
DOC = "title: none | text: "
OVER = len(tok.encode(DOC).ids)
CAP = 512
C = CAP - OVER  # budget for the chunk text itself


def ntok(s):
    return len(tok.encode(DOC + s).ids)


calls = {"n": 0, "chars": 0}


def counter(s):
    calls["n"] += 1
    calls["chars"] += len(s)
    return len(tok.encode(s, add_special_tokens=False).ids)


# ---------- splitters ----------
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


def greedy(text, window_end):
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


def any_runes2000(text):
    return greedy(text, lambda rest: 2000)


def any_tok_offsets(text):
    """chunk.go's break preferences, window bounded by tokens: tokenize the
    remainder's head once, take the char offset of token C, verify."""
    def end(rest):
        head = rest[: C * 8]
        calls["n"] += 1
        calls["chars"] += len(head)
        enc = tok.encode(head, add_special_tokens=False)
        if len(enc.ids) <= C and len(head) == len(rest):
            return len(rest)
        e = enc.offsets[C - 1][1] if len(enc.ids) > C else len(head)
        while e > 1 and counter(rest[:e]) > C:
            e -= max(1, e // 50)
        return e
    return greedy(text, end)


def any_tok_once(text):
    """One tokenization of the whole record; cut points chosen on token
    offsets with chunk.go's break preferences, no re-tokenization (the
    boundary tokens are re-counted only by the final compliance check)."""
    calls["n"] += 1
    calls["chars"] += len(text)
    enc = tok.encode(text, add_special_tokens=False)
    offs = enc.offsets
    out, start, ti = [], 0, 0  # ti: first token at/after start
    n = len(offs)
    while start < len(text):
        while ti < n and offs[ti][0] < start:
            ti += 1
        if n - ti <= C - 4:  # slack for re-merging at the cut
            p = text[start:].strip()
            if p:
                out.append(p)
            break
        lim = offs[min(ti + C - 4, n) - 1][1]
        cut = start + break_point(text[start:lim])
        p = text[start:cut].strip()
        if p:
            out.append(p)
        start = cut
    return out


import semantic_text_splitter as sts
import semchunk
from langchain_text_splitters import RecursiveCharacterTextSplitter
import chonkie

_sts = sts.TextSplitter.from_huggingface_tokenizer(tok, C)
_sts_md = sts.MarkdownSplitter.from_huggingface_tokenizer(tok, C)
_sem = semchunk.chunkerify(counter, C)
_lc = RecursiveCharacterTextSplitter(chunk_size=C, chunk_overlap=0, length_function=counter,
                                     separators=["\n\n", "\n", ". ", "。", " ", ""])
try:
    _ck_rec = chonkie.RecursiveChunker(tokenizer=tok, chunk_size=C)
    _ck_tok = chonkie.TokenChunker(tokenizer=tok, chunk_size=C, chunk_overlap=0)
except Exception as e:
    print("chonkie with HF Tokenizer object failed:", e)
    _ck_rec = _ck_tok = None

SPLITTERS = {
    "any runes2000 (today)": any_runes2000,
    "any-style, token window": any_tok_offsets,
    "any-style, tokenize once": any_tok_once,
    "text-splitter (Rust) TextSplitter": lambda t: _sts.chunks(t),
    "text-splitter MarkdownSplitter": lambda t: _sts_md.chunks(t),
    "semchunk": lambda t: _sem(t),
    "LangChain Recursive (token len)": lambda t: _lc.split_text(t),
}
if _ck_rec:
    SPLITTERS["Chonkie RecursiveChunker"] = lambda t: [c.text for c in _ck_rec.chunk(t)]
    SPLITTERS["Chonkie TokenChunker (no bounds)"] = lambda t: [c.text for c in _ck_tok.chunk(t)]

# ---------- corpora ----------
fiqa = [json.loads(l)["text"] for l in open(sys.argv[2])]
wiki = {}
for f in sorted(glob.glob(os.path.join(sys.argv[3], "*.json"))):
    lang = os.path.basename(f).split("-")[0]
    for p in json.load(open(f))["query"]["pages"].values():
        if p.get("extract"):
            wiki.setdefault(lang, []).append(p["extract"])
gosrc = [open(f).read() for f in sorted(glob.glob(os.path.join(sys.argv[4], "*.go")))]
# "large text blocks": each wiki article / Go file is ONE block (pasted text,
# a code block); FiQA docs are the short-record case.
CORP = {"fiqa": fiqa, **{f"wiki-{k}": v for k, v in wiki.items()}, "go": gosrc}


def cut_kind(text, chunk):
    """Where the chunk ends in the source: para / line / sentence / space / mid."""
    chunk = chunk.strip()
    i = text.find(chunk)
    if i < 0:
        return "?"
    j = i + len(chunk)
    if j >= len(text.rstrip()):
        return "end"
    nxt = text[j:j + 2]
    if nxt.startswith("\n\n") or (nxt.startswith("\n") and text[j + 1:j + 2] == "\n"):
        return "para"
    if nxt.startswith("\n"):
        return "line"
    if chunk[-1:] in ".!?。！？":
        return "sentence"
    if nxt[:1].isspace():
        return "space"
    return "mid"


INS = {"ja": "この文は編集で挿入されました。", "zh": "这句话是编辑时插入的。", "ru": "Это предложение вставлено правкой. ",
       "de": "Dieser Satz wurde eingefügt. "}


def edit(text, frac, lang, rng):
    """Insert one sentence at a sentence/line boundary near frac of the text."""
    pos = int(len(text) * frac)
    for k in range(pos, min(len(text), pos + 400)):
        if text[k] in "\n。.":
            pos = k + 1
            break
    s = INS.get(lang, "This sentence was inserted by an edit. ")
    return text[:pos] + s + text[pos:]


results = {}
for cname, texts in CORP.items():
    lang = cname.split("-")[-1]
    long_texts = [t for t in texts if len(t) > 3000]
    print(f"\n== {cname}: {len(texts)} records, {sum(map(len, texts)) / 1e6:.2f} M chars, {len(long_texts)} long")
    print(f"  {'splitter':36} {'ms/MB':>7} {'tokx':>5} {'calls':>6} {'chunks':>6} {'p50':>4} {'max':>5} {'>512':>4} {'fill':>5}"
          f"  cuts para/line/sent/space/mid   re-embed per edit (25%/75%)")
    for sname, fn in SPLITTERS.items():
        calls["n"] = calls["chars"] = 0
        t0 = time.perf_counter()
        chunks_per = [fn(t) for t in texts]
        dt = time.perf_counter() - t0
        work = calls["chars"] / max(1, sum(map(len, texts)))
        ns = sorted(ntok(c) for cs in chunks_per for c in cs)
        kinds = {}
        for t, cs in zip(texts, chunks_per):
            for c in cs[:-1]:
                k = cut_kind(t, c)
                kinds[k] = kinds.get(k, 0) + 1
        tot = max(1, sum(v for k, v in kinds.items() if k not in ("end", "?")))
        kp = "/".join(f"{100 * kinds.get(k, 0) / tot:.0f}" for k in ("para", "line", "sentence", "space", "mid"))
        # edit stability on long records
        rng = random.Random(1)
        re = []
        for frac in (0.25, 0.75):
            ch = tot_new = 0
            for t in long_texts[:40]:
                old = set(fn(t))
                new = fn(edit(t, frac, lang, rng))
                ch += sum(1 for c in new if c not in old)
                tot_new += len(new)
            re.append((ch / max(1, len(long_texts[:40])), ch / max(1, tot_new)))
        fill = np.mean([min(n, CAP) / CAP for n in ns])
        print(f"  {sname:36} {dt * 1000 / (sum(map(len, texts)) / 1e6):7.0f} {work:5.1f} {calls['n']:6} {len(ns):6} "
              f"{ns[len(ns) // 2]:4} {ns[-1]:5} {sum(n > CAP for n in ns):4} {fill:5.2f}  {kp:28}"
              + ("   " + " / ".join(f"{a:.1f} chunks ({100 * b:.0f}%)" for a, b in re) if long_texts else ""))
        results.setdefault(cname, {})[sname] = {"msPerMB": dt * 1000 / (sum(map(len, texts)) / 1e6), "chunks": len(ns),
                                                "max": ns[-1], "over": sum(n > CAP for n in ns), "fill": float(fill), "reembed": re}


# ---------- editor windows (1500 bytes, break before headings) ----------
def windows(blocks):
    out, cur = [], None
    for typ, txt in blocks:
        head = typ == "h"
        if cur is not None and (head or len(cur.encode()) + len(txt.encode()) + 1 > 1500):
            out.append(cur)
            cur = None
        if cur is None:
            cur = txt
        else:
            cur = cur + "\n" + txt if txt else cur
    if cur:
        out.append(cur)
    return out


print("\n== editor windows: wiki paragraphs as blocks, '== x ==' lines as headings")
for lang, arts in wiki.items():
    ns, single_over, n_win = [], 0, 0
    for a in arts:
        blocks = [("h" if l.startswith("==") else "p", l.strip("= ").strip()) for l in a.split("\n") if l.strip()]
        ws = windows(blocks)
        n_win += len(ws)
        for w in ws:
            n = ntok(w)
            ns.append(n)
    ns.sort()
    bl = [ntok(t) for a in arts for t in a.split("\n") if t.strip()]
    print(f"  {lang}: {n_win} windows, tokens p50 {ns[len(ns) // 2]} p95 {ns[int(len(ns) * .95)]} max {ns[-1]}, "
          f">512: {sum(n > 512 for n in ns)};  blocks >512 tokens: {sum(b > 512 for b in bl)} of {len(bl)}")
json.dump(results, open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "splitbench.json"), "w"), indent=1)
