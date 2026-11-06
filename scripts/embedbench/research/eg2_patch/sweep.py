"""Closed-form, dev-only weight interventions. No optimizer or gradients."""
from common import *
from eg2 import EG2, mx
import argparse

from edit_math import build_edit

def run(spec,model,rows,ds,qs,qr,dirs,original):
    name=spec['name'];fp=BASE/'dev'/f'{name}.json'
    if fp.exists():return json.loads(fp.read_text())
    ledger=read(BASE/'dev/passes.jsonl');assert len(ledger)<50,'Dev pass budget exhausted'
    pid=len(ledger)+1;start=time.time()
    with (BASE/'dev/passes.jsonl').open('a') as f:f.write(json.dumps(dict(pass_id=pid,name=name,status='started',spec=spec))+'\n')
    ed,details=build_edit(model,spec,dirs);model.apply(ed)
    v=model.encode(rows);np.save(BASE/'dev'/f'{name}.npy',v)
    met=metrics(norm(v[len(ds):])@norm(v[:len(ds)]).T,ds,qs,qr)
    out=dict(pass_id=pid,spec=spec,metrics={k:float(x.mean()) for k,x in met.items() if k!='top'},delta={k:delta(x,original[k]) for k,x in met.items() if k!='top'},tensors=details,seconds=time.time()-start)
    np.savez(BASE/'dev'/f'{name}-metrics.npz',**met);save(fp,out)
    log(f'Dev pass {pid}: {name}: nDCG {out["metrics"]["ndcg10"]:.5f}; paired delta/CI {out["delta"]["ndcg10"]}; under80={out["metrics"]["short80"]:.3f}.')
    return out

def main():
    p=argparse.ArgumentParser();p.add_argument('phase',choices=['isolate','dose','sparse','custom','head']);p.add_argument('--specs');a=p.parse_args()
    dirs=np.load(BASE/'dev/directions.npz');loc=json.loads((BASE/'dev/localization.json').read_text())
    model=EG2();rows=read(BASE/'dev/records.jsonl');ds,qs,qr=loadset('dev');original=dict(np.load(BASE/'dev/original_metrics.npz'))
    if a.phase=='isolate':
        # First four observationally strongest distinct writer sites, plus top PLE.
        sites=sorted(loc['writers'],key=lambda x:-x['relative'])[:4]
        sites+=[max((x for x in loc['writers'] if x['kind']=='ple'),key=lambda x:x['relative'])]
        specs=[]
        for s in sites:
            for raw in [False,True]:
                direction=f'{s["kind"]}_raw/{s["layer"]}' if raw else f'residual/{s["layer"]}'
                specs.append(dict(name=f'iso-{s["layer"]}-{s["kind"]}-'+('raw' if raw else 'res'),alpha=1.,sites=[dict(tensor=s['tensor'],layer=s['layer'],direction=direction,columns=f'{s["kind"]}_columns/{s["layer"]}',k=0)]))
    elif a.phase=='head':
        specs=[dict(name=f'head-{kind}-{alpha}',alpha=alpha,sites=[dict(tensor='output.weight',direction='head_'+kind,k=0)]) for kind in ['mean','contrast'] for alpha in [-.5,.25,.5,1.,1.5]]
    else:specs=json.loads(Path(a.specs).read_text())
    for s in specs:run(s,model,rows,ds,qs,qr,dirs,original)

if __name__=='__main__':main()
