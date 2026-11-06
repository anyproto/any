"""One frozen-candidate evaluation, all metrics and paired CIs, no selection."""
from common import *
import argparse,gguf
from head_sweep import substitute

METRICS=['ndcg10','mrr10','recall10','short80','short400']
def bank(name):
    p=BASE/'vectors'/name
    return {r['key']:i for i,r in enumerate(read(p/'records.jsonl'))},np.load(p/'raw.npy')
def output_matrix(path):
    r=gguf.GGUFReader(str(path));return next(gguf.dequantize(t.data,t.tensor_type).copy() for t in r.tensors if t.name=='output.weight')
def summary(m):return {k:float(m[k].mean()) for k in METRICS}
def comparisons(a,b,groups=None):return {k:delta(a[k],b[k],groups) for k in METRICS}

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--sets',nargs='+',required=True);args=ap.parse_args()
    frozen=json.loads((BASE/'FROZEN.json').read_text());variants=frozen['variants'];banks={};allmetrics={};report={}
    for name,info in variants.items():
        if info['kind']=='bank':banks[name]=bank(info['bank'])
    baseidx,basev=banks['original'];w=output_matrix(ROOT/'bench/embeddinggemma-2-Q8_0.gguf')
    for name,info in variants.items():
        if info['kind']=='head':banks[name]=(baseidx,substitute(basev,w,output_matrix(ROOT/info['gguf'])))
    banks['centered']=banks['original'];mu=np.load(BASE/'dev/directions.npz')['centering_mean']
    for setname in args.sets:
        dest=BASE/'results'/f'{setname}.json'
        assert not dest.exists(),f'{setname} already scored; no repeated test peeking'
        ds,qs,qr=loadset(setname);res={};restricted={}
        for name,(idx,v) in banks.items():
            d=norm(v[[idx[key(r['text'],'doc')] for r in ds]])
            q=norm(v[[idx[key(r['text'],'query')] for r in qs]])
            if name=='centered':q=norm(q-.5*mu)
            score=q@d.T
            if setname=='rig-topical':
                for i,row in enumerate(qs):
                    excluded=np.array([d['id'] not in qr[row['id']] for d in ds])
                    assert (~excluded).sum()>=10
                    score[i,excluded]=-np.inf
            met=metrics(score,ds,qs,qr);res[name]=met
            if setname.startswith('wiki'):
                local=score.copy()
                for i,row in enumerate(qs):local[i,np.array([d['lang']!=row['lang'] for d in ds])]=-np.inf
                restricted[name]=metrics(local,ds,qs,qr)
            allmetrics.update({f'{setname}/{name}/{k}':v for k,v in met.items()})
        groups=[q['target'] for q in qs] if setname in ['ru','rig-known'] else None
        sr={'queries':len(qs),'docs':len(ds),'scores':{m:summary(r) for m,r in res.items()},'comparisons':{},'slices':{}}
        if restricted:
            sr['language_restricted']={'scores':{n:summary(r) for n,r in restricted.items()},'comparisons':{n:{ref:comparisons(r,restricted[ref]) for ref in ['original','qwen'] if ref!=n} for n,r in restricted.items()}}
        for name,r in res.items():
            sr['comparisons'][name]={ref:comparisons(r,res[ref],groups) for ref in ['original','qwen'] if ref!=name}
            if setname.startswith('wiki'):
                sr['comparisons'][name]['article_cluster_vs_original']=comparisons(r,res['original'],[q['lang']+'/'+q['article'] for q in qs])
        sr['target_minus_random']={name:comparisons(res[name],res[name+'_random'],groups) for name in ['writer','head']}
        sr['candidate_comparisons']={f'{a}_minus_{b}':comparisons(res[a],res[b],groups)
                                    for a,b in [('head','writer'),('head','centered'),('writer','centered')]}
        for field in ['lang','kind']:
            for label in sorted({q[field] for q in qs if field in q}):
                ii=[i for i,q in enumerate(qs) if q.get(field)==label]
                sub={name:{k:x[k][ii] for k in METRICS} for name,x in res.items()}
                gg=np.array(groups)[ii] if groups is not None else None
                sr['slices'][field+'/'+label]={'n':len(ii),'scores':{n:summary(r) for n,r in sub.items()},'comparisons':{n:{ref:comparisons(r,sub[ref],gg) for ref in ['original','qwen'] if ref!=n} for n,r in sub.items()}}
        # Protect useful short answers: target length subgroup, defined without scores.
        lengths={d['id']:len(d['text']) for d in ds};ii=[]
        for i,q in enumerate(qs):
            rel=qr[q['id']];positive=rel if isinstance(rel,list) else [x for x,v in rel.items() if v>0]
            if positive and all(lengths[x]<400 for x in positive):ii.append(i)
        if ii:
            sub={n:{k:r[k][ii] for k in METRICS} for n,r in res.items()}
            gg=np.array(groups)[ii] if groups is not None else None
            sr['slices']['all_positive_under400']={'n':len(ii),'scores':{n:summary(r) for n,r in sub.items()},'comparisons':{n:{ref:comparisons(r,sub[ref],gg) for ref in ['original','qwen'] if ref!=n} for n,r in sub.items()}}
        if setname=='rig-known':
            ranks=read(BASE/'from-rig/known-ranks.jsonl')
            misses={r['qid'] for r in ranks if r['model']=='qwen3' and r['mode']=='vector' and r.get('goldRank') is None}
            ii=[i for i,q in enumerate(qs) if q['id'] in misses]
            if ii:
                sub={n:{k:r[k][ii] for k in METRICS} for n,r in res.items()};gg=np.array(groups)[ii]
                sr['slices']['rig_qwen_vector_miss']={'n':len(ii),'scores':{n:summary(r) for n,r in sub.items()},'comparisons':{n:{ref:comparisons(r,sub[ref],gg) for ref in ['original','qwen'] if ref!=n} for n,r in sub.items()}}
        save(dest,sr);report[setname]=sr
        np.savez_compressed(BASE/'results'/f'{setname}-per-query.npz',**{f'{n}/{k}':x for n,r in res.items() for k,x in r.items()})
        log(f'Final frozen evaluation {setname}: '+', '.join(f'{n} nDCG={r["ndcg10"].mean():.4f}' for n,r in res.items())+'. All deltas and paired intervals saved; no retuning.')
    save(BASE/'results/all.json',report)

if __name__=='__main__':main()
