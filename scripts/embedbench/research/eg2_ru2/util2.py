"""Round-2 paths; no import side effects outside the allowed output trees."""
import os,sys,json,hashlib,time,datetime
from pathlib import Path
ROOT=Path(__file__).resolve().parents[4];BASE=ROOT/'bench/eg2-ru2';CODE=Path(__file__).parent;OLD=ROOT/'bench/eg2-ru'
for k,v in {'TMPDIR':str(BASE/'tmp'),'HF_HOME':str(BASE/'cache/hf'),'PIP_CACHE_DIR':str(BASE/'cache/pip'),'PYTHONDONTWRITEBYTECODE':'1','OMP_NUM_THREADS':'2','OPENBLAS_NUM_THREADS':'2','TOKENIZERS_PARALLELISM':'false'}.items():os.environ[k]=v
sys.dont_write_bytecode=True
sys.path.extend([str(OLD/'deps'),str(ROOT/'scripts/embedbench/research/eg2_patch')])
import numpy as np
def read(p):
 with Path(p).open() as f:return [json.loads(s) for s in f if s.strip()]
def write(p,rows):
 p=Path(p);p.parent.mkdir(parents=True,exist_ok=True);p.write_text(''.join(json.dumps(x,ensure_ascii=False)+'\n' for x in rows))
def save(p,x):
 p=Path(p);p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(x,ensure_ascii=False,indent=2)+'\n')
def key(text,role):return hashlib.sha256((role+'\0'+text).encode()).hexdigest()
def sha(p):
 h=hashlib.sha256()
 with Path(p).open('rb') as f:
  for b in iter(lambda:f.read(8<<20),b''):h.update(b)
 return h.hexdigest()
def log(s):
 with (BASE/'PROGRESS.md').open('a') as f:f.write('- '+datetime.datetime.now(datetime.timezone.utc).isoformat()+': '+s+'\n')
 print(s,flush=True)
def norm(x):return x/np.maximum(np.linalg.norm(x,axis=-1,keepdims=True),1e-12)
def head(x):
 u=norm(np.load(ROOT/'scripts/embedbench/research/eg2_head_patch/head_mean.npy'));return norm(x-np.outer(x@u,u))
def ci(a,b,groups=None):
 d=np.asarray(a,dtype=float)-np.asarray(b,dtype=float);g=np.arange(len(d)) if groups is None else np.asarray(groups);u=np.unique(g)
 sums=np.array([d[g==v].sum() for v in u]);counts=np.array([(g==v).sum() for v in u]);rng=np.random.default_rng(391)
 ix=rng.integers(len(u),size=(10000,len(u)));boot=sums[ix].sum(1)/counts[ix].sum(1)
 return dict(delta=float(d.mean()),lo=float(np.quantile(boot,.025)),hi=float(np.quantile(boot,.975)),n=len(d),clusters=len(u))
def verify_seal():
 s=json.loads((BASE/'SEALED.json').read_text())
 for p,h in s['files'].items():assert sha(ROOT/p)==h,p
 assert sha(ROOT/s['background'])==s['background_sha256']

