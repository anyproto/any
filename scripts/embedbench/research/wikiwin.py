"""wikiwin.py <wiki dir> <out dir>

Builds a local proxy of the account's editor index from the Wikipedia
extracts (plain text with '== Heading ==' section lines): paragraphs and
headings become blocks, and internal/editor/window.go's Windows() rule
(break before every heading and at 1500 bytes; heading leads its window;
an over-budget block alone forms its own window) coalesces them. Writes
  docs.jsonl   {id, title (leading heading or ''), text, heading_only, article, lang}
  stats on heading-only windows
Headings become heading-only windows when the next paragraph alone exceeds
the budget (the common Wikipedia case) or when headings are consecutive.
"""
import glob, json, os, re, sys

WIKI, OUT = sys.argv[1], sys.argv[2]
os.makedirs(OUT, exist_ok=True)
BUDGET = 1500
HEAD = re.compile(r"^(=+)\s*(.*?)\s*=+$")


def blocks(extract):
    """(kind, text, path): path = heading stack at this block"""
    out, stack = [], []
    for line in extract.split("\n"):
        line = line.strip()
        if not line:
            continue
        m = HEAD.match(line)
        if m:
            lvl = len(m.group(1)) - 1
            stack = stack[: lvl - 1] + [m.group(2)]
            out.append(("h", m.group(2), list(stack)))
        else:
            out.append(("p", line, list(stack)))
    return out


def windows(bl):
    out, cur, title, started, path = [], "", "", False, []
    for kind, text, p in bl:
        is_h = kind == "h"
        if started and (is_h or len(cur.encode()) + len(text.encode()) + 1 > BUDGET):
            if cur.strip():
                out.append((title, cur, path))
            cur, title, started = "", "", False
        if not started:
            title = text if is_h else ""
            started, path = True, p
        cur = text if not cur else cur + "\n" + text
    if started and cur.strip():
        out.append((title, cur, path))
    return out


docs, n_h = [], 0
with open(os.path.join(OUT, "docs.jsonl"), "w") as f:
    for p in sorted(glob.glob(os.path.join(WIKI, "*.json"))):
        lang = os.path.basename(p).split("-")[0]
        for page in json.load(open(p))["query"]["pages"].values():
            ex = page.get("extract")
            if not ex:
                continue
            art = page["title"]
            for i, (title, text, path) in enumerate(windows(blocks(ex))):
                ho = title != "" and text.strip() == title.strip()
                n_h += ho
                d = {"id": f"{lang}-{art}-{i}", "title": title, "text": text, "heading_only": ho, "article": art, "lang": lang, "path": path}
                docs.append(d)
                f.write(json.dumps(d, ensure_ascii=False) + "\n")
L = [len(d["text"]) for d in docs]
print(f"{len(docs)} windows, {n_h} heading-only ({100*n_h/len(docs):.0f}%), median chars {sorted(L)[len(L)//2]}")
