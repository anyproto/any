from util2 import *
SETS=['fiqa-full','wiki-mix','scifact','miracl-ru-pool','miracl-de-pool','ru']
def dataset(name):
 if name in ['sealed','sealed-ru']:
  ds=read(OLD/'sets/public-background.jsonl')+read(BASE/'sets/sealed-docs.jsonl');qs=read(BASE/'sets/sealed-queries.jsonl');rel={q['id']:{q['target']:1} for q in qs}
  if name=='sealed-ru':
   excluded={q['target'] for q in qs if q['kind']=='english'};ds=[r for r in ds if r['id'] not in excluded];qs=[q for q in qs if q['kind']!='english'];rel={q['id']:rel[q['id']] for q in qs}
 elif name in ['dev','round1']:
  split='dev' if name=='dev' else 'test';ds=read(OLD/'results'/f'{split}-docs.jsonl');qs=read(OLD/'results'/f'{split}-queries.jsonl');rel={q['id']:{q['target']:1} for q in qs}
 elif name=='rig':
  assert (BASE/'FROZEN.json').exists();ds=read(OLD/'sets/rig-docs.jsonl');qs=read(OLD/'sets/rig-queries.jsonl');rel={q['id']:{q['target']:1} for q in qs}
 elif name=='new-rig':
  assert (BASE/'FROZEN.json').exists();ds=read(BASE/'sets/new-rig-docs.jsonl');qs=read(BASE/'sets/new-rig-queries.jsonl');rel={q['id']:{q['target']:1} for q in qs}
 else:
  assert name in SETS;p=ROOT/'bench/eg2-patch/sets'/name;ds=read(p/'docs.jsonl');qs=read(p/'queries.jsonl');rel=json.loads((p/'qrels.json').read_text())
 return sorted(ds,key=lambda r:r['id']),qs,rel
def public_index(model):
 p=ROOT/'bench/eg2-patch';folder='original' if model=='eg2' else 'qwen';run=json.loads((p/'vectors'/folder/'run.json').read_text());idx={};records=[]
 allowed={'wiki','wiki-mix','wiki-fold','ru','fiqa10k','fiqa-full','scifact','nfcorpus','miracl-ru-pool','miracl-de-pool'}
 for name in run['sets']:
  if name not in allowed:break
  for role,filename in [('doc','docs.jsonl'),('query','queries.jsonl')]:
   for r in read(p/'sets'/name/filename):
    k=key(r['text'],role)
    if k not in idx:idx[k]=len(idx);records.append(dict(text=r['text'],role=role,key=k))
 raw=np.load(p/'vectors'/folder/'raw.npy',mmap_mode='r');assert len(idx)==74612 and len(idx)<len(raw)
 return idx,raw,records
def clusters(qs,rels):
 """Queries connected by any relevant target are resampled together."""
 parent=list(range(len(qs)));seen={}
 def find(i):
  while parent[i]!=i:parent[i]=parent[parent[i]];i=parent[i]
  return i
 for i,q in enumerate(qs):
  rel=rels[q['id']];rel=dict.fromkeys(rel,1) if isinstance(rel,list) else rel
  for target,gain in rel.items():
   if gain<=0:continue
   if target in seen:parent[find(i)]=find(seen[target])
   seen[target]=i
 return [str(find(i)) for i in range(len(qs))]
