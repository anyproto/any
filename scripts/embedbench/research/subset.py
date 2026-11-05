"""subset.py <beir dir> <out dir> <max docs>

Replicates capBEIRCorpus (internal/indexer/beir_test.go): every doc that is
relevant in qrels/test.tsv (corpus order), then the first non-relevant docs
until max. Writes docs.jsonl ({id,title,text=title+body,body}), queries.jsonl ({id,text}) and
qrels.json ({qid: [docid...]}) for test queries with a relevant doc present.
"""
import json, os, sys, csv

beir, out, maxd = sys.argv[1], sys.argv[2], int(sys.argv[3])
os.makedirs(out, exist_ok=True)
qrels = {}
with open(os.path.join(beir, "qrels", "test.tsv")) as f:
    r = csv.reader(f, delimiter="\t")
    next(r)
    for qid, did, score in r:
        if int(score) > 0:
            qrels.setdefault(qid, set()).add(did)
rel = {d for ds in qrels.values() for d in ds}
corpus = [json.loads(l) for l in open(os.path.join(beir, "corpus.jsonl"))]
docs = [d for d in corpus if d["_id"] in rel]
for d in corpus:
    if len(docs) >= maxd:
        break
    if d["_id"] not in rel:
        docs.append(d)
present = {d["_id"] for d in docs}
queries = {}
for l in open(os.path.join(beir, "queries.jsonl")):
    q = json.loads(l)
    queries[q["_id"]] = q["text"]
qs, qr = [], {}
for qid, ds in qrels.items():
    p = sorted(d for d in ds if d in present)
    if qid in queries and p:
        qs.append({"id": qid, "text": queries[qid]})
        qr[qid] = p
with open(os.path.join(out, "docs.jsonl"), "w") as f:
    for d in docs:
        text = (d.get("title", "") + "\n" + d["text"]).strip()
        f.write(json.dumps({"id": d["_id"], "title": d.get("title", "").strip(), "text": text, "body": d["text"].strip()}, ensure_ascii=False) + "\n")
with open(os.path.join(out, "queries.jsonl"), "w") as f:
    for q in qs:
        f.write(json.dumps(q, ensure_ascii=False) + "\n")
json.dump(qr, open(os.path.join(out, "qrels.json"), "w"))
print(f"docs {len(docs)} (rel {len(docs) if len(docs) < len(rel) else len(rel)}), queries {len(qs)}")
