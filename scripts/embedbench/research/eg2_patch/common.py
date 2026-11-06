"""Public-only paths, metrics and paired bootstrap; never contacts a local service."""
import os
from pathlib import Path
ROOT = Path(__file__).resolve().parents[4]
BASE = ROOT / 'bench/eg2-patch'
for key, value in {'TMPDIR': str(BASE/'tmp'), 'HF_HOME': str(BASE/'cache/hf'),
                   'TOKENIZERS_PARALLELISM': 'false', 'OMP_NUM_THREADS': '2',
                   'OPENBLAS_NUM_THREADS': '2', 'PYTHONDONTWRITEBYTECODE': '1'}.items():
    os.environ.setdefault(key, value)
import sys, json, hashlib, time
import numpy as np
sys.path.insert(0, str(ROOT/'bench/granite/llama.cpp-b11600/gguf-py'))

def read(p):
    with Path(p).open() as f: return [json.loads(x) for x in f]

def write(p, rows):
    p = Path(p); p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(''.join(json.dumps(r, ensure_ascii=False)+'\n' for r in rows))

def save(p, obj):
    Path(p).write_text(json.dumps(obj, ensure_ascii=False, indent=2)+'\n')

def log(s):
    with (BASE/'PROGRESS.md').open('a') as f: f.write('- '+time.strftime('%Y-%m-%d %H:%M:%S')+': '+s+'\n')
    print(s, flush=True)

def norm(x): return x / np.maximum(np.linalg.norm(x, axis=-1, keepdims=True), 1e-12)
def key(text, role): return hashlib.sha256((role+'\0'+text).encode()).hexdigest()
def sha(p):
    h = hashlib.sha256()
    with Path(p).open('rb') as f:
        for block in iter(lambda:f.read(8<<20), b''): h.update(block)
    return h.hexdigest()

def loadset(name):
    p=BASE/'sets'/name
    return read(p/'docs.jsonl'), read(p/'queries.jsonl'), json.loads((p/'qrels.json').read_text())

def delta(a,b,groups=None,n=4000):
    d=np.asarray(a,dtype=float)-np.asarray(b,dtype=float); rng=np.random.default_rng(391)
    if groups is None:
        boot=np.concatenate([d[rng.integers(len(d),size=(min(200,n-i),len(d)))].mean(1) for i in range(0,n,200)])
    else:
        g=np.asarray(groups); u=np.unique(g)
        sums=np.array([d[g==v].sum() for v in u]); counts=np.array([(g==v).sum() for v in u])
        ix=rng.integers(len(u),size=(n,len(u)));boot=sums[ix].sum(1)/counts[ix].sum(1)
    return {'delta':float(d.mean()),'lo':float(np.quantile(boot,.025)),'hi':float(np.quantile(boot,.975)),'n':len(d)}

def metrics(scores, docs, queries, qrels):
    top=np.argsort(-scores,axis=1,kind='stable')[:,:10]
    ids=np.array([d['id'] for d in docs]); lengths=np.array([len(d['text']) for d in docs])
    dis=1/np.log2(np.arange(top.shape[1])+2); nd=[];rr=[];rec=[]
    for t,q in zip(top,queries):
        rel=qrels[q['id']]
        if isinstance(rel,list):rel=dict.fromkeys(rel,1)
        gain=np.array([2**rel.get(i,0)-1 for i in ids[t]])
        ideal=np.sort([2**v-1 for v in rel.values() if v>0])[::-1][:10]
        nd.append(float(gain@dis/(ideal@dis[:len(ideal)])) if len(ideal) else 0.)
        hits=np.flatnonzero(gain>0);rr.append(1/(hits[0]+1) if len(hits) else 0.)
        rec.append(len(hits)/max(1,sum(v>0 for v in rel.values())))
    return dict(ndcg10=np.array(nd),mrr10=np.array(rr),recall10=np.array(rec),
                short80=(lengths[top]<80).mean(1),short400=(lengths[top]<400).mean(1),top=top)
