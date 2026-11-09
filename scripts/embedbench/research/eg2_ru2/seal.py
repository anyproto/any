"""One-time data seal; never imports a dictionary or model."""
import json, hashlib, re, datetime
from pathlib import Path
ROOT=Path(__file__).resolve().parents[4]
BASE=ROOT/'bench/eg2-ru2'
CODE=Path(__file__).parent
def save(p,x):p.write_text(json.dumps(x,ensure_ascii=False,indent=2)+'\n')
def write(p,rows):p.write_text(''.join(json.dumps(x,ensure_ascii=False)+'\n' for x in rows))
assert not (BASE/'SEALED.json').exists()
old=json.loads((ROOT/'scripts/embedbench/research/eg2_ru/glossary.json').read_text())
rows=[];docs=[];qs=[]
for line in (CODE/'sealed_scenarios.tsv').read_text().splitlines():
 if not line or line.startswith('#'):continue
 family,text,para,kw,specific,competitor=line.split('|');i=len(rows);rid=f'r2-{i:02}'
 known=any(re.fullmatch(g['pattern'],family,re.I) for g in old)
 row=dict(id=rid,family=family,text=text,para=para,kw=kw,specific=specific,competitor=competitor,round1_known=known,source='authored-before-dictionary',risk=i>=48)
 rows.append(row);docs.extend([dict(id=rid,text=text),dict(id=rid+'-competitor',text=competitor)])
 for kind in ['para','kw','specific']:qs.append(dict(id=rid+':'+kind,target=rid,group=rid,kind=kind,text=row[kind],family=family,round1_known=known,risk=row['risk']))
controls=[
('Рейс в Берлин перенесли на утро, вечерний отменён.','Когда вылетит самолёт в Берлин?','ordinary'),
('Он мок под дождём, куртку оставил сушиться у двери.','Почему одежду повесили возле входа?','ordinary'),
('Мастер починил часы, новые детали не понадобились.','Удалось ли отремонтировать часы без замены деталей?','ordinary'),
('Канарейка перестала петь после переезда клетки.','Что случилось с птицей после перемены места?','ordinary'),
('Пиар выставки поручили музею, рекламу уже оплатили.','Кто занимается продвижением выставки?','ordinary'),
('Лук нарезали заранее, морковь натрут перед ужином.','Какие овощи подготовили раньше остальных?','ordinary'),
('Патчи под глаза закончились, крем ещё есть.','Какое средство ухода нужно купить?','ordinary'),
('Мокрый снег растаял на тёплой дорожке.','Что произошло со снегом на нагретой поверхности?','ordinary'),
('Драгоценный камень выпал из оправы кольца.','Что потерялось из украшения?','ordinary'),
('Рейсом из Рима доставили коробку книг.','Как доставили книги из Италии?','ordinary'),
('Кастрюля остыла, суп можно убрать в холодильник.','Почему еду уже можно поставить на холод?','ordinary'),
('Фиксированная цена действует до конца месяца.','Как долго стоимость останется неизменной?','ordinary'),
('The upload completed but the remote file is empty.','Why is the uploaded file missing its contents?','english'),
('Canceling the download leaves the transfer running.','Does cancellation stop network traffic?','english'),
('The weekly backup contains rows but no indexes.','What must be rebuilt after restoring the backup?','english'),
('The menu refactor only moves code into smaller functions.','Did the menu gain any new features?','english'),
('The release is delayed until security approves the key dialog.','Whose sign-off blocks the release?','english'),
('The flight leaves tomorrow morning after the evening service was canceled.','When is the replacement flight?','english'),
('A wet jacket is drying beside the door.','Where is the rain-soaked coat?','english'),
('The tablet build uses an expired signing certificate.','Why can the tablet package not be installed?','english'),
('The cache keeps profile images after the user signs out.','Are avatars deleted when the session ends?','english'),
('A click handler was registered twice during navigation.','Why does one click trigger two callbacks?','english'),
('The payment service is updated after midnight; the storefront can ship now.','Which component can be deployed immediately?','english'),
('Batch updates keep card order while individual updates reset it.','Which update method preserves the sequence?','english')]
for i,(t,q,k) in enumerate(controls):
 rid=f'control-{i:02}';docs.append(dict(id=rid,text=t));qs.append(dict(id=rid,target=rid,group=rid,kind=k,text=q,family='control',round1_known=False,risk=False))
assert len(rows)>=64 and sum(not r['round1_known'] for r in rows)>=len(rows)/2
assert len({r['id'] for r in docs})==len(docs)
write(BASE/'sets/sealed-scenarios.jsonl',rows);write(BASE/'sets/sealed-docs.jsonl',docs);write(BASE/'sets/sealed-queries.jsonl',qs)
paths=[CODE/'sealed_scenarios.tsv',CODE/'seal.py',BASE/'PLAN.md']+sorted((BASE/'sets').glob('sealed-*.jsonl'))
save(BASE/'SEALED.json',dict(created=datetime.datetime.now(datetime.timezone.utc).isoformat(),files={str(p.relative_to(ROOT)):hashlib.sha256(p.read_bytes()).hexdigest() for p in paths},targets=len(rows),outside_round1=sum(not r['round1_known'] for r in rows),queries=len(qs),dictionary_acquired=False,models_scored=False,authorship='Same agent; prospective temporal/source separation, not independent human authorship',background='bench/eg2-ru/sets/public-background.jsonl',background_sha256=hashlib.sha256((ROOT/'bench/eg2-ru/sets/public-background.jsonl').read_bytes()).hexdigest()))
print((BASE/'SEALED.json').read_text())
