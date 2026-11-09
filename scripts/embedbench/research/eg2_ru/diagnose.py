from evaluate import *
from pipeline import transliterate

def run(split):
 out=BASE/'results'/f'{split}-diagnostics.json';assert not out.exists()
 rows=[r for r in read(BASE/'sets/synthetic.jsonl') if r['split']==split];res={};retr={};lex={};dirs=dict(np.load(BASE/'dev/directions.npz'))
 for model in ['eg2','qwen']:
  bank=load_bank(split,model);ds,qs=dataset(split)
  for name,transform in ([('stock',lambda x:x),('head',head),('language',lambda x:project(head(x),dirs['language'],json.loads((BASE/'dev/selected.json').read_text())['projection_doses']['language']))] if model=='eg2' else [('qwen',lambda x:x)]):
   w=[re.search(r['pattern'],r['text'],re.I)[0] for r in rows]
   vec=lambda ts,role='doc':transform(vectors(bank,[dict(text=t) for t in ts],role))
   slang=vec(w);source=vec([r['en'] for r in rows]);plain=vec([r['ru'] for r in rows]);trans=vec([transliterate(t) for t in w]);sq=vec(w,'query')
   ru=vec([r['base_text'] for r in rows]);en=vec([r['english'] for r in rows]);formal=vec([rewrite(r['base_text'],'plain') for r in rows]);pairs={}
   for k,a,b in [('loan_plain',slang,plain),('loan_english',slang,source),('english_plain',source,plain),('translit_plain',trans,plain),('sentence_ru_en',ru,en),('sentence_ru_plain',ru,formal)]:pairs[k]=(a*b).sum(1)
   for k,a in [('meaning_from_loan',sq),('meaning_from_english',vec([r['en'] for r in rows],'query')),('meaning_from_translit',vec([transliterate(t) for t in w],'query'))]:
    order=np.argsort(-(a@plain.T),axis=1);rank=np.array([np.where(o==i)[0][0]+1 for i,o in enumerate(order)]);pairs[k+'_mrr']=1/rank
   mixed=[i for i,r in enumerate(rows) if '// debug' in r['text']]
   full=vec([rows[i]['text'] for i in mixed]);part=vec(['debug build, client state, pending check']*len(mixed));meaning=vec([rows[i]['english'] for i in mixed]);pairs['mixed_full_english_meaning']=(full*meaning).sum(1);pairs['mixed_full_english_tail']=(full*part).sum(1)
   lex[name]=pairs
   # Reverse direction: English documents are substituted in a separate corpus.
   english={r['id']:r['english'] for r in rows};revds=[dict(d,text=english.get(d['id'],d['text'])) for d in ds];rq=[q for q in qs if q['source_kind']=='synthetic' and q['kind']=='para']
   score=vec([q['text'] for q in rq],'query')@vec([d['text'] for d in revds]).T
   retr[name]=metrics(score,revds,rq)
 res['lexical']={n:{k:float(v.mean()) for k,v in p.items()} for n,p in lex.items()}
 res['lexical_differences']={n:{ref:{k:ci(v,lex[ref][k]) for k,v in p.items()} for ref in ['head','qwen'] if ref!=n} for n,p in lex.items()}
 res['counterfactual_differences']={n:{k:ci(p[k],p['loan_plain']) for k in ['english_plain','translit_plain']} for n,p in lex.items()}
 res['lexical_source_minus_loan']={n:ci(p['meaning_from_english_mrr'],p['meaning_from_loan_mrr']) for n,p in lex.items()}
 res['mixed_meaning_minus_tail']={n:ci(p['mixed_full_english_meaning'],p['mixed_full_english_tail']) for n,p in lex.items()}
 res['reverse']={n:{k:float(m[k].mean()) for k in METRICS} for n,m in retr.items()}
 res['reverse_differences']={n:{ref:{k:ci(m[k],retr[ref][k]) for k in METRICS} for ref in ['head','qwen'] if ref!=n} for n,m in retr.items()}
 save(out,res);np.savez_compressed(BASE/'results'/f'{split}-lexical-per-item.npz',**{n+'/'+k:v for n,p in lex.items() for k,v in p.items()})
 log(f'{split}: saved predefined lexical, matched-language and reverse-direction diagnostics; no candidate selection from held-out.')
if __name__=='__main__':
 ap=argparse.ArgumentParser();ap.add_argument('split',choices=['dev','test']);run(ap.parse_args().split)
