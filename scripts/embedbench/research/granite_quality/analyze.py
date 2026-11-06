"""Exact retrieval, held-out calibration, and paired bootstrap comparisons."""
from common import *
from collections import Counter

REPORT={}; PER={}
def save():
    (BASE/'debug/results.json').write_text(json.dumps(REPORT,indent=2))
    np.savez_compressed(BASE/'debug/per_query_metrics.npz',**PER)
def summary(m):return {k:float(v.mean()) for k,v in m.items() if k!='top'}
def compare(a,b,groups=None):return {k:delta(a[k],b[k],groups) for k in a if k!='top'}
def wiki_fit(d):return ('photo' in d.get('article','').lower() or d.get('article') in ['Фотосинтез','光合成','光合作用'])
def qsplit(q,docs,qr):
    dm={d['id']:d for d in docs}
    return np.array([all(wiki_fit(dm[x]) for x in qr[z['id']]) for z in q])
banks={m:bank(m) for m in ['granite','qwen']}
stored={}
for name in ['wiki','wiki-mix','wiki-fold','ru','fiqa10k']:
    docs,qs,qr=loadset(name);res={}
    for model,b in banks.items():
        D=norm(vectors(b,docs));Q=norm(vectors(b,qs,'query'));s=Q@D.T
        r=metrics(s,docs,qs,qr)
        lg=np.log10(np.maximum([len(d['text']) for d in docs],1));xc=lg-lg.mean()
        r['length_slope']=s@xc/(xc@xc)
        # Fraction of retrieved short hits irrelevant: per-query short false-positive rate among 10.
        ids=np.array([d['id'] for d in docs]);short=np.array([len(d['text'])<80 for d in docs])
        r['irrelevant_short10']=np.array([sum(short[j] and ids[j] not in qr[q['id']] for j in t)/10 for q,t in zip(qs,r['top'])])
        res[model]=r
        for k,v in r.items():
            if k!='top':PER[f'{name}/{model}/{k}']=v
    grouped=[q.get('target',q['id']) for q in qs] if name=='ru' else None
    REPORT[name]={m:summary(r) for m,r in res.items()}
    REPORT[name]['granite_minus_qwen']=compare(res['granite'],res['qwen'],grouped)
    if name.startswith('wiki'):
        REPORT[name]['article_cluster_ndcg_delta']=delta(res['granite']['ndcg'],res['qwen']['ndcg'],[q['lang']+'/'+q['article'] for q in qs])
    REPORT[name]['slices']={}
    sizes={d['id']:len(d['text']) for d in docs}
    ix=[i for i,q in enumerate(qs) if all(sizes[x]<400 for x in qr[q['id']])]
    if ix:
        ar={k:v[ix] for k,v in res['granite'].items() if k!='top'};br={k:v[ix] for k,v in res['qwen'].items() if k!='top'}
        REPORT[name]['slices']['all_targets_short400']={'n':len(ix),'granite':summary(ar),'qwen':summary(br),'difference':compare(ar,br)}
    for lang in ['en','de','ru','ja','zh','ru-para','en-cross']:
        ix=[i for i,q in enumerate(qs) if q.get('lang',q.get('kind'))==lang]
        if not ix:continue
        ar={k:v[ix] for k,v in res['granite'].items() if k!='top'};br={k:v[ix] for k,v in res['qwen'].items() if k!='top'}
        REPORT[name]['slices'][lang]={'n':len(ix),'granite':summary(ar),'qwen':summary(br),'difference':compare(ar,br)}
    stored[name]=(docs,qs,qr,res)
    log(f'Exact baseline {name}: Granite {res["granite"]["ndcg"].mean():.4f}, Qwen {res["qwen"]["ndcg"].mean():.4f}; paired CI in results.json.')
    save()

# Fit all transforms on photosynthesis documents and queries ONLY; unrelated city/Python eval.
# FiQA remains an untouched out-of-domain test of these wiki-fitted transforms.
docs,qs,qr,_=stored['wiki-mix'];trainD=np.array([wiki_fit(d) for d in docs]);trainQ=qsplit(qs,docs,qr)
fit_texts={d['text'] for d,t in zip(docs,trainD) if t}
g=banks['granite'];D=norm(vectors(g,docs));Q=norm(vectors(g,qs,'query'))
mu=D[trainD].mean(0);X=D[trainD]-mu
_,sv,vt=np.linalg.svd(X,full_matrices=False)
energy=(D[trainD]**2).mean(0);dimorder=np.argsort(-energy)
variance=X.var(0)
scales={}
for beta in [.25,.5]:
    scales['diag_var_'+str(beta)]=(variance+.1*variance.mean())**(-beta)
    scales['diag_rms_'+str(beta)]=(energy+.1*energy.mean())**(-beta)
for k in scales:scales[k]/=np.sqrt((scales[k]**2).mean())
np.savez(BASE/'debug/calibration.npz',mean=mu,pcs=vt[:16],dimension_order=dimorder,**scales)
lg=np.log10(np.maximum([len(d['text']) for d in docs],1))
xc=lg[trainD]-lg[trainD].mean();slope=float((Q[trainQ]@D[trainD].T@xc/(xc@xc)).mean())
params={'fit_docs':int(trainD.sum()),'fit_queries':int(trainQ.sum()),'length_slope':slope,'top_energy_dimensions':dimorder[:16].tolist()}
REPORT['calibration']=params
fdocs,fqs,_=loadset('flores-dev')
FD=norm(vectors(g,fdocs));FQ=norm(vectors(g,fqs,'query'))
langd={l:FD[[d['lang']==l for d in fdocs]].mean(0)-FD.mean(0) for l in ['en','de','ru']}
langq={l:FQ[[q['lang']==l for q in fqs]].mean(0)-FQ.mean(0) for l in ['en','de','ru']}

names=['baseline','query_center_0.5','doc_center_0.5','both_center_0.5','both_center_1','pc1','pc3','pc8','drop1','drop8','mrl128','mrl256','mrl512','length_logfit','short_penalty_0.01','short_penalty_0.03','mean','mean_content','layer10','layer16','layer19','layer21']
names+=list(scales)
names+=['language_offsets']
names+=['mean_docs','mean_content_docs','mean_queries']
def get_scores(name,docs,qs):
    v=name if name.startswith('layer') or name in ['mean','mean_content'] else 'cls'
    dv={'mean_docs':'mean','mean_content_docs':'mean_content'}.get(name,v)
    qv='mean' if name=='mean_queries' else v
    d=norm(vectors(g,docs,variant=dv));q=norm(vectors(g,qs,'query',variant=qv))
    if 'center' in name:
        a=float(name.rsplit('_',1)[1])
        if name.startswith(('both','doc')):d=norm(d-a*mu)
        if name.startswith(('both','query')):q=norm(q-a*mu)
    elif name.startswith('pc'):
        u=vt[:int(name[2:])];d=norm(d-(d@u.T)@u);q=norm(q-(q@u.T)@u)
    elif name.startswith('drop'):
        ix=dimorder[:int(name[4:])];d[:,ix]=0;q[:,ix]=0;d=norm(d);q=norm(q)
    elif name.startswith('mrl'):
        k=int(name[3:]);d=norm(d[:,:k]);q=norm(q[:,:k])
    elif name in scales:
        d=norm(d*scales[name]);q=norm(q*scales[name])
    elif name=='language_offsets':
        for i,row in enumerate(docs):d[i]-=langd.get(row.get('lang'),0)
        for i,row in enumerate(qs):
            lang=row.get('lang',{'ru-para':'ru','en-cross':'en'}.get(row.get('kind')))
            q[i]-=langq.get(lang,0)
        d=norm(d);q=norm(q)
    s=q@d.T
    if name=='length_logfit':s-=slope*np.log10(np.maximum([len(x['text']) for x in docs],1))[None,:]
    if name.startswith('short_penalty'):s-=float(name.rsplit('_',1)[1])*np.array([len(x['text'])<80 for x in docs])[None,:]
    return s
REPORT['fixes']={}
fitdocs=[d for d,t in zip(docs,trainD) if t];fitqs=[q for q,t in zip(qs,trainQ) if t]
fitres={name:metrics(get_scores(name,fitdocs,fitqs),fitdocs,fitqs,qr) for name in names}
selected=max(names,key=lambda name:fitres[name]['ndcg'].mean())
REPORT['calibration']['selected_by_fit_ndcg']=selected
REPORT['calibration']['fit_ndcg']={name:float(r['ndcg'].mean()) for name,r in fitres.items()}
for setname in ['wiki','wiki-mix','wiki-fold','ru','fiqa10k']:
    docs,qs,qr,_=stored[setname]
    if setname!='fiqa10k':
        test=np.logical_not(qsplit(qs,docs,qr));qs=[q for q,t in zip(qs,test) if t]
        docs=[d for d in docs if not wiki_fit(d) and d['text'] not in fit_texts]
        present={d['id'] for d in docs}
        assert all(set(qr[q['id']])<=present for q in qs), 'A relevant test doc overlaps calibration text'
    groups=[q.get('target',q['id']) for q in qs] if setname=='ru' else None
    base=metrics(get_scores('baseline',docs,qs),docs,qs,qr)
    rr={}
    for name in names:
        r=metrics(get_scores(name,docs,qs),docs,qs,qr)
        rr[name]={'metrics':summary(r),'delta':compare(r,base,groups)}
        if setname.startswith('wiki'):
            rr[name]['article_cluster_ndcg_delta']=delta(r['ndcg'],base['ndcg'],[q['lang']+'/'+q['article'] for q in qs])
        rr[name]['slices']={}
        for lang in ['en','de','ru','ja','zh','ru-para','en-cross']:
            ix=[i for i,q in enumerate(qs) if q.get('lang',q.get('kind'))==lang]
            if ix:rr[name]['slices'][lang]={'n':len(ix),'ndcg':float(r['ndcg'][ix].mean()),'ndcg_delta':delta(r['ndcg'][ix],base['ndcg'][ix])}
        for k,v in r.items():
            if k!='top':PER[f'fix/{setname}/{name}/{k}']=v
    REPORT['fixes'][setname]={'n':len(qs),'docs':len(docs),'variants':rr}
    log(f'Held-out fixes evaluated on {setname}: {len(qs)} queries, {len(docs)} docs; fit-only selection={selected}.')
    save()

# Matched folding effect; original and folded held-out corpora, same query list.
REPORT['folding']={}
for model in banks:
    a=stored['wiki-fold'][3][model];b=stored['wiki'][3][model]
    REPORT['folding'][model]=compare({k:a[k] for k in ['ndcg','mrr10','short80']},{k:b[k] for k in ['ndcg','mrr10','short80']})
REPORT['distractor_effect']={m:compare({k:stored['wiki-mix'][3][m][k] for k in ['ndcg','mrr10','short80']},{k:stored['wiki'][3][m][k] for k in ['ndcg','mrr10','short80']}) for m in banks}
REPORT['folding_heldout_granite']={k:delta(PER['fix/wiki-fold/baseline/'+k],PER['fix/wiki/baseline/'+k]) for k in ['ndcg','mrr10','short80']}
save()
