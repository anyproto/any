"""Analyze only the two text-free rank/hit exports. Never open query/pool text files."""
from common import *
from collections import Counter
p=BASE/'from-rig'
rows=read(p/'known-ranks.jsonl')
assert all(set(r)=={'qid','model','mode','goldRank','goldObjectRank'} for r in rows)
hits=read(p/'topical-hits.jsonl')
assert all(set(r)=={'qid','model','mode','rank','chunk'} for r in hits)
R={'scope':'Two shared English editor spaces only; no chat. Rank-only analysis. All 108 queries included because weak-row flags live in a prohibited text-bearing query file; not the filtered canonical account score.',
   'read_files':['known-ranks.jsonl','topical-hits.jsonl'],
   'not_read':['known-queries.jsonl','topical-queries.jsonl','topical-pool.jsonl'],
   'known':{},'topical':{}}
for mode in ['vector','hybrid','fts']:
    maps={m:{r['qid']:r for r in rows if r['model']==m and r['mode']==mode} for m in ['granite-311m','qwen3']}
    ids=sorted(maps['granite-311m']);assert set(ids)==set(maps['qwen3'])
    R['known'][mode]={}
    for kind in ['all','kw','para','cross']:
        sel=[i for i in ids if kind=='all' or i.split(':')[-1]==kind]
        if not sel:continue
        obj={'queries':len(sel),'targets':len(set(q.split(':')[0] for q in sel))}
        for field in ['goldRank','goldObjectRank']:
            v={m:np.array([1/maps[m][q][field] if maps[m][q][field] else 0 for q in sel]) for m in maps}
            obj[field]={m:float(x.mean()) for m,x in v.items()}
            obj[field]['granite_minus_qwen']=delta(v['granite-311m'],v['qwen3'],groups=[q.split(':')[0] for q in sel])
        R['known'][mode][kind]=obj
for mode in ['vector','hybrid']:
    ids=sorted({r['qid'] for r in hits if r['mode']==mode});over=[]
    for q in ids:
        a={r['chunk'] for r in hits if r['mode']==mode and r['qid']==q and r['model']=='granite-311m'}
        b={r['chunk'] for r in hits if r['mode']==mode and r['qid']==q and r['model']=='qwen3'}
        over.append(len(a&b)/10)
    R['topical'][mode]={'queries':len(ids),'mean_top10_overlap':float(np.mean(over)),'note':'No relevance/length metrics computed: their source file contains account text and was not opened.'}
(BASE/'debug/rig_metadata.json').write_text(json.dumps(R,indent=2))
log('Late rig export received. Read only schema-checked text-free known-ranks/topical-hits files. No query, gold passage or pooled text file opened; no vectors supplied. Paired rank-only analysis includes weak rows (flags unavailable under no-text rule), so it is explicitly not the canonical filtered account score.')
for mode in R['known']:
    for kind,x in R['known'][mode].items():print(mode,kind,x['goldRank'])
