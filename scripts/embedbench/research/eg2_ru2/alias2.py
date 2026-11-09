"""Training-free sparse token_embd row aliases; PLE derives from the edited rows."""
from util2 import *
from pipeline2 import G,forms,ACTIVE
def discover(tok):
 candidates={};audit=[]
 for gi,g in enumerate(G):
  variants=[]
  for spelling in sorted(forms(g['lemma'])):
   for text in [spelling,' '+spelling]:
    enc=tok.encode(text,add_special_tokens=False);variants.append(dict(form=text,tokens=enc.tokens,ids=enc.ids))
    if gi in ACTIVE and not g['ambiguous'] and len(enc.ids)==1 and len(spelling)>=4:
     candidates.setdefault(enc.ids[0],[]).append((g['en'],g['lemma'],enc.tokens[0]))
  audit.append(dict(lemma=g['lemma'],en=g['en'],ambiguous=g['ambiguous'],forms=variants))
 accepted=[]
 for tid,values in sorted(candidates.items()):
  sources={v[0] for v in values}
  if len(sources)!=1:continue
  en=next(iter(sources));sourceids=tok.encode(' '+en,add_special_tokens=False).ids
  if not sourceids:continue
  accepted.append(dict(id=tid,en=en,source_ids=sourceids,lemmas=sorted({v[1] for v in values}),token=values[0][2]))
 save(BASE/'dev/tokenization.json',audit);save(CODE/'alias_spec.json',accepted)
 return accepted
def make_rows(model):
 import mlx.core as mx
 spec=discover(model.tok);ids=np.array([g['id'] for g in spec],dtype=np.int32)
 w=model.original['token_embd.weight'];orig=np.array(w[mx.array(ids)]);target=np.array([np.array(w[mx.array(g['source_ids'])]).mean(0) for g in spec]);target=norm(target)*np.linalg.norm(orig,axis=1,keepdims=True)
 rng=np.random.default_rng(392);z=rng.standard_normal(orig.shape).astype(np.float32);unit=norm(orig);z=norm(z-(z*unit).sum(1,keepdims=True)*unit)
 out=dict(ids=ids,original=orig);audit={}
 for dose in [.25,.5,1.]:
  moved=norm((1-dose)*orig+dose*target)*np.linalg.norm(orig,axis=1,keepdims=True)
  c=np.clip((norm(moved)*unit).sum(1,keepdims=True),-1,1)
  random=(c*unit+np.sqrt(np.maximum(0,1-c*c))*z)*np.linalg.norm(orig,axis=1,keepdims=True)
  out['alias_'+str(dose)]=moved;out['random_'+str(dose)]=random
  audit[str(dose)]=dict(row_count=len(ids),alias_displacement=float(np.linalg.norm(moved-orig)),random_displacement=float(np.linalg.norm(random-orig)),max_row_displacement_error=float(np.max(np.abs(np.linalg.norm(moved-orig,axis=1)-np.linalg.norm(random-orig,axis=1)))),max_final_norm_error=float(np.max(np.abs(np.linalg.norm(moved,axis=1)-np.linalg.norm(orig,axis=1)))))
  assert audit[str(dose)]['max_row_displacement_error']<1e-3
 np.savez(BASE/'dev/alias_rows.npz',**out);save(BASE/'dev/alias_norms.json',audit)
 log(f'Constructed {len(ids)} complete-word token row aliases and per-row norm/displacement matched random controls; PLE is recomputed by the unchanged shared-input graph.')
 return out
def apply(model,rows,dose,random=False):
 import mlx.core as mx
 model.restore();ids=mx.array(rows['ids']);base=model.original['token_embd.weight'];new=mx.array(rows[('random_' if random else 'alias_')+str(float(dose))]);model.weights['token_embd.weight']=base.at[ids].add(new-base[ids]);mx.eval(model.weights['token_embd.weight'])
def affected(model,rows,ids):
 ids=set(int(i) for i in ids);return [any(t in ids for t in model.ids(r['text'],r['role'])) for r in rows]
