from util2 import *
from data2 import dataset,SETS,clusters
from cache2 import Bank
from pipeline2 import vector_text,lexical,fuse,doc_matches,expanded_tokens,base_tokens
from dev_controls import controls
import argparse
METRICS=['mrr10','ndcg10','recall10']
def load(name):
 ds,qs,rels=dataset(name)
 if name=='dev':
  d,q=controls();ds=sorted(ds+d,key=lambda r:r['id']);qs+=q;rels.update({r['id']:{r['target']:1} for r in q})
 return ds,qs,rels
def encode(scope,modelname):
 if scope!='dev':assert (BASE/'FROZEN.json').exists()
 names=['dev'] if scope=='dev' else ['sealed','round1','rig']+SETS+(['new-rig'] if (BASE/'sets/new-rig-docs.jsonl').exists() else [])
 rows={};gloss={}
 for name in names:
  ds,qs,_=load(name)
  for role,rs in [('doc',ds),('query',qs)]:
   for r in rs:
    row=dict(text=r['text'],role=role);rows[key(r['text'],role)]=row
    if modelname=='eg2' and role=='doc':
     t=vector_text(r['text'])
     if t!=r['text']:gloss[key(t,role)]=dict(text=t,role=role)
 bank=Bank(modelname,scope)
 if modelname=='qwen':
  from qwen import Qwen
  m=Qwen();bank.ensure(m,list(rows.values()));return
 from eg2 import EG2
 import alias2
 m=EG2(ROOT/'bench/embeddinggemma-2-Q8_0.gguf')
 alias=alias2.make_rows(m) if scope=='dev' else dict(np.load(BASE/'dev/alias_rows.npz'))
 bank.ensure(m,list(rows.values())+list(gloss.values()))
 doses=[.25,.5,1.] if scope=='dev' else [json.loads((BASE/'FROZEN.json').read_text())['weight_dose']]
 counts={}
 for dose in doses:
  for random in [False,True]:
   variant=('R' if random else 'W')+str(float(dose));alias2.apply(m,alias,dose,random)
   counts[variant]=bank.ensure(m,list(rows.values()),variant,alias['ids'])
 save(BASE/('dev' if scope=='dev' else 'results')/'encoding-counts.json',dict(original_rows=len(rows),gloss_rows=len(gloss),alias_counts=counts))
 log(f'Completed {scope}/{modelname}: {len(rows)} source records, {len(gloss)} glossary variants; sparse alias rows only re-encoded when present.')
def metrics(scores,ds,qs,rels,positive=False):
 order=np.argsort(-scores,axis=1,kind='stable');top=order[:,:10];ids=[r['id'] for r in ds];nd=[];rr=[];rec=[];ranks=[]
 for i,(t,q) in enumerate(zip(top,qs)):
  rel=rels[q['id']];rel=dict.fromkeys(rel,1) if isinstance(rel,list) else rel
  gain=np.array([2**rel.get(ids[j],0)-1 if not positive or scores[i,j]>0 else 0 for j in t]);ideal=sorted([2**g-1 for g in rel.values() if g>0],reverse=True)[:10];discount=1/np.log2(np.arange(len(t))+2)
  nd.append(float(gain@discount/(np.asarray(ideal)@discount[:len(ideal)])) if ideal else 0);hit=np.flatnonzero(gain>0);rr.append(float(1/(hit[0]+1)) if len(hit) else 0);rec.append(len(hit)/max(1,sum(g>0 for g in rel.values())))
  rank=len(ds)+1
  for j,di in enumerate(order[i]):
   if rel.get(ids[di],0)>0 and (not positive or scores[i,di]>0):rank=j+1;break
  ranks.append(rank)
 return dict(mrr10=np.array(rr),ndcg10=np.array(nd),recall10=np.array(rec),rank=np.array(ranks),top=top)
def summarize(ms,qs,rels,name):
 groups=np.array(clusters(qs,rels));slices={'all':np.arange(len(qs))}
 for k in sorted({q.get('kind','public') for q in qs}):slices[k]=np.array([i for i,q in enumerate(qs) if q.get('kind','public')==k])
 if name=='dev' or name=='round1':
  for src in ['synthetic','public','dev-control']:
   for kind in ['para','kw','cross','ordinary','english']:
    ii=[i for i,q in enumerate(qs) if q.get('source_kind')==src and q['kind']==kind]
    if ii:slices[src+'/'+kind]=np.array(ii)
 if name.startswith('sealed'):
  for label,pred in [('outside_round1',lambda q:not q['round1_known']),('covered',lambda q:q['covered']),('uncovered',lambda q:not q['covered']),('risk',lambda q:q['risk'])]:
   ii=[i for i,q in enumerate(qs) if q['kind']=='para' and pred(q)]
   if ii:slices[label]=np.array(ii)
 out={}
 for sl,ii in slices.items():
  means={n:{k:float(m[k][ii].mean()) for k in METRICS} for n,m in ms.items()}
  diffs={}
  for n,m in ms.items():
   ref='H.hybrid' if n.endswith('.hybrid') else 'H.bm25' if n.endswith('.bm25') else 'H.vector'
   qref='Q.hybrid' if n.endswith('.hybrid') else 'Q.bm25' if n.endswith('.bm25') else 'Q.vector'
   diffs[n]={r:{k:ci(m[k][ii],ms[r][k][ii],groups[ii]) for k in METRICS} for r in [ref,qref]}
  out[sl]=dict(n=len(ii),clusters=len(set(groups[ii])),means=means,differences=diffs)
 return out
def score(name):
 scope='dev' if name=='dev' else 'heldout';dest=BASE/('dev' if name=='dev' else 'results')/f'{name}.json';assert not dest.exists(),'Completed score passes are immutable'
 if name!='dev':assert (BASE/'FROZEN.json').exists();verify_seal()
 ds,qs,rels=load(name);eg=Bank('eg2',scope);qw=Bank('qwen',scope)
 if name.startswith('sealed'):
  byid={r['id']:r for r in ds};qs=[dict(q,covered=bool(doc_matches(byid[q['target']]['text']))) for q in qs]
 dh=head(eg.matrix(ds,'doc'));qh=head(eg.matrix(qs,'query'));h=qh@dh.T;q=qw.matrix(qs,'query')@qw.matrix(ds,'doc').T
 gd=[dict(r,text=vector_text(r['text'])) for r in ds];v=qh@head(eg.matrix(gd,'doc')).T
 b=lexical(ds,qs,'base');l=lexical(ds,qs,'expand');s=lexical(ds,qs,'stem')
 vectors={'H':h,'Q':q,'V':v,'L':h,'C':v,'S':h};lex={'H':b,'Q':b,'V':b,'L':l,'C':l,'S':s}
 doses=[.25,.5,1.] if name=='dev' else [json.loads((BASE/'FROZEN.json').read_text())['weight_dose']]
 for dose in doses:
  for tag in ['W','R']:
   n=tag+str(float(dose));vectors[n]=head(eg.matrix(qs,'query',n))@head(eg.matrix(ds,'doc',n)).T;lex[n]=b
 ms={}
 for arm,vv in vectors.items():
  for leg,sc,pos in [('vector',vv,False),('bm25',lex[arm],True),('hybrid',fuse(vv,lex[arm]),True)]:ms[arm+'.'+leg]=metrics(sc,ds,qs,rels,pos)
 summary=summarize(ms,qs,rels,name);save(dest,summary)
 np.savez_compressed(dest.with_suffix('.npz'),**{n+'/'+k:v for n,m in ms.items() for k,v in m.items()});write(dest.with_name(name+'-queries.jsonl'),qs);write(dest.with_name(name+'-docs.jsonl'),ds)
 save(dest.with_name(name+'-coverage.json'),dict(docs=len(ds),queries=len(qs),vector_docs=sum(a['text']!=c['text'] for a,c in zip(ds,gd)),lexical_docs=sum(list(base_tokens(r['text']))!=expanded_tokens(r['text'],'doc') for r in ds),lexical_queries=sum(list(base_tokens(r['text']))!=expanded_tokens(r['text'],'query') for r in qs)))
 if name=='dev':
  sel=[];dev=summary['synthetic/para'];control=[k for k in summary if k in ['dev-control/ordinary','dev-control/english','public/para','synthetic/kw']]
  for dose in doses:
   arm='W'+str(float(dose));delta=dev['differences'][arm+'.hybrid']['H.hybrid']['mrr10']['delta'];ok=dev['differences'][arm+'.vector']['H.vector']['mrr10']['delta']>=-.02
   ok=ok and all(summary[sl]['differences'][arm+'.'+leg]['H.'+leg]['mrr10']['delta']>=-.02 for sl in control for leg in ['vector','hybrid'])
   sel.append(dict(dose=dose,delta=delta,eligible=ok))
  eligible=[x for x in sel if x['eligible'] and x['delta']>0];chosen=max(eligible,key=lambda x:(x['delta'],-x['dose']))['dose'] if eligible else .25
  save(BASE/'dev/selected.json',dict(weight_dose=chosen,weight_rejected=not bool(eligible),candidates=sel,primary='C'))
 log(f'Scored {name} once: {len(ds)} docs, {len(qs)} queries; vector/BM25/fused arrays saved, paired target-connected bootstrap CIs.')
 for sl in ['para','synthetic/para','ordinary','english','public']:
  if sl in summary:print(sl,{n:round(m['mrr10'],4) for n,m in summary[sl]['means'].items() if n.endswith('.hybrid')},flush=True)
if __name__=='__main__':
 ap=argparse.ArgumentParser();ap.add_argument('action',choices=['encode','score']);ap.add_argument('name');ap.add_argument('model',nargs='?');a=ap.parse_args();encode(a.name,a.model) if a.action=='encode' else score(a.name)
