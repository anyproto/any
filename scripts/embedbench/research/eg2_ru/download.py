"""Stream first 6000 PUBLIC StackOverflow threads, retain a hash sample."""
from util import *
import urllib.request,io,re,html,datetime
import zstandard
url='https://huggingface.co/datasets/IlyaGusev/ru_stackoverflow/resolve/main/ru_stackoverflow.jsonl.zst'
pool=[]
def lic(ts):return 'CC-BY-SA-'+('2.5' if ts<1302220800 else '3.0' if ts<1525219200 else '4.0')
def add(t,r,typ,pid,author,ts):
 t=html.unescape(re.sub('<[^>]+>',' ',t));t=re.sub(r'\s+',' ',t).strip()
 if 100<=len(t)<=2000 and len(re.findall('[а-яА-ЯёЁ]',t))>=60:
  pool.append(dict(id=f'so-{typ}-{pid}',text=t,source=r['url'],author=author,timestamp=ts,license=lic(ts),formality='comment' if typ=='c' else 'qa',group=str(r['question_id'])))
with urllib.request.urlopen(url,timeout=60) as resp:
 with zstandard.ZstdDecompressor().stream_reader(resp) as stream:
  for i,line in enumerate(io.TextIOWrapper(stream)):
   if i>=6000:break
   r=json.loads(line);add(r['title']+' '+r['text_markdown'],r,'q',r['question_id'],r['author'],r['timestamp'])
   for c in r.get('comments',[]):add(c['text'],r,'c',c['comment_id'],c['author'],c['timestamp'])
   for a in r.get('answers',[]):
    add(a['text_markdown'],r,'a',a['answer_id'],a['author'],a['timestamp'])
    for c in a.get('comments',[]):add(c['text'],r,'c',c['comment_id'],c['author'],c['timestamp'])
   if i%1000==0:print(i,len(pool),flush=True)
pool=sorted({r['id']:r for r in pool}.values(),key=lambda x:key(x['id'],'sample'))
# 800 background passages plus 24 fixed public targets, independently of embeddings.
chosen=pool[:824];write(BASE/'sources/stackoverflow-sample.jsonl',chosen)
write(BASE/'sets/public-background.jsonl',chosen[:800]);write(BASE/'sets/public-targets.jsonl',chosen[800:])
save(BASE/'sources/provenance.json',dict(url=url,threads_scanned=6000,eligible=len(pool),retained=len(chosen),selection='SHA256(id), first 824',license_card='https://huggingface.co/datasets/IlyaGusev/ru_stackoverflow',license_authority='https://stackoverflow.com/help/licensing',revision_caveat='License inferred from creation timestamp; edits may have a later CC BY-SA license. Original URL retained.',changes='HTML unescape, tag stripping and whitespace collapse; no private chat source'))
log(f'Public source acquired: {len(chosen)} attributed Q&A/comment passages from 6000 threads; no full archive saved.')
