"""Small checks of paired statistics, qrels, vector integrity and split separation."""
from common import *
a=np.array([0.,.5,1.]);z=delta(a,a)
assert z['delta']==z['lo']==z['hi']==0
p=delta(a+.2,a);assert abs(p['delta']-.2)<1e-12 and abs(p['lo']-.2)<1e-12
q=delta(a,a+.2);assert abs(q['delta']+p['delta'])<1e-12
d=[dict(id=str(i),text='doc') for i in range(12)];qs=[dict(id='q',text='query')]
r=metrics(np.array([list(range(12,0,-1))]),d,qs,{'q':['1']})
assert r['mrr10'][0]==.5 and abs(r['ndcg'][0]-1/np.log2(3))<1e-12
for name in ['wiki','wiki-fold','wiki-mix','ru','fiqa10k','flores-dev','flores-devtest']:
    ds,qs,qr=loadset(name);ids={x['id'] for x in ds}
    assert len(ids)==len(ds)
    assert all(qr[q['id']] and set(qr[q['id']])<=ids for q in qs)
tr,_,_=loadset('flores-dev');te,_,_=loadset('flores-devtest')
assert not {d['source'] for d in tr}&{d['source'] for d in te}
for model in ['granite','qwen']:
    idx,arrays=bank(model)
    for k in arrays.files:
        x=arrays[k];assert len(x)==len(idx) and np.isfinite(x).all() and (np.linalg.norm(x,axis=1)>0).all(),(model,k)
    meta=json.loads((BASE/'vectors'/model/'run.json').read_text())
    if model=='qwen':assert min(meta['batch_single_cosines'])>.999
log('Validation passed: paired bootstrap identities, hand-calculated rank metrics, qrels completeness/unique IDs, FLORES article separation, finite nonzero vectors, Qwen batch/single agreement.')
