"""Open the explicitly cleared export only after freezing candidates."""
from common import *
assert (BASE/'FROZEN.json').exists(),'Rig is final-only; candidate specs must be frozen'
source=BASE/'from-rig'
# README was read manually before this script was written; retain a provenance hash.
known=read(source/'known-queries.jsonl');tqs=read(source/'topical-queries.jsonl');pool=read(source/'topical-pool.jsonl')
docs={};qrels={}
for r in pool:
    docs.setdefault(r['chunk'],dict(id=r['chunk'],text=r['text']))
    qrels.setdefault(r['qid'],{})[r['chunk']]=int(r['rel'])
queries=[]
for r in tqs:
    queries.append(dict(id=r['qid'],text=r.get('query',r.get('text')),space=r.get('space','unknown')))
assert all(isinstance(q['text'],str) for q in queries)
p=BASE/'sets/rig-topical';p.mkdir(parents=True,exist_ok=True)
write(p/'docs.jsonl',list(docs.values()));write(p/'queries.jsonl',queries);save(p/'qrels.json',qrels)
for r in known:docs.setdefault(r['gold'],dict(id=r['gold'],text=r['goldText']))
queries=[dict(id=r['qid'],text=r['query'],kind=r['kind'],target=r['gold'],space=r['space']) for r in known if not r.get('weak',False)]
qrels={r['qid']:{r['gold']:1} for r in known if not r.get('weak',False)}
p=BASE/'sets/rig-known';p.mkdir(parents=True,exist_ok=True)
write(p/'docs.jsonl',list(docs.values()));write(p/'queries.jsonl',queries);save(p/'qrels.json',qrels)
save(BASE/'results/rig-provenance.json',dict(source_hashes={p.name:sha(p) for p in source.glob('*.jsonl')},readme_sha256=sha(source/'README.md'),known_rows=len(known),nonweak_known=len(queries),pooled_rows=len(pool),unique_union_passages=len(docs),warning='Closed pool reranking is not the rig full-index IVF-SQ or hybrid score; topical judgments are LLM graded, texts clipped at 900 chars.'))
log(f'Final-only cleared rig opened after freeze: {len(queries)} nonweak known queries; {len(tqs)} topical queries; {len(docs)} union passages.')
