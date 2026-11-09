"""Only access the PUBLIC PREFIX of existing mixed banks; no private rows read.

The prefix is rebuilt from public datasets in encode_eval's insertion order. The
mixed records.jsonl is deliberately never opened. NPY mmap reads only selected
public row indices; source banks are never modified.
"""
from evaluate import *
SETS=['fiqa-full','wiki-mix','scifact','miracl-ru-pool','miracl-de-pool','ru']
def public_bank():
 p=ROOT/'bench/eg2-patch';run=json.loads((p/'vectors/original/run.json').read_text());idx={}
 allowed={'wiki','wiki-mix','wiki-fold','ru','fiqa10k','fiqa-full','scifact','nfcorpus','miracl-ru-pool','miracl-de-pool'}
 for name in run['sets']:
  if name not in allowed:break
  for role,filename in [('doc','docs.jsonl'),('query','queries.jsonl')]:
   for r in read(p/'sets'/name/filename):
    k=key(r['text'],role)
    if k not in idx:idx[k]=len(idx)
 raw=np.load(p/'vectors/original/raw.npy',mmap_mode='r')
 assert max(idx.values())<raw.shape[0]
 return idx,raw

def prepare():
 assert (BASE/'FROZEN.json').exists();bank={};counts={}
 for name in SETS:
  ds=read(ROOT/'bench/eg2-patch/sets'/name/'docs.jsonl');qs=read(ROOT/'bench/eg2-patch/sets'/name/'queries.jsonl');counts[name]={}
  for mode in ['plain','gloss','latin','translit']:
   rr=[dict(text=rewrite(r['text'],mode),role='doc',prompt='prod') for r in ds if rewrite(r['text'],mode)!=r['text']]
   counts[name][mode]=len(rr)
   for r in rr:bank[key(r['text'],'doc')]=dict(r,key=key(r['text'],'doc'))
 write(BASE/'sets/regression-bank.jsonl',list(bank.values()));save(BASE/'results/regression-triggers.json',counts)
 print(counts,flush=True)
 if bank:
  from eg2 import EG2
  m=EG2(ROOT/'bench/embeddinggemma-2-Q8_0.gguf');np.save(BASE/'vectors/regression-changed.npy',m.encode(list(bank.values()),budget=2048))

def run():
 assert not (BASE/'results/regression.json').exists();idx,raw=public_bank();dirs=dict(np.load(BASE/'dev/directions.npz'));fr=json.loads((BASE/'FROZEN.json').read_text());out={}
 rr=read(BASE/'sets/regression-bank.jsonl');changed={r['key']:i for i,r in enumerate(rr)};cv=np.load(BASE/'vectors/regression-changed.npy') if rr else np.zeros((0,768))
 from common import metrics as graded_metrics
 for name in SETS:
  p=ROOT/'bench/eg2-patch/sets'/name;ds=read(p/'docs.jsonl');qs=read(p/'queries.jsonl');rels=json.loads((p/'qrels.json').read_text())
  d=head(norm(raw[[idx[key(r['text'],'doc')] for r in ds]]));q=head(norm(raw[[idx[key(r['text'],'query')] for r in qs]]));scores={'head':q@d.T}
  for typ,a in fr['projection_doses'].items():
   for suffix in ['', '_random']:
    v=dirs[typ+suffix];scores[typ+suffix]=project(q,v,a if not suffix else abs(a))@project(d,v,a if not suffix else abs(a)).T
  for mode in ['plain','gloss','latin','translit']:
   dd=d.copy()
   for i,r in enumerate(ds):
    t=rewrite(r['text'],mode)
    if t!=r['text']:dd[i]=head(norm(cv[changed[key(t,'doc')]]))
   scores[mode]=q@dd.T
  mets={n:graded_metrics(s,ds,qs,rels) for n,s in scores.items()};groups=[r.get('target',r.get('article',r['id'])) for r in qs]
  out[name]=dict(queries=len(qs),docs=len(ds),scores={n:{k:float(m[k].mean()) for k in ['mrr10','ndcg10','recall10']} for n,m in mets.items()},differences={n:{k:ci(m[k],mets['head'][k],groups) for k in ['mrr10','ndcg10','recall10']} for n,m in mets.items() if n!='head'})
  np.savez_compressed(BASE/'results'/f'regression-{name}-per-query.npz',**{n+'/'+k:v for n,m in mets.items() for k,v in m.items()})
  print(name,out[name]['differences'],flush=True)
 save(BASE/'results/regression.json',out);log('Scored all frozen projection/rewriting candidates once against public FiQA full, wiki stress, SciFact, MIRACL RU/DE and Russian wiki. Read only public-prefix vector rows; mixed text banks were never opened.')
if __name__=='__main__':
 ap=argparse.ArgumentParser();ap.add_argument('action',choices=['prepare','run']);a=ap.parse_args();prepare() if a.action=='prepare' else run()
