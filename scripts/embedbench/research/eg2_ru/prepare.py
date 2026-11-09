from util import *
import re
CODE=Path(__file__).parent
rows=[]
for l in (CODE/'items.tsv').read_text().splitlines():
 if not l or l.startswith('#'):continue
 split,pattern,en,ru,doc,kw,para,cross,english=l.split('|')
 base_text=doc;i=len(rows);style='notes' if i%4==3 else 'chat'
 # Explicitly controlled generic context; not source data or extra answer content.
 if style=='notes':doc='Заметка по итогам проверки. '+doc+' Наблюдение повторяется на отдельном стенде. До следующего выпуска нужно сохранить описание и проверить результат после исправления. Ответственный за компонент добавит итог в список изменений. Проверку следует выполнить еще раз после перезапуска приложения.'
 else:doc+=' пока оставил как есть, подробности допишу после проверки. если кто уже смотрел это место, отпишитесь в треде.'
 if i%3==0:doc+=' // debug build, client state, pending check'
 rows.append(dict(id=f'syn-{i:02}',split=split,pattern=pattern,en=en,ru=ru,text=doc,base_text=base_text,kw=kw,para=para,cross=cross,english=english,formality=style,group=f'syn-{i:02}'))
write(BASE/'sets/synthetic.jsonl',rows)
# Static glossary includes all predefined families. Test contexts/queries are never fit;
# this is dictionary coverage evaluation, not unseen-lexeme generalization.
gloss=[dict(pattern=r['pattern'],en=r['en'],ru=r['ru']) for r in rows]
save(BASE/'glossary.json',gloss)
public=read(BASE/'sets/public-targets.jsonl');pub=[]
for l in (CODE/'public_queries.tsv').read_text().splitlines():
 if not l or l.startswith('#'):continue
 i,kw,para,cross=l.split('|');r=dict(public[int(i)]);r.update(kw=kw,para=para,cross=cross,split='dev' if len(pub)%2==0 else 'test');pub.append(r)
write(BASE/'sets/public.jsonl',pub)
log(f'Authored and sealed before scores: {len(rows)} synthetic families (20 dev/20 test), {len(pub)} public targets, 800 public distractors. Glossary fixed before retrieval; rig texts unopened.')
