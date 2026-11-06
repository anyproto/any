"""Control for wiki qrels restricted to one language/article/section."""
from common import *
R={};banks={m:bank(m) for m in ['granite','qwen']}
for name in ['wiki','wiki-mix']:
    docs,qs,qr=loadset(name);results={};langs=np.array([d['lang'] for d in docs]);qlangs=np.array([q['lang'] for q in qs])
    for model,b in banks.items():
        s=norm(vectors(b,qs,'query'))@norm(vectors(b,docs)).T
        allr=metrics(s,docs,qs,qr);s[qlangs[:,None]!=langs[None,:]]=-np.inf
        mono=metrics(s,docs,qs,qr)
        results[model]=(allr,mono)
    R[name]={}
    for lang in ['all','en','de','ru','ja','zh']:
        ix=np.arange(len(qs)) if lang=='all' else np.flatnonzero(qlangs==lang)
        obj={}
        for model,(a,b) in results.items():
            cross=(langs[a['top'][:,0]]!=qlangs).astype(float)
            obj[model]={'all_ndcg':float(a['ndcg'][ix].mean()),'monolingual_ndcg':float(b['ndcg'][ix].mean()),'monolingual_mrr10':float(b['mrr10'][ix].mean()),'restrict_ndcg_delta':delta(b['ndcg'][ix],a['ndcg'][ix]),'restrict_mrr_delta':delta(b['mrr10'][ix],a['mrr10'][ix]),'foreign_language_top1':float(cross[ix].mean())}
        obj['monolingual_granite_minus_qwen']={k:delta(results['granite'][1][k][ix],results['qwen'][1][k][ix]) for k in ['ndcg','mrr10','short80']}
        ga=(langs[results['granite'][0]['top'][:,0]]!=qlangs).astype(float);qa=(langs[results['qwen'][0]['top'][:,0]]!=qlangs).astype(float)
        obj['foreign_language_top1_difference']=delta(ga[ix],qa[ix]);R[name][lang]=obj
(BASE/'debug/language_control.json').write_text(json.dumps(R,indent=2))
log('Language-restricted exact-search control completed: wiki qrels only credit a specified language/article/section, so semantically correct foreign-language passages can be false negatives. Control separates this evaluation artifact from multilingual capacity claims.')
