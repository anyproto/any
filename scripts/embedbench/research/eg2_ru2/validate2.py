"""Read-only cache/row-patch validation and scope audit after scoring."""
from util2 import *
from data2 import public_index,SETS,dataset
from cache2 import Bank
from dev_controls import controls
import gc
def main():
 verify_seal();fr=json.loads((BASE/'FROZEN.json').read_text());out={}
 corrections=json.loads((BASE/'CORRECTIONS.json').read_text()) if (BASE/'CORRECTIONS.json').exists() else {}
 out['frozen']={p:sha(ROOT/p)==corrections.get(p,{}).get('corrected_sha256',h) for p,h in fr['files'].items()};assert all(out['frozen'].values())
 original=json.loads((BASE/'ROUND1_READONLY.json').read_text());out['round1_unchanged']=all(sha(ROOT/p)==h for p,h in original.items());assert out['round1_unchanged']
 out['models']={p:sha(ROOT/p)==h for p,h in fr['models'].items()};assert all(out['models'].values())
 rows=[]
 for name in SETS:
  ds,qs,_=dataset(name)
  for role,rs in [('doc',ds),('query',qs)]:
   r=sorted(rs,key=lambda r:key(r['text'],role))[0];rows.append(dict(text=r['text'],role=role))
 import mlx.core as mx
 for model in ['eg2','qwen']:
  idx,raw,_=public_index(model)
  if model=='eg2':
   from eg2 import EG2
   m=EG2(ROOT/'bench/embeddinggemma-2-Q8_0.gguf')
  else:
   from qwen import Qwen
   m=Qwen()
  v=m.encode(rows,budget=2048);cached=norm(raw[[idx[key(r['text'],r['role'])] for r in rows]]);cos=(norm(v)*cached).sum(1);assert cos.min()>.99999
  out[model+'_public_cache_parity']=dict(rows=len(rows),public_prefix=len(idx),minimum_cosine=float(cos.min()),mean_cosine=float(cos.mean()))
  if model=='eg2':
   import alias2
   a=dict(np.load(BASE/'dev/alias_rows.npz'));alias2.apply(m,a,fr['weight_dose']);x=m.weights['token_embd.weight'][mx.array(a['ids'])];y=m.original['token_embd.weight'][mx.array(a['ids'])]
   def ple(z):
    v=z@m.weights['per_layer_model_proj.weight'].T;return mx.fast.rms_norm(v.reshape(-1,24,512),m.weights['per_layer_proj_norm.weight'],1e-6)
   d=ple(x)-ple(y);mx.eval(d)
   out['PLE']=dict(derivation='same edited token_embd rows -> per_layer_model_proj -> per_layer_proj_norm -> all 24 PLE gates',independent_PLE_token_table=False,changed_rows=len(a['ids']),projected_difference_norm=float(np.linalg.norm(np.array(d))))
   specs=json.loads((CODE/'alias_spec.json').read_text());idset=set(int(i) for i in a['ids']);out['common_slang_tokens']={}
   for s in ['хотфикс','мастер','ревью','хендлер','флакают','задеплоить','пиар','залупился','мок','рейс','коммит','патч']:
    e=m.tok.encode(' '+s,add_special_tokens=False);out['common_slang_tokens'][s]=dict(tokens=e.tokens,ids=e.ids,edited=bool(idset.intersection(e.ids)))
   devbank=Bank('eg2','dev');ds,qs=controls();probe=[]
   for r in ds+qs:
    role='query' if 'target' in r else 'doc';base=head(devbank.matrix([r],role))[0];entry=dict(text=r['text'],role=role,edited_token_ids=sorted(idset.intersection(m.ids(r['text'],role))),doses={})
    for dose in [.25,.5,1.]:
     entry['doses'][str(dose)]={tag:float(base@head(devbank.matrix([r],role,tag+str(dose)))[0]) for tag in ['W','R']}
    probe.append(entry)
   out['ordinary_english_probes']=probe
  del m;gc.collect();mx.clear_cache()
 out['new_bytes']=sum(p.stat().st_size for root in [BASE,CODE] for p in root.rglob('*') if p.is_file());assert out['new_bytes']<5_000_000_000
 save(BASE/'AUDIT.json',out);log('Final audit passed: model hashes, round-1 read-only hashes, sealed/frozen inputs, EG2/Qwen public-cache parity, shared PLE row edit and <5GB disk.')
if __name__=='__main__':main()
