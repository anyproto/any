"""Geometry on final vectors; paired resampling uses identical public observations."""
from common import *
R={};banks={m:bank(m) for m in ['granite','qwen']}
def nonlinear_ci(a,b,fn,n=500):
    rng=np.random.default_rng(392); vals=[]
    for _ in range(n):
        ix=rng.integers(len(a),size=len(a));vals.append(fn(a[ix])-fn(b[ix]))
    return dict(delta=float(fn(a)-fn(b)),lo=float(np.quantile(vals,.025)),hi=float(np.quantile(vals,.975)),replicates=n)
def skew(x):
    x=x-x.mean();return float((x**3).mean()/max((x*x).mean(),1e-12)**1.5)
def occurrence(top,n):return skew(np.bincount(top.ravel(),minlength=n))
def boot_hubs(a,b,n,rep=500):
    rng=np.random.default_rng(393);vals=[]
    for _ in range(rep):
        ix=rng.integers(len(a),size=len(a));vals.append(occurrence(a[ix],n)-occurrence(b[ix],n))
    return dict(delta=occurrence(a,n)-occurrence(b,n),lo=float(np.quantile(vals,.025)),hi=float(np.quantile(vals,.975)))
for setname in ['wiki-mix','fiqa10k']:
    docs,qs,qr=loadset(setname);res={};Ds={};tops={};pcs={};concs={}
    for model,b in banks.items():
        raw=vectors(b,docs);D=norm(raw);Q=norm(vectors(b,qs,'query'));Ds[model]=D
        top=np.argsort(-(Q@D.T),axis=1)[:,:10];tops[model]=top
        # Fixed public calibration axes; evaluate variance on the target corpus.
        cal,_,_=loadset('flores-dev');C=norm(vectors(b,cal));mu=C.mean(0)
        _,sv,vt=np.linalg.svd(C-mu,full_matrices=False)
        X=D-D.mean(0);p=(X@vt[:10].T)**2;tot=(X*X).sum(1)
        eig=np.linalg.eigvalsh(X.T@X)[::-1]
        pcs[model]=np.column_stack((p[:,:1].sum(1),p[:,:5].sum(1),p.sum(1),tot))
        e=D*D;conc=np.sort(e,axis=1)[:,-8:].sum(1);concs[model]=conc
        short=np.array([len(d['text'])<80 for d in docs]);central=D@norm(D.mean(0))
        # No causal inference from a marginal length correlation.
        res[model]=dict(mean_norm=float(np.linalg.norm(D.mean(0))),raw_norm_mean=float(np.linalg.norm(raw,axis=1).mean()),raw_norm_sd=float(np.linalg.norm(raw,axis=1).std()),top1_cal_pc_variance=float(p[:,:1].sum()/tot.sum()),top5_cal_pc_variance=float(p[:,:5].sum()/tot.sum()),top10_cal_pc_variance=float(p.sum()/tot.sum()),k10_skew=occurrence(top,len(docs)),max_k10_occurrence=int(np.bincount(top.ravel(),minlength=len(docs)).max()),top8_coordinate_energy=float(conc.mean()),short_centrality=float(central[short].mean()),long_centrality=float(central[~short].mean()),short_raw_norm=float(np.linalg.norm(raw[short],axis=1).mean()),long_raw_norm=float(np.linalg.norm(raw[~short],axis=1).mean()))
        res[model]['corpus_pca_variance']={str(k):float(eig[:k].sum()/eig.sum()) for k in [1,5,10]}
    diff={'mean_norm':nonlinear_ci(Ds['granite'],Ds['qwen'],lambda x:np.linalg.norm(x.mean(0))),
          'k10_skew':boot_hubs(tops['granite'],tops['qwen'],len(docs)),
          'top8_coordinate_energy':delta(concs['granite'],concs['qwen'])}
    for i,k in enumerate(['top1','top5','top10']):diff[k+'_cal_pc_variance']=nonlinear_ci(pcs['granite'],pcs['qwen'],lambda x:x[:,i].sum()/x[:,3].sum())
    R[setname]={'models':res,'granite_minus_qwen':diff}
    log(f'Geometry complete for {setname}; descriptive values and paired bootstrap differences saved.')

train,_,_=loadset('flores-dev');test,_,_=loadset('flores-devtest');langs=['en','de','ru'];N=len(test)//3
R['flores']={'directions':{},'language_geometry':{},'tokenization':{}}
idx={l:np.array([i for i,d in enumerate(test) if d['lang']==l]) for l in langs}
tridx={l:np.array([i for i,d in enumerate(train) if d['lang']==l]) for l in langs}
cache={}
for model,b in banks.items():
    D=norm(vectors(b,test));Q=norm(vectors(b,test,'query'));Dt=norm(vectors(b,train));Qt=norm(vectors(b,train,'query'))
    globalD=Dt.mean(0);globalQ=Qt.mean(0);dc=D.copy();qc=Q.copy()
    for lang in langs:
        dc[idx[lang]]=norm(D[idx[lang]]-(Dt[tridx[lang]].mean(0)-globalD))
        qc[idx[lang]]=norm(Q[idx[lang]]-(Qt[tridx[lang]].mean(0)-globalQ))
    cache[model]=(D,Q,dc,qc)
    # Every language covers the exact same topics: offsets are not topic prevalence.
    offsets={l:float(np.linalg.norm(Dt[tridx[l]].mean(0)-globalD)) for l in langs}
    sims=D@D.T;topics=np.array([d['topic'] for d in test]);language=np.array([d['lang'] for d in test])
    sims[topics[:,None]==topics[None,:]]=-np.inf
    top=np.argsort(-sims,axis=1)[:,:10]
    shares=(language[top]==language[:,None]).mean(1)
    margins=[]
    for i in range(len(test)):
        same=(language==language[i])&(topics!=topics[i]);other=(language!=language[i])&(topics!=topics[i])
        margins.append(sims[i,same].mean()-sims[i,other].mean())
    R['flores']['language_geometry'][model]={'dev_language_offset_norm':offsets,'same_language_neighbor_share':float(shares.mean()),'same_language_similarity_advantage':float(np.mean(margins))}
    cache[model]+= (shares,np.array(margins))
    counts=np.load(BASE/'vectors'/model/'token_lengths.npy');bidx=b[0]
    for l in langs:
        xx=[test[i] for i in idx[l]];tt=counts[[bidx[key(r['text'],'doc')] for r in xx]]
        R['flores']['tokenization'].setdefault(l,{})[model]={'mean_tokens':float(tt.mean()),'tokens_per_100_chars':float(np.mean(tt/np.array([len(r['text']) for r in xx])*100))}
        cache[(model,l)]=tt
for l in langs:R['flores']['tokenization'][l]['granite_minus_qwen']=delta(cache[('granite',l)],cache[('qwen',l)],groups=[test[i]['source'] for i in idx[l]])
for k,j in [('same_language_neighbor_share',4),('same_language_similarity_advantage',5)]:
    R['flores']['language_geometry'].setdefault('granite_minus_qwen',{})[k]=delta(cache['granite'][j],cache['qwen'][j],groups=[r['source'] for r in test])
for source in langs:
    for target in langs:
        if source==target:continue
        out={};vals={}
        for model in banks:
            D,Q,dc,qc,*_=cache[model]
            for variant,dd,qq in [('base',D,Q),('language_offset',dc,qc)]:
                ss=qq[idx[source]]@dd[idx[target]].T
                rank=1+np.sum(ss>np.diag(ss)[:,None],axis=1)
                vals[(model,variant)]={'mrr10':np.where(rank<=10,1/rank,0),'recall1':(rank==1).astype(float),'positive_cosine':np.diag(ss)}
            out[model]={'base':{k:float(v.mean()) for k,v in vals[(model,'base')].items()},'offset_delta':{k:delta(vals[(model,'language_offset')][k],v,groups=[test[i]['source'] for i in idx[source]]) for k,v in vals[(model,'base')].items()}}
        out['granite_minus_qwen']={k:delta(v,vals[('qwen','base')][k],groups=[test[i]['source'] for i in idx[source]]) for k,v in vals[('granite','base')].items()}
        R['flores']['directions'][source+'->'+target]=out
(BASE/'debug/geometry.json').write_text(json.dumps(R,indent=2))
log('Finished final-vector geometry, aligned language offsets, translation retrieval, token fragmentation, and paired CIs.')
