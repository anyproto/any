"""Build deterministic public distractors and aligned FLORES subsets."""
from common import *
import tarfile, argparse
ap=argparse.ArgumentParser();ap.add_argument('--flores', action='store_true');a=ap.parse_args()
docs,qs,qr=loadset('wiki')
noise=[]; seen=set(d['text'] for d in docs)
templates={'en':['Discussion: {}','Any update on {}?','We should discuss {}.'],
 'de':['Diskussion: {}','Gibt es Neuigkeiten zu {}?','Wir sollten über {} sprechen.'],
 'ru':['Обсуждение: {}','Что нового по теме «{}»?','Надо обсудить: {}.'],
 'ja':['{}についての議論','{}について更新はありますか？'],
 'zh':['讨论：{}','关于{}有什么更新？']}
for d in docs:
    if not d['title']:continue
    for j,t in enumerate(templates[d['lang']]):
        text=t.format(d['title'])
        if text in seen or len(text)>=80:continue
        seen.add(text)
        noise.append(dict(id='noise-'+str(len(noise)),text=text,lang=d['lang'],article=d['article'],distractor=True,kind='chat' if j else 'title',heading_only=False))
p=BASE/'sets/wiki-mix';write(p/'docs.jsonl',docs+noise);write(p/'queries.jsonl',qs);(p/'qrels.json').write_text(json.dumps(qr))
log(f'Built wiki-mix with {len(noise)} unique <80-character public-heading-derived distractors; templates are synthetic, not real chat. {len(docs)+len(noise)} total docs.')
if a.flores:
    codes={'en':'eng_Latn','de':'deu_Latn','ru':'rus_Cyrl'}
    with tarfile.open(BASE/'tmp/flores200.tar.gz') as t:
        names=t.getnames()
        for split,count in [('dev',160),('devtest',240)]:
            rows=[]
            meta=next(n for n in names if n.endswith('/metadata_'+split+'.tsv'))
            sources=[x.split('\t')[0] for x in t.extractfile(meta).read().decode().splitlines()[1:count+1]]
            for lang,code in codes.items():
                name=next(n for n in names if n.endswith('/'+split+'/'+code+'.'+split))
                lines=t.extractfile(name).read().decode().splitlines()[:count]
                rows += [dict(id=f'{split}-{i}-{lang}',text=x,lang=lang,topic=i,split=split,source=sources[i]) for i,x in enumerate(lines)]
            p=BASE/'sets'/('flores-'+split);write(p/'docs.jsonl',rows);write(p/'queries.jsonl',rows)
            (p/'qrels.json').write_text(json.dumps({r['id']:[x['id'] for x in rows if x['topic']==r['topic'] and x['lang']!=r['lang']] for r in rows}))
    log('FLORES-200: first 160 aligned dev sentences/language reserved for calibration; first 240 devtest sentences/language for evaluation (EN/DE/RU). Translation retrieval is a diagnostic, not topical search.')
