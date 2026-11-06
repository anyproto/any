"""Matched heading/body geometry and inspectable public retrieval failures."""
from common import *
docs,qs,qr=loadset('wiki-mix');plain,_,_=loadset('wiki')
banks={m:bank(m) for m in ['granite','qwen']};stats={};allhits=[];contrasts={}
pairs=[(i,i+1) for i,(d,e) in enumerate(zip(plain,plain[1:])) if d['heading_only'] and not e['heading_only'] and d['article']==e['article'] and d['lang']==e['lang'] and len(d['text'])<80 and len(e['text'])>=200]
a=np.array([p[0] for p in pairs]);b=np.array([p[1] for p in pairs]);ids={d['id']:i for i,d in enumerate(docs)}
for model,bank_ in banks.items():
    raw=vectors(bank_,docs);D=norm(raw);Q=norm(vectors(bank_,qs,'query'));S=Q@D.T;mu=norm(D.mean(0));r=metrics(S,docs,qs,qr)
    e=np.sort(D*D,axis=1)[:,-8:].sum(1);nr=np.linalg.norm(raw,axis=1)
    body_mean=norm(D[[len(d['text'])>=200 and not d.get('distractor',False) for d in docs]].mean(0))
    c={'centrality':D@mu,'body_only_centroid_cosine':D@body_mean,'norm':nr,'top8_energy':e}
    stats[model]={'matched_pairs':len(pairs),'heading_minus_next_body':{k:delta(x[a],x[b]) for k,x in c.items()}}
    vals={};vals['best_irrelevant_short_minus_best_relevant']=[]
    vals['unrelated_length_slope']=[]
    for i,q in enumerate(qs):
        relevant=np.array([ids[x] for x in qr[q['id']]])
        short=np.array([len(d['text'])<80 and d['id'] not in qr[q['id']] for d in docs])
        vals['best_irrelevant_short_minus_best_relevant'].append(S[i,short].max()-S[i,relevant].max())
        other=np.array([d.get('article')!=q.get('article') for d in docs]);lg=np.log10(np.maximum([len(d['text']) for d in docs],1))[other];xc=lg-lg.mean()
        vals['unrelated_length_slope'].append(S[i,other]@xc/(xc@xc))
        for rank,j in enumerate(r['top'][i],1):
            allhits.append(dict(model=model,qid=q['id'],query=q['text'],rank=rank,score=float(S[i,j]),docid=docs[j]['id'],text=docs[j]['text'][:800],chars=len(docs[j]['text']),lang=docs[j]['lang'],relevant=docs[j]['id'] in qr[q['id']],distractor=docs[j].get('distractor',False),heading=docs[j].get('heading_only',False)))
    contrasts[model]={k:np.array(v) for k,v in vals.items()}
    stats[model]['mean_query_contrasts']={k:float(np.mean(v)) for k,v in vals.items()}
stats['granite_minus_qwen']={k:delta(v,contrasts['qwen'][k]) for k,v in contrasts['granite'].items()}
stats['distractor_density']={}
noise=[d for d in docs if d.get('distractor')]
for count in [0,100,400,len(noise)]:
    rng=np.random.default_rng(391)
    selected=[noise[i] for i in sorted(rng.choice(len(noise),size=count,replace=False))]
    dd=plain+selected;res={}
    for model,bk in banks.items():
        rr=metrics(norm(vectors(bk,qs,'query'))@norm(vectors(bk,dd)).T,dd,qs,qr)
        res[model]={k:rr[k] for k in ['ndcg','short80','noise','irrelevant_short10'] if k in rr}
    stats['distractor_density'][str(count)]={m:{k:float(v.mean()) for k,v in r.items()} for m,r in res.items()}
    stats['distractor_density'][str(count)]['granite_minus_qwen']={k:delta(v,res['qwen'][k]) for k,v in res['granite'].items()}
fd,_,_=loadset('fiqa10k');control=[dict(d,id='fiqa-'+d['id'],distractor=True) for d in fd if len(d['text'])<80]
rng=np.random.default_rng(391);control=[control[i] for i in rng.choice(len(control),size=min(len(control),300),replace=False)]
dd=plain+control;res={}
for model,bk in banks.items():
    rr=metrics(norm(vectors(bk,qs,'query'))@norm(vectors(bk,dd)).T,dd,qs,qr)
    res[model]={k:rr[k] for k in ['ndcg','short80','noise']}
stats['unrelated_short_fiqa_control']={'added_docs':len(control),**{m:{k:float(v.mean()) for k,v in r.items()} for m,r in res.items()},'granite_minus_qwen':{k:delta(v,res['qwen'][k]) for k,v in res['granite'].items()}}
(BASE/'debug/mechanism.json').write_text(json.dumps(stats,indent=2));write(BASE/'debug/public_top_hits.jsonl',allhits)
log('Saved public top-10 evidence and matched heading/body comparisons, with paired CIs for norm, centroid proximity, coordinate concentration, and query score margins.')
