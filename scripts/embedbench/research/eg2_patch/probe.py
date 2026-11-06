"""Dev-only baseline and token-level localization of residual writers."""
from common import *
from eg2 import EG2, mx

def main():
    ds,qs,qr=loadset('dev');rows=[dict(d,role='doc') for d in ds]+[dict(q,role='query') for q in qs]
    model=EG2();v=model.encode(rows);np.save(BASE/'dev/original.npy',v)
    r=metrics(norm(v[len(ds):])@norm(v[:len(ds)]).T,ds,qs,qr)
    np.savez(BASE/'dev/original_metrics.npz',**r)
    write(BASE/'dev/records.jsonl',rows)
    with (BASE/'dev/passes.jsonl').open('a') as f:f.write(json.dumps(dict(pass_id=1,name='original',kind='retrieval',metrics={k:float(x.mean()) for k,x in r.items() if k!='top'}))+'\n')
    # Use matched headings and following answer-bearing bodies from dev articles.
    plain=[d for d in ds if not d.get('distractor')];pairs=[]
    for lang in ['en','de','ru','ja','zh']:
        eligible=[(a,b) for a,b in zip(plain,plain[1:]) if a.get('lang')==lang and a.get('heading_only') and not b.get('heading_only') and a['article']==b['article'] and len(a['text'])<80 and len(b['text'])>=200]
        rng=np.random.default_rng(391);rng.shuffle(eligible);pairs+=eligible[:8]
    # Also contrast actual top-ranked short non-answers against the best gold.
    scores=norm(v[len(ds):])@norm(v[:len(ds)]).T;dm={d['id']:i for i,d in enumerate(ds)}
    failures=[]
    for i,q in enumerate(qs):
        gold=max((dm[x] for x in qr[q['id']]),key=lambda j:scores[i,j])
        shorts=[j for j,d in enumerate(ds) if len(d['text'])<80 and d['id'] not in qr[q['id']]]
        bad=max(shorts,key=lambda j:scores[i,j])
        failures.append(dict(qid=q['id'],short=ds[bad]['id'],gold=ds[gold]['id'],margin=float(scores[i,bad]-scores[i,gold])))
    save(BASE/'dev/failures.json',failures)
    write(BASE/'dev/contrast_pairs.jsonl',[dict(short=a,body=b) for a,b in pairs])
    records=[dict(d,role='doc') for p in pairs for d in p]+[dict(id='empty-doc',text='',role='doc'),dict(id='empty-query',text='',role='query')]
    pooled={};rms_inputs={};bos={};prompt={};content={}
    for i,row in enumerate(records):
        ids=model.ids(row['text'],row['role']);cap={}
        def capture(kind,layer,x):cap[f'{layer}/{kind}']=x
        y=model.forward(mx.array([ids]),mx.array([len(ids)]),capture);mx.eval(y,cap)
        npre=len(model.ids('',row['role']))-1
        raw={k:np.array(x[0]) for k,x in cap.items()}
        # Keep complete per-token traces for one matched pair per language.
        lang=row.get('lang');keep=(i<len(pairs)*2 and next(j for j,p in enumerate(pairs) if p[0]['lang']==lang)==i//2)
        if keep:np.savez_compressed(BASE/'dev'/f'tokens-{i:03d}.npz',ids=np.array(ids),**raw)
        for k,x in raw.items():
            pooled.setdefault(k,[]).append(x.mean(0))
            bos.setdefault(k,[]).append(x[0])
            prompt.setdefault(k,[]).append(x[1:npre].mean(0))
            content.setdefault(k,[]).append(x[npre:-1].mean(0) if len(x)>npre+1 else x[-1])
            if k.endswith('_in'):rms_inputs.setdefault(k,[]).append(np.sqrt(np.mean(x*x,axis=0)))
        if i%10==0:print('Probe',i+1,'/',len(records),flush=True)
        del y,raw,cap;mx.clear_cache()
    maps={'pool':pooled,'bos':bos,'prompt':prompt,'content':content,'rms':rms_inputs}
    arrays={f'{name}/{k}':np.array(v) for name,m in maps.items() for k,v in m.items()}
    np.savez_compressed(BASE/'dev/activations.npz',**arrays)
    n=len(pairs);dirs={};report={'pairs':n,'layers':[],'writers':[]}
    for l in range(24):
        states=arrays[f'pool/{l}/residual'];s=states[:2*n:2];b=states[1:2*n:2]
        u=norm((norm(s)-norm(b)).mean(0));dirs[f'residual/{l}']=u
        mu=norm(states[:2*n].mean(0))
        report['layers'].append(dict(layer=l,contrast=delta(norm(s)@u,norm(b)@u),cos_mean=float(u@mu),cos_bos=float(u@norm(arrays[f'bos/{l}/residual'][:2*n].mean(0))),cos_prompt=float(u@norm(arrays[f'prompt/{l}/residual'][:2*n].mean(0)))))
        for kind,suffix in [('attn','attn_output'),('mlp','ffn_down'),('ple','proj')]:
            x=arrays[f'pool/{l}/{kind}_write'];raw=arrays[f'pool/{l}/{kind}_raw']
            uraw=norm((norm(raw[:2*n:2])-norm(raw[1:2*n:2])).mean(0));dirs[f'{kind}_raw/{l}']=uraw
            w=np.array(model.weights[f'blk.{l}.{suffix}.weight'])
            act=arrays[f'rms/{l}/{kind}_in'][:2*n].mean(0)
            power=abs(u@w)*act;order=np.argsort(-power);dirs[f'{kind}_columns/{l}']=order
            contrast=(x[:2*n:2]-x[1:2*n:2])@u
            entry=dict(layer=l,kind=kind,tensor=f'blk.{l}.{suffix}.weight',contrast=delta(x[:2*n:2]@u,x[1:2*n:2]@u),relative=float(np.mean(contrast)/np.mean(np.linalg.norm(states[:2*n],axis=1))),top64_power_share=float(power[order[:64]].sum()/power.sum()),top_columns=order[:64].tolist())
            if kind=='attn':
                hd=w.shape[1]//4;entry['head_power']=[float(power[h*hd:(h+1)*hd].sum()) for h in range(4)]
            report['writers'].append(entry)
    uv=norm(v[:len(ds)]);rng=np.random.default_rng(391)
    dirs['centering_mean']=uv[rng.choice(len(ds),100,replace=False)].mean(0)
    dirs['head_mean']=norm(uv.mean(0))
    pairs_idx=[(dm[a['id']],dm[b['id']]) for a,b in pairs]
    dirs['head_contrast']=norm(np.mean([uv[a]-uv[b] for a,b in pairs_idx],axis=0))
    np.savez(BASE/'dev/directions.npz',**dirs);save(BASE/'dev/localization.json',report)
    log(f'Dev baseline and {n} matched heading/body activation contrasts completed. Per-token traces kept for first pair per language; pooled, BOS, prompt, content and writer-input RMS summaries retained for every pair. One retrieval pass used.')

if __name__=='__main__':main()
