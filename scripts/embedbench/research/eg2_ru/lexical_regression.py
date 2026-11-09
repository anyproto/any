"""Frozen lexical candidate non-regression, no refitting and no model inference."""
from regression import *
from pipeline import tokens,STEM
from collections import Counter

def matrix(dt,qt):
 df=Counter(t for d in dt for t in d);n=len(dt);ln=np.array([sum(d.values()) for d in dt]);den=1.2*(.25+.75*ln/ln.mean());cache={}
 for t in {t for q in qt for t in q}:
  tf=np.array([d.get(t,0) for d in dt]);cache[t]=np.log(1+(n-df[t]+.5)/(df[t]+.5))*2.2*tf/(tf+den)
 return np.array([sum((cache[t] for t in set(q)),start=np.zeros(n)) for q in qt],dtype=np.float32)
def run():
 assert not (BASE/'results/lexical-regression.json').exists();idx,raw=public_bank();out={}
 from common import metrics as gm
 for name in SETS:
  p=ROOT/'bench/eg2-patch/sets'/name;ds=read(p/'docs.jsonl');qs=read(p/'queries.jsonl');rels=json.loads((p/'qrels.json').read_text());ruq=any(re.search('[а-яё]',q['text'],re.I) for q in qs);changed=any(rewrite(d['text'],'gloss')!=d['text'] for d in ds)
  if not ruq and not changed:
   zero=ci(np.zeros(len(qs)),np.zeros(len(qs)));out[name]=dict(method='Exact no-op: no Cyrillic query, no glossary-triggering document. Stem changes preserve token count and all Latin terms.',differences={n:{k:zero for k in ['mrr10','ndcg10','recall10']} for n in ['stem','gloss']});print(name,'exact no-op',flush=True);continue
  dt=[Counter(tokens(d['text'],'base')) for d in ds];qt=[tokens(q['text'],'base') for q in qs];stem=lambda t:STEM.stemWord(t.replace('ё','е')) if re.search('[а-яё]',t) else t
  cache={t:stem(t) for d in dt for t in d};sdt=[]
  for d in dt:
   c=Counter()
   for t,n in d.items():c[cache[t]]+=n
   sdt.append(c)
  sqt=[[stem(t) for t in q] for q in qt]
  gdt=[Counter(tokens(rewrite(d['text'],'gloss'),'stem')) if rewrite(d['text'],'gloss')!=d['text'] else sdt[i] for i,d in enumerate(ds)]
  q=head(norm(raw[[idx[key(r['text'],'query')] for r in qs]]));d=head(norm(raw[[idx[key(r['text'],'doc')] for r in ds]]));v=q@d.T
  ms={}
  for mode,dt0,qt0 in [('base',dt,qt),('stem',sdt,sqt),('gloss',gdt,sqt)]:ms[mode]=gm(fuse(v,matrix(dt0,qt0)),ds,qs,rels)
  groups=[r.get('target',r.get('article',r['id'])) for r in qs];out[name]=dict(method='Single-field BM25+equal RRF exact-vector head; same frozen analyzer parameters.',scores={n:{k:float(m[k].mean()) for k in ['mrr10','ndcg10','recall10']} for n,m in ms.items()},differences={n:{k:ci(ms[n][k],ms['base'][k],groups) for k in ['mrr10','ndcg10','recall10']} for n in ['stem','gloss']})
  np.savez_compressed(BASE/'results'/f'lexical-regression-{name}.npz',**{n+'/'+k:v for n,m in ms.items() for k,v in m.items()});print(name,out[name]['differences'],flush=True)
 save(BASE/'results/lexical-regression.json',out);log('Completed frozen Russian-stemming and glossary lexical non-regression on six public sets, with exact no-op proofs where applicable. No fitting or selection.')
if __name__=='__main__':run()
