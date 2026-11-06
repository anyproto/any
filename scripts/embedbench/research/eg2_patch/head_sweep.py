"""Separate head candidate, after writer tests; exact linear readout substitution.

Recover pooled 512-state from unnormalized baseline output with a pseudoinverse.
This is algebraic inference caching, not fitting/learning a head. Modified weights
are precisely BF16-rounded rank-one closed-form projections.
"""
from common import *
from edit_math import build_edit
from types import SimpleNamespace
import gguf

def substitute(v,w,new):
    inverse=np.linalg.pinv(w.T.astype(np.float64))
    h=v.astype(np.float64)@inverse
    error=np.linalg.norm(h@w.T-v,axis=1)/np.maximum(np.linalg.norm(v,axis=1),1e-12)
    assert error.max()<1e-5,'Readout inversion failed'
    return (h@new.T).astype(np.float32)

def main():
    reader=gguf.GGUFReader(str(ROOT/'bench/embeddinggemma-2-BF16.gguf'))
    w=next(gguf.dequantize(t.data,t.tensor_type).copy() for t in reader.tensors if t.name=='output.weight')
    model=SimpleNamespace(original={'output.weight':w});dirs=np.load(BASE/'dev/directions.npz')
    v=np.load(BASE/'dev/original.npy');ds,qs,qr=loadset('dev');base=np.load(BASE/'dev/original_metrics.npz')
    for kind in ['mean','contrast']:
        for a in [-.5,.25,.5,1.,1.5]:
            name=f'head-{kind}-{a}';fp=BASE/'dev'/f'{name}.json'
            if fp.exists():continue
            spec=dict(name=name,alpha=a,sites=[dict(tensor='output.weight',direction='head_'+kind,k=0)])
            ed,det=build_edit(model,spec,dirs);new=substitute(v,w,ed['output.weight'])
            m=metrics(norm(new[len(ds):])@norm(new[:len(ds)]).T,ds,qs,qr)
            ledger=read(BASE/'dev/passes.jsonl');pid=len(ledger)+1;assert pid<=50
            with (BASE/'dev/passes.jsonl').open('a') as f:f.write(json.dumps(dict(pass_id=pid,name=name,spec=spec,status='complete',method='exact_linear_head_substitution'))+'\n')
            out=dict(pass_id=pid,spec=spec,metrics={k:float(x.mean()) for k,x in m.items() if k!='top'},delta={k:delta(x,base[k]) for k,x in m.items() if k!='top'},tensors=det)
            save(fp,out);np.save(BASE/'dev'/f'{name}.npy',new);np.savez(BASE/'dev'/f'{name}-metrics.npz',**m)
            log(f'Dev head pass {pid} {name}: nDCG {out["metrics"]["ndcg10"]:.5f}; delta/CI {out["delta"]["ndcg10"]}.')

if __name__=='__main__':main()
