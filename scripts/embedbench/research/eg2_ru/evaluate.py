from util import *
from pipeline import rewrite,bm25,fuse
import argparse,re
METRICS=['mrr10','ndcg10','recall10','gold_score','margin','short80','short400']
def load_bank(split,model):
 p=BASE/'vectors'/f'{split}-{model}';rr=read(p/'records.jsonl');v=np.load(p/'raw.npy');return {r['key']:i for i,r in enumerate(rr)},v

def vectors(bank,rows,role,prompt='prod'):
 idx,v=bank;return norm(v[[idx[key(r['text'],role if prompt=='prod' else role+'/'+prompt)] for r in rows]])
def dataset(split,mode='original'):
 if split=='rig':return read(BASE/'sets/rig-docs.jsonl'),read(BASE/'sets/rig-queries.jsonl')
 targets=[];qs=[];hubs=[]
 for src in ['synthetic','public']:
  for r in read(BASE/'sets'/f'{src}.jsonl'):
   if r['split']!=split:continue
   targets.append(dict(r,source_kind=src))
   for kind in ['kw','para','cross']:
    qs.append(dict(id=r['id']+':'+kind,text=r[kind],target=r['id'],group=r['id'],kind=kind,source_kind=src,formality=r['formality'],length=len(r['text']),latin_share=latin_share(r['text'])))
   if src=='synthetic':hubs.append(dict(id=r['id']+'-hub',text='Обсуждение: '+r['kw']+'. Подробности будут позже.',formality='hub'))
 ds=read(BASE/'sets/public-background.jsonl')+targets+hubs
 return [dict(d,text=rewrite(d['text'],mode)) for d in ds],qs

def latin_share(s):
 ws=re.findall(r'[^\W\d_]+',s);return sum(bool(re.search('[a-zA-Z]',w)) for w in ws)/max(1,len(ws))
def metrics(s,ds,qs,cosine=True):
 order=np.argsort(-s,axis=1,kind='stable');top=order[:,:10];ids={r['id']:i for i,r in enumerate(ds)};gold=np.array([ids[q['target']] for q in qs]);ranks=np.array([int(np.where(o==g)[0][0])+1 for o,g in zip(order,gold)])
 other=s.copy();other[np.arange(len(qs)),gold]=-np.inf
 lengths=np.array([len(d['text']) for d in ds])
 out=dict(mrr10=np.where(ranks<=10,1/ranks,0),ndcg10=np.where(ranks<=10,1/np.log2(ranks+1),0),recall10=(ranks<=10).astype(float),gold_score=s[np.arange(len(qs)),gold],margin=s[np.arange(len(qs)),gold]-other.max(1),short80=(lengths[top]<80).mean(1),short400=(lengths[top]<400).mean(1),rank=ranks,top=top)
 return out

def head(x):
 u=np.load(ROOT/'scripts/embedbench/research/eg2_head_patch/head_mean.npy');u=u/np.linalg.norm(u);return norm(x-np.outer(x@u,u))
def project(x,u,a):return norm(x-a*np.outer(x@u,u))
def fit_directions(bank,ds):
 u=norm(np.load(ROOT/'scripts/embedbench/research/eg2_head_patch/head_mean.npy'))
 rr=[r for r in read(BASE/'sets/synthetic.jsonl') if r['split']=='dev']
 get=lambda texts:head(vectors(bank,[dict(text=t) for t in texts],'doc'))
 ru=get([r['base_text'] for r in rr]);en=get([r['english'] for r in rr]);plain=get([rewrite(r['base_text'],'plain') for r in rr])
 dirs={}
 for name,d in [('language',ru-en),('slang',ru-plain)]:
  v=d.mean(0);v=norm(v-u*(v@u));dirs[name]=v
  rng=np.random.default_rng(391 if name=='language' else 392);z=rng.standard_normal(len(v));z=norm(z-u*(z@u)-v*(z@v));dirs[name+'_random']=z
  # Stability of sign-aligned contrasts; mean direction removed only if it generalizes.
  odd=norm(d[::2].mean(0));even=norm(d[1::2].mean(0))
  log(f'Dev {name} direction split-half cosine {odd@even:.4f}; removal probes follow, not proof of knowledge.')
 ii=sorted(range(len(ds)),key=lambda i:key(ds[i]['text'],'center'))[:100]
 dirs['centering_mean']=vectors(bank,[ds[i] for i in ii],'doc').mean(0)
 np.savez(BASE/'dev/directions.npz',**dirs)
 return dirs

def summarize(results,qs):
 groups=np.array([q['group'] for q in qs]);out={};slices={'all':np.arange(len(qs))}
 for f in ['kind','source_kind','formality']:
  for label in sorted({q.get(f,'unknown') for q in qs}):slices[f+'/'+label]=np.array([i for i,q in enumerate(qs) if q.get(f,'unknown')==label])
 for src in ['synthetic','public','rig']:
  for kind in ['kw','para','cross']:
   ii=[i for i,q in enumerate(qs) if q['source_kind']==src and q['kind']==kind]
   if ii:slices[src+'/'+kind]=np.array(ii)
 for name,fn in [('length/<400',lambda q:q['length']<400),('length/>=400',lambda q:q['length']>=400),('latin/<10%',lambda q:q['latin_share']<.1),('latin/>=10%',lambda q:q['latin_share']>=.1)]:
  ii=[i for i,q in enumerate(qs) if fn(q)]
  if ii:slices[name]=np.array(ii)
 for sl,ii in slices.items():
  sub={n:{k:float(m[k][ii].mean()) for k in METRICS} for n,m in results.items()}
  comparisons={n:{ref:{k:ci(m[k][ii],results[ref][k][ii],groups[ii]) for k in METRICS} for ref in ['stock','head','qwen'] if ref in results and ref!=n} for n,m in results.items()}
  out[sl]=dict(n=len(ii),means=sub,differences=comparisons)
 return out

def run(split):
 dest=BASE/'results'/f'{split}.json';assert not dest.exists(),'Never rescore held-out or silently overwrite dev results'
 eg=load_bank(split,'eg2');qw=load_bank(split,'qwen');ds,qs=dataset(split);d=vectors(eg,ds,'doc');q=vectors(eg,qs,'query');dh=head(d);qh=head(q)
 dirs=fit_directions(eg,ds) if split=='dev' else dict(np.load(BASE/'dev/directions.npz'))
 scores={'stock':q@d.T,'head':qh@dh.T,'centered':norm(q-.5*dirs['centering_mean'])@d.T,'qwen':vectors(qw,qs,'query')@vectors(qw,ds,'doc').T}
 if split=='dev':
  for typ in ['language','slang']:
   for a in [-.5,.25,.5,1.,1.5]:scores[f'{typ}/{a}']=project(qh,dirs[typ],a)@project(dh,dirs[typ],a).T
 else:
  frozen=json.loads((BASE/'FROZEN.json').read_text())
  for typ,a in frozen['projection_doses'].items():
   scores[typ]=project(qh,dirs[typ],a)@project(dh,dirs[typ],a).T
   scores[typ+'_random']=project(qh,dirs[typ+'_random'],abs(a))@project(dh,dirs[typ+'_random'],abs(a)).T
 for mode in ['plain','gloss','latin','translit']:
  altered=[dict(r,text=rewrite(r['text'],mode)) for r in ds];scores['head-'+mode]=qh@head(vectors(eg,altered,'doc')).T
 if split=='dev':
  for prompt in ['bare','ru','paraphrase']:scores['prompt-'+prompt]=head(vectors(eg,qs,'query',prompt))@dh.T
 # Primary vector scores plus lexical and hybrid estimates; no local index accessed.
 for mode in ['base','stem','gloss']:
  b=bm25(ds,qs,mode);scores['bm25-'+mode]=b
  for model in ['stock','head','centered','qwen']:scores[model+'-hybrid-'+mode]=fuse(scores[model],b)
  if mode=='base':
   for pm in ['plain','gloss','latin','translit']:scores['head-'+pm+'-hybrid']=fuse(scores['head-'+pm],b)
 if split=='dev':
  ii=[i for i,r in enumerate(qs) if r['source_kind']=='synthetic' and r['kind']=='para'];doses={}
  for typ in ['language','slang']:
   opts={a:metrics(scores[f'{typ}/{a}'],ds,qs)['mrr10'][ii].mean() for a in [-.5,.25,.5,1.,1.5]}
   a=max(opts,key=lambda a:(opts[a],-abs(a)));doses[typ]=a
   scores[typ+'_random']=project(qh,dirs[typ+'_random'],abs(a))@project(dh,dirs[typ+'_random'],abs(a)).T
  save(BASE/'dev/selected.json',dict(projection_doses=doses,selection='Max synthetic/dev Russian paraphrase MRR, smallest absolute dose breaks ties; pipeline variants all retained as prespecified diagnostics'))
 results={n:metrics(s,ds,qs,not ('hybrid' in n or 'bm25' in n)) for n,s in scores.items()}
 save(dest,summarize(results,qs));np.savez_compressed(BASE/'results'/f'{split}-per-query.npz',**{n+'/'+k:v for n,m in results.items() for k,v in m.items()});write(BASE/'results'/f'{split}-queries.jsonl',qs);write(BASE/'results'/f'{split}-docs.jsonl',ds)
 save(BASE/'results'/f'{split}-ledger.json',dict(variants=list(scores),count=len(scores),ds=len(ds),qs=len(qs)))
 log(f'Scored {split} once: {len(ds)} docs, {len(qs)} queries, {len(scores)} variants; paired target-cluster CIs saved.')
 for n in scores:print(n,results[n]['mrr10'].mean(),flush=True)
if __name__=='__main__':
 ap=argparse.ArgumentParser();ap.add_argument('split',choices=['dev','test','rig']);run(ap.parse_args().split)
