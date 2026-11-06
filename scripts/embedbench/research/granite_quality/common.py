"""Public-only experiment paths, deterministic identities, and statistics."""
import os
from pathlib import Path
ROOT = Path(__file__).resolve().parents[4]
BASE = ROOT / 'bench/granite-quality'
os.environ.setdefault('HF_HOME', str(BASE / 'cache/hf'))
os.environ.setdefault('TMPDIR', str(BASE / 'tmp'))
os.environ.setdefault('TOKENIZERS_PARALLELISM', 'false')
os.environ.setdefault('HF_HUB_OFFLINE', '1')
os.environ.setdefault('OMP_NUM_THREADS', '2')
os.environ.setdefault('OPENBLAS_NUM_THREADS', '2')
import json, hashlib
import numpy as np

def read(path):
    with Path(path).open() as f: return [json.loads(x) for x in f]
def write(path, rows):
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    Path(path).write_text(''.join(json.dumps(x, ensure_ascii=False)+'\n' for x in rows))
def log(s):
    with (BASE/'debug/PROGRESS.md').open('a') as f: f.write('- '+s+'\n')
    print(s, flush=True)
def key(text, role): return hashlib.sha256((role+'\0'+text).encode()).hexdigest()
def norm(x): return x / np.maximum(np.linalg.norm(x, axis=-1, keepdims=True), 1e-12)
def delta(a, b, groups=None, n=4000):
    d=np.asarray(a,dtype=float)-np.asarray(b,dtype=float)
    rng=np.random.default_rng(391)
    if groups is None:
        boot=np.concatenate([d[rng.integers(len(d),size=(min(200,n-i),len(d)))].mean(1) for i in range(0,n,200)])
    else:
        g=np.asarray(groups); u=np.unique(g)
        sums=np.array([d[g==v].sum() for v in u]); counts=np.array([(g==v).sum() for v in u])
        inds=rng.integers(len(u),size=(n,len(u)))
        boot=sums[inds].sum(1)/counts[inds].sum(1)
    return dict(delta=float(d.mean()),lo=float(np.quantile(boot,.025)),hi=float(np.quantile(boot,.975)),n=len(d))
def loadset(name):
    p=BASE/'sets'/name
    return read(p/'docs.jsonl'),read(p/'queries.jsonl'),json.loads((p/'qrels.json').read_text())
def bank(model):
    p=BASE/'vectors'/model
    rows=read(p/'records.jsonl'); arrays=np.load(p/'vectors.npz')
    return {r['key']:i for i,r in enumerate(rows)},arrays
def vectors(b, rows, role='doc', variant='cls'):
    idx, arrays=b
    return arrays[variant][[idx[key(r['text'],role)] for r in rows]]
def metrics(s,docs,queries,qrels):
    top=np.argsort(-s,axis=1,kind='stable')[:,:10]
    ids=np.array([d['id'] for d in docs]); nd=[]; rr=[]
    discount=1/np.log2(np.arange(10)+2)
    for t,q in zip(top,queries):
        rel=set(qrels[q['id']]); h=np.array([v in rel for v in ids[t]])
        nd.append((h*discount).sum()/discount[:min(10,len(rel))].sum())
        hits=np.flatnonzero(h);rr.append(1/(hits[0]+1) if len(hits) else 0)
    short=np.array([len(d['text'])<80 for d in docs]); ho=np.array([d.get('heading_only',False) for d in docs])
    noise=np.array([d.get('distractor',False) for d in docs])
    return dict(ndcg=np.array(nd),mrr10=np.array(rr),short80=short[top].mean(1),heading=ho[top].mean(1),noise=noise[top].mean(1),top=top)
