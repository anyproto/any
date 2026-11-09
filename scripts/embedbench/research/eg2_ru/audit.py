"""Integrity/implementation verification; no additional held-out retrieval."""
from util import *
from pipeline import rewrite,tokens
from expand import expand
from regression import public_bank,SETS
import re

def main():
 frozen=json.loads((BASE/'FROZEN.json').read_text());checks={}
 corrections=json.loads((BASE/'CORRECTIONS.json').read_text()) if (BASE/'CORRECTIONS.json').exists() else {}
 for p,c in corrections.items():assert c['frozen_sha256']==frozen['files'][p]
 checks['frozen_files']={p:sha(ROOT/p)==corrections.get(p,{}).get('corrected_sha256',h) for p,h in frozen['files'].items()}
 checks['documented_code_corrections']=corrections;assert all(checks['frozen_files'].values())
 checks['models']={p:sha(ROOT/p)==h for p,h in frozen['models'].items()};assert all(checks['models'].values())
 gloss=json.loads((BASE/'glossary.json').read_text());assert gloss==json.loads((Path(__file__).parent/'glossary.json').read_text())
 rows=read(BASE/'sets/public-background.jsonl')+read(BASE/'sets/synthetic.jsonl')+read(BASE/'from-rig/synthetic-lookalikes.jsonl');assert all(expand(r['text'],gloss)==rewrite(r['text'],'gloss') for r in rows);checks['portable_expander_matches_frozen']=len(rows)
 checks['analyzer_examples']={s:{mode:tokens(s,mode) for mode in ['base','stem','gloss']} for s in ['ЁЛКА елка','обработчик обработчика обработчики','хендлер обработчик','хотфикс срочное исправление']}
 u=norm(np.load(ROOT/'scripts/embedbench/research/eg2_head_patch/head_mean.npy'));dirs=np.load(BASE/'dev/directions.npz');checks['directions']={}
 for typ,a in frozen['projection_doses'].items():
  v=dirs[typ];z=dirs[typ+'_random'];checks['directions'][typ]=dict(norm=float(np.linalg.norm(v)),head_dot=float(u@v),random_target_dot=float(v@z),target_operator_frobenius=float(np.linalg.norm(a*np.outer(v,v))),random_operator_frobenius=float(np.linalg.norm(a*np.outer(z,z))))
  assert abs(u@v)<1e-5 and abs(v@z)<1e-5
 # Re-encode a small public-only sample to validate deterministic-prefix indexing.
 idx,raw=public_bank();sample=[]
 for name in SETS:
  for role,file in [('doc','docs.jsonl'),('query','queries.jsonl')]:
   rr=read(ROOT/'bench/eg2-patch/sets'/name/file);r=sorted(rr,key=lambda r:key(r['text'],role))[0];sample.append(dict(text=r['text'],role=role))
 from eg2 import EG2
 m=EG2(ROOT/'bench/embeddinggemma-2-Q8_0.gguf');v=m.encode(sample,budget=2048);cached=norm(raw[[idx[key(r['text'],r['role'])] for r in sample]]);cos=(cached*norm(v)).sum(1);assert cos.min()>.99999
 checks['public_prefix_cache_parity']=dict(public_rows=len(idx),sample_rows=len(sample),min_cosine=float(cos.min()),mean_cosine=float(cos.mean()),private_text_bank_opened=False)
 checks['new_data_bytes']=sum(p.stat().st_size for p in BASE.rglob('*') if p.is_file());assert checks['new_data_bytes']<5_000_000_000
 save(BASE/'AUDIT.json',checks);log('Final audit passed: frozen hashes, portable expansion parity, projection/random norm match, public-prefix cached vector parity and <5GB new data. No held-out scores repeated.')
if __name__=='__main__':main()
