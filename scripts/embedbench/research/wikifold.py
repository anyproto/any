"""wikifold.py <wiki set dir> <out dir>

Chunking variant of the wiki window proxy: a heading-only window is
removed and its heading is carried into the next window of the same
section — both as the window's Title (EG2 'title: <h> | text: ' prompt)
and, in the `text` field, prepended as the leading line (what window.go
already does when the heading's paragraph fits the budget). Queries and
qrels are copied with the removed ids dropped."""
import json, os, sys
src, out = sys.argv[1], sys.argv[2]
os.makedirs(out, exist_ok=True)
docs = [json.loads(l) for l in open(f"{src}/docs.jsonl")]
new, carry, removed = [], None, set()
for x in docs:
    if x["heading_only"]:
        carry = x
        removed.add(x["id"])
        continue
    if carry and carry["article"] == x["article"] and carry["lang"] == x["lang"]:
        y = dict(x)
        y["title"] = carry["title"]
        y["text"] = carry["title"] + "\n" + x["text"]
        y["body"] = x["text"]
        new.append(y)
    else:
        y = dict(x); y["body"] = x["text"]
        new.append(y)
    carry = None
with open(f"{out}/docs.jsonl", "w") as f:
    for y in new:
        f.write(json.dumps(y, ensure_ascii=False) + "\n")
qrels = json.load(open(f"{src}/qrels.json"))
json.dump({q: [i for i in ids if i not in removed] for q, ids in qrels.items()}, open(f"{out}/qrels.json", "w"))
os.system(f"cp {src}/queries.jsonl {out}/")
print(f"{len(docs)} -> {len(new)} windows ({len(removed)} heading-only folded)")
