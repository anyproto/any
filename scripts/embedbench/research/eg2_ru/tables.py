"""Render tables and derived comparisons from frozen per-query results only."""
from util import *

def fmt(d,nd=3):return f"{d['delta']:+.{nd}f} [{d['lo']:+.{nd}f}, {d['hi']:+.{nd}f}]"
def load(split,suffix=''):
 z=np.load(BASE/'results'/f'{split}{suffix}-per-query.npz');qs=read(BASE/'results'/f'{split}-queries.jsonl');return z,qs
def diff(z,qs,a,b,ii,metric='mrr10'):
 return ci(z[a+'/'+metric][ii],z[b+'/'+metric][ii],[qs[i]['group'] for i in ii])
def render():
 lines=['# Frozen result tables','','All differences are paired 95% bootstrap intervals: 4,000 resamples, seed 391; target clusters. Pointwise, not multiplicity-adjusted. MRR/Recall/nDCG use cutoff 10. Rank differences are not treated as MRR differences. Hybrid is a single-field BM25 estimate with equal-weight RRF(60), 30 candidates per leg.','','## Held-out vector and hybrid retrieval','']
 derived={}
 for split in ['test','rig']:
  z,qs=load(split)
  for src in (['synthetic','public'] if split=='test' else ['rig']):
   for kind in (['kw','para','cross'] if split=='test' else ['kw','para']):
    ii=[i for i,q in enumerate(qs) if q['source_kind']==src and q['kind']==kind];name=src+'/'+kind;derived[name]={}
    lines += [f'### {name} (n={len(ii)})','','| Method | MRR@10 | Δ vs head | Δ vs Qwen |','|---|---:|---|---|']
    for n in ['stock','head','centered','qwen','language','language_random','slang','slang_random','head-gloss','head-plain','head-latin','head-translit']:
     v=float(z[n+'/mrr10'][ii].mean());dh=diff(z,qs,n,'head',ii);dq=diff(z,qs,n,'qwen',ii);derived[name][n]=dict(mrr=v,vs_head=dh,vs_qwen=dq)
     lines.append(f'| {n} | {v:.3f} | {fmt(dh)} | {fmt(dq)} |')
    lines += ['','| Hybrid method | MRR@10 | Δ vs head + base BM25 | Δ vs Qwen + base BM25 |','|---|---:|---|---|']
    for n in ['head-hybrid-base','qwen-hybrid-base','head-hybrid-stem','head-hybrid-gloss','head-gloss-hybrid']:
     v=float(z[n+'/mrr10'][ii].mean());dh=diff(z,qs,n,'head-hybrid-base',ii);dq=diff(z,qs,n,'qwen-hybrid-base',ii);derived[name][n]=dict(mrr=v,vs_head=dh,vs_qwen=dq)
     lines.append(f'| {n} | {v:.3f} | {fmt(dh)} | {fmt(dq)} |')
    derived[name]['language_vs_random']=diff(z,qs,'language','language_random',ii)
    derived[name]['slang_vs_random']=diff(z,qs,'slang','slang_random',ii)
    lines+=['']
 lines+=['## Held-out sensitivity without artificial hubs','','| Slice | Stock | Head | Qwen | Head+glossary | Δ glossary vs head | Δ head vs Qwen |','|---|---:|---:|---:|---:|---|---|']
 z,qs=load('test','-nohubs');main,_=load('test');derived['nohubs']={}
 for src in ['synthetic','public']:
  for kind in ['kw','para','cross']:
   ii=[i for i,q in enumerate(qs) if q['source_kind']==src and q['kind']==kind];name=src+'/'+kind
   dh=diff(z,qs,'head-gloss','head',ii);dq=diff(z,qs,'head','qwen',ii);vals={n:float(z[n+'/mrr10'][ii].mean()) for n in ['stock','head','qwen','head-gloss']};derived['nohubs'][name]=dict(vals,gloss_vs_head=dh,head_vs_qwen=dq)
   lines.append('| '+name+' | '+' | '.join(f'{vals[n]:.3f}' for n in ['stock','head','qwen','head-gloss'])+f' | {fmt(dh)} | {fmt(dq)} |')
 lines += ['','## Held-out rig cases: exact vector ranks in public pool','','These are different distractors from the private rig pool; never compare the absolute ranks as if they were one experiment.','','| Case | Rig Qwen/stock/head | Public Qwen | Stock | Head | Head+gloss | Head+plain | Head+English source | Glossary fires |','|---|---|---:|---:|---:|---:|---:|---:|---|']
 z,qs=load('rig');rig=read(BASE/'from-rig/synthetic-lookalikes.jsonl');trig=json.loads((BASE/'results/rig-provenance.json').read_text())['glossary_triggers'];derived['rig_cases']=[]
 for r in rig:
  i=next(i for i,q in enumerate(qs) if q['id']==r['id']+':para');ranks={n:int(z[n+'/rank'][i]) for n in ['qwen','stock','head','head-gloss','head-plain','head-latin']};derived['rig_cases'].append(dict(id=r['id'],ranks=ranks,trigger=trig[r['id']]))
  external='/'.join(r['rig_para_rank'][n] for n in ['qwen3','eg2','eg2_head']);lines.append('| '+r['id']+' | '+external+' | '+' | '.join(str(v) for v in ranks.values())+' | '+str(trig[r['id']])+' |')
 # Prespecified rig failure subset, externally labeled before scoring.
 ii=[i for i,q in enumerate(qs) if q['kind']=='para' and q.get('rig_reproduces_gap')];derived['rig_external_failure_subset']={n:dict(mrr=float(z[n+'/mrr10'][ii].mean()),vs_head=diff(z,qs,n,'head',ii),vs_qwen=diff(z,qs,n,'qwen',ii)) for n in ['stock','head','qwen','head-gloss','head-hybrid-base','head-hybrid-stem','head-hybrid-gloss']}
 # Compare actual paired hub removal; includes both improved and harmed ranks.
 z,qs=load('test');nh,_=load('test','-nohubs');ds=read(BASE/'results/test-docs.jsonl');hub=np.array([d['id'].endswith('-hub') for d in ds]);derived['hubs']={}
 for kind in ['kw','para','cross']:
  ii=[i for i,q in enumerate(qs) if q['source_kind']=='synthetic' and q['kind']==kind];derived['hubs'][kind]={}
  for n in ['stock','head','qwen']:
   h=hub[z[n+'/top'][ii]].mean(1);h1=hub[z[n+'/top'][ii,0]].astype(float);derived['hubs'][kind][n]=dict(top10_share=float(h.mean()),top1_share=float(h1.mean()),removal_mrr=ci(nh[n+'/mrr10'][ii],z[n+'/mrr10'][ii]))
 # Query-kind interactions are descriptive slices from the same frozen rankings.
 derived['paraphrase_slices']={};lines += ['', '## Held-out authored paraphrases by target properties', '', '| Slice | n | Head MRR | Qwen MRR | Δ head−Qwen | Δ glossary−head |','|---|---:|---:|---:|---|---|']
 for field in ['formality','length','latin_share']:
  labels=['chat','notes'] if field=='formality' else (['<400','>=400'] if field=='length' else ['<10%','>=10%'])
  for label in labels:
   def match(q):
    if field=='formality':return q[field]==label
    if field=='length':return (q[field]<400)==(label=='<400')
    return (q[field]<.1)==(label=='<10%')
   ii=[i for i,q in enumerate(qs) if q['source_kind']=='synthetic' and q['kind']=='para' and match(q)]
   if not ii:continue
   vals={n:float(z[n+'/mrr10'][ii].mean()) for n in ['head','qwen']};a=diff(z,qs,'head','qwen',ii);b=diff(z,qs,'head-gloss','head',ii);derived['paraphrase_slices'][field+'/'+label]=dict(n=len(ii),**vals,head_vs_qwen=a,gloss_vs_head=b)
   lines.append(f"| {field}/{label} | {len(ii)} | {vals['head']:.3f} | {vals['qwen']:.3f} | {fmt(a)} | {fmt(b)} |")
 # Full regression candidates including exact zeros; pairwise intervals already computed.
 reg=json.loads((BASE/'results/regression.json').read_text());lines += ['','## Public regression nDCG@10 vs existing exact head','','| Set | Language projection | Slang projection | Additive glossary | Russian replacement | English replacement | Transliteration |','|---|---|---|---|---|---|']
 for name,r in reg.items():lines.append('| '+name+' | '+' | '.join(fmt(r['differences'][n]['ndcg10'],4) for n in ['language','slang','gloss','plain','latin','translit'])+' |')
 save(BASE/'results/derived.json',derived);(BASE/'TABLES.md').write_text('\n'.join(lines)+'\n');log('Rendered tables and paired hybrid-to-hybrid contrasts from saved ranks; no rescoring or retuning.')
if __name__=='__main__':render()
