"""Isolated paths and IO; import before the read-only eg2_patch loaders."""
import os, sys, json, hashlib, time
from pathlib import Path
ROOT=Path(__file__).resolve().parents[4]
BASE=ROOT/'bench/eg2-ru'
for k,v in {'TMPDIR':str(BASE/'tmp'),'HF_HOME':str(BASE/'cache/hf'),'PIP_CACHE_DIR':str(BASE/'cache/pip'),'PYTHONDONTWRITEBYTECODE':'1','OMP_NUM_THREADS':'2','OPENBLAS_NUM_THREADS':'2','TOKENIZERS_PARALLELISM':'false'}.items():os.environ[k]=v
sys.dont_write_bytecode=True
sys.path.insert(0,str(BASE/'deps'))
sys.path.append(str(ROOT/'scripts/embedbench/research/eg2_patch'))
import numpy as np

def read(p):
 with Path(p).open() as f:return [json.loads(s) for s in f if s.strip()]
def write(p,x):
 p=Path(p);p.parent.mkdir(parents=True,exist_ok=True);p.write_text(''.join(json.dumps(r,ensure_ascii=False)+'\n' for r in x))
def save(p,x):Path(p).write_text(json.dumps(x,ensure_ascii=False,indent=2)+'\n')
def norm(x):return x/np.maximum(np.linalg.norm(x,axis=-1,keepdims=True),1e-12)
def key(text,role):return hashlib.sha256((role+'\0'+text).encode()).hexdigest()
def sha(p):
 h=hashlib.sha256()
 with open(p,'rb') as f:
  for b in iter(lambda:f.read(8<<20),b''):h.update(b)
 return h.hexdigest()
def log(s):
 with (BASE/'PROGRESS.md').open('a') as f:f.write('- '+time.strftime('%Y-%m-%d %H:%M:%S')+': '+s+'\n')
 print(s,flush=True)
def ci(a,b,groups=None):
 d=np.asarray(a,dtype=float)-np.asarray(b,dtype=float)
 if not len(d):return None
 g=np.arange(len(d)) if groups is None else np.asarray(groups);u=np.unique(g)
 sums=np.array([d[g==v].sum() for v in u]);counts=np.array([(g==v).sum() for v in u]);rng=np.random.default_rng(391)
 ix=rng.integers(len(u),size=(4000,len(u)));boot=sums[ix].sum(1)/counts[ix].sum(1)
 return dict(delta=float(d.mean()),lo=float(np.quantile(boot,.025)),hi=float(np.quantile(boot,.975)),n=len(d),clusters=len(u))
