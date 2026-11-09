"""Reporting-only audits derived from frozen source files and saved ranks."""
from util2 import *
from pipeline2 import G,ACTIVE,loan_score,doc_matches,vector_text,base_tokens,expanded_tokens
import re
def fmt(x):return f"{x['delta']:+.3f} [{x['lo']:+.3f}, {x['hi']:+.3f}]"
def run():
 pages={r['title']:r for r in read(BASE/'sources/pages.jsonl')};lookup={r['lemma']:i for i,r in enumerate(G)};rows=[]
 for r in read(BASE/'sets/sealed-scenarios.jsonl'):
  f=r['family'];i=lookup.get(f);stage='active family' if i in ACTIVE else 'no documented etymon or low loan similarity' if i is not None else 'no eligible computer-sense extraction' if f in pages else 'family absent from acquired whole categories'
  rows.append(dict(family=f,id=r['id'],stage=stage,en=G[i]['en'] if i is not None else None,ambiguous=G[i]['ambiguous'] if i is not None else None,loan_score=loan_score(G[i]) if i is not None else None))
 write(BASE/'results/coverage-stages.jsonl',rows)
 audit=[]
 for i,g in enumerate(G):
  if not g['en']:continue
  s=g.get('source_text','');explicit=bool(re.search(r'\{\{(?:lang|сэ)\|en\||\{\{lang-en\||англ\.',s));audit.append(dict(lemma=g['lemma'],candidate=g['en'],active=i in ACTIVE,explicit_english_marker=explicit,source=g['source'],etymology=s))
 write(BASE/'results/etymology-audit.jsonl',audit)
 z=np.load(BASE/'dev/dev.npz');qs=read(BASE/'dev/dev-queries.jsonl');norms=json.loads((BASE/'dev/alias_norms.json').read_text())
 lines=['# Token alias dose and random controls','','Computed from saved dev scores, no new scoring. Paired target-cluster 95% CIs.','','| Slice | Dose | W vector MRR | R vector MRR | W hybrid MRR | R hybrid MRR | Δ W−R vector | Δ W−R hybrid |','|---|---:|---:|---:|---:|---:|---|---|']
 slices=[('slang paraphrase',[i for i,q in enumerate(qs) if q.get('source_kind')=='synthetic' and q['kind']=='para']),('ordinary',[i for i,q in enumerate(qs) if q['kind']=='ordinary']),('English',[i for i,q in enumerate(qs) if q['kind']=='english'])]
 for sl,ii in slices:
  for dose in [.25,.5,1.]:
   w='W'+str(dose);r='R'+str(dose);groups=[qs[i]['target'] for i in ii];vals=[z[n+'.'+leg+'/mrr10'][ii].mean() for leg in ['vector','hybrid'] for n in [w,r]];diff=[ci(z[w+'.'+leg+'/mrr10'][ii],z[r+'.'+leg+'/mrr10'][ii],groups) for leg in ['vector','hybrid']]
   lines.append(f'| {sl} | {dose} | '+' | '.join(f'{v:.4f}' for v in vals)+' | '+' | '.join(fmt(v) for v in diff)+' |')
 lines+=['','| Dose | Alias edit norm | Random edit norm | Max per-row displacement mismatch | Max final row-norm error |','|---|---:|---:|---:|---:|']
 for dose,r in norms.items():lines.append(f'| {dose} | {r["alias_displacement"]:.8f} | {r["random_displacement"]:.8f} | {r["max_row_displacement_error"]:.2e} | {r["max_final_norm_error"]:.2e} |')
 (BASE/'WEIGHT_TABLES.md').write_text('\n'.join(lines)+'\n')
 z=np.load(BASE/'results/round1.npz');old=np.load(OLD/'results/test-per-query.npz');qs=read(BASE/'results/round1-queries.jsonl');checks={}
 for new,prior in [('H.vector','head'),('Q.vector','qwen'),('H.hybrid','head-hybrid-base')]:checks[new]={k:float(np.max(np.abs(z[new+'/'+k]-old[prior+'/'+k]))) for k in ['mrr10','ndcg10','recall10','rank']}
 save(BASE/'results/round1-baseline-reproduction.json',checks)
 changes=[]
 for i in np.flatnonzero(z['H.hybrid/mrr10']!=old['head-hybrid-base/mrr10']):changes.append(dict(id=qs[i]['id'],kind=qs[i]['kind'],source=qs[i]['source_kind'],old_rank=int(old['head-hybrid-base/rank'][i]),round2_rank=int(z['H.hybrid/rank'][i])))
 save(BASE/'results/round1-hybrid-tie-differences.json',changes)
 cases=[]
 p=BASE/'from-rig/lookalikes-batch2.jsonl'
 if p.exists():
  for r in read(p):
   if r['id'] in ['s12','s15','s20']:cases.append(dict(id=r['id'],text=r['text'],vector_text=vector_text(r['text']),query=r['para_query'],original_query_tokens=base_tokens(r['para_query']),expanded_query_tokens=expanded_tokens(r['para_query'],'query'),doc_additions=sorted(set(expanded_tokens(r['text'],'doc'))-set(base_tokens(r['text'])))))
 save(BASE/'results/failure-case-expansions.json',cases)
if __name__=='__main__':run()
