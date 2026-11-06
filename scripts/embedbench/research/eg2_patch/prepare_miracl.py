"""Cross-check a public MIRACL text repack against official dev queries/counts.

Uses a union of judged passages, not the multi-million-passage full collection.
The repack omits doc IDs; exact text SHA256 is the passage identity. Verify each
selected query and its positive/negative judgment counts against official qrels.
"""
from common import *
sys.path.insert(0,str(BASE/'deps'))
import pyarrow.parquet as pq

for lang in ['ru','de']:
    p=BASE/'sources';topics=dict(line.split('\t',1) for line in (p/f'miracl-{lang}-topics.tsv').read_text().splitlines())
    official={}
    for line in (p/f'miracl-{lang}-qrels.tsv').read_text().splitlines():
        q,_,d,r=line.split();official.setdefault(q,[]).append(int(r))
    bytext={qtext:qid for qid,qtext in topics.items()}
    records=pq.read_table(p/f'miracl-{lang}.parquet').to_pylist()
    matched=[];mismatch=[]
    for r in records:
        if r['query'] not in bytext:continue
        qid=bytext[r['query']];counts=(sum(x>0 for x in official[qid]),sum(x==0 for x in official[qid]))
        if counts!=(len(r['positive']),len(r['negative'])):mismatch.append(qid);continue
        matched.append((qid,r))
    # Deterministic query sample, selected without any model scores.
    matched=sorted(matched,key=lambda x:hashlib.sha256(('391/'+x[0]).encode()).hexdigest())[:200]
    docs={};qs=[];qr={}
    for qid,r in matched:
        qs.append(dict(id=qid,text=r['query'],lang=lang));qr[qid]={}
        for rel,field in [(1,'positive'),(0,'negative')]:
            for text in r[field]:
                did=hashlib.sha256(text.encode()).hexdigest();docs[did]=dict(id=did,text=text,lang=lang)
                qr[qid][did]=max(qr[qid].get(did,0),rel)
    assert qs,'No verified official-dev query matches'
    out=BASE/'sets'/f'miracl-{lang}-pool';out.mkdir(parents=True,exist_ok=True)
    write(out/'docs.jsonl',list(docs.values()));write(out/'queries.jsonl',qs);save(out/'qrels.json',qr)
    save(out/'provenance.json',dict(official_dev_queries=len(topics),repack_rows=len(records),selected=len(qs),mismatches=mismatch,
        text_source=f'https://huggingface.co/datasets/Hieuman/{lang}_MIRACL',query_qrels_source='https://huggingface.co/datasets/miracl/miracl',
        limitation='Text repack has no original doc IDs; query wording and judgment counts verified, passage IDs cannot be independently cross-checked. Closed union-pool exact search; unjudged pairs treated nonrelevant.',
        hashes={f.name:sha(f) for f in p.glob(f'miracl-{lang}*')}))
    log(f'MIRACL {lang}: {len(qs)} official-dev queries matched to public text repack and judgment counts; {len(docs)} union-pool passages. Closed pool, not full corpus.')
