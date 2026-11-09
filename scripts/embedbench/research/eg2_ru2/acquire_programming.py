"""Prescore source expansion driven by dev failure, never sealed coverage."""
from acquire import fetch
from util2 import *
import urllib.parse
def main():
 assert not (BASE/'FROZEN.json').exists();old=read(BASE/'sources/pages.jsonl');seen=[];queue=['Категория:Программирование/ru'];titles=set()
 while queue:
  cat=queue.pop(0)
  if cat in seen:continue
  seen.append(cat);params=dict(action='query',list='categorymembers',cmtitle=cat,cmlimit=500)
  while True:
   x=fetch(params)
   for p in x['query']['categorymembers']:
    if p['ns']==0:titles.add(p['title'])
    elif p['ns']==14 and p['title'].endswith('/ru'):queue.append(p['title'])
   if 'continue' not in x:break
   params.update(x['continue'])
 new=sorted(titles-{p['title'] for p in old});pages=[]
 for start in range(0,len(new),40):
  x=fetch(dict(action='query',prop='revisions',rvprop='ids|timestamp|content',rvslots='main',titles='|'.join(new[start:start+40])))
  for p in x['query']['pages']:
   if 'revisions' not in p:continue
   r=p['revisions'][0];pages.append(dict(title=p['title'],pageid=p['pageid'],revid=r['revid'],timestamp=r['timestamp'],wikitext=r['slots']['main']['content'],url='https://ru.wiktionary.org/w/index.php?oldid='+str(r['revid']),history='https://ru.wiktionary.org/w/index.php?title='+urllib.parse.quote(p['title'])+'&action=history',attribution='Wiktionary contributors',license='CC-BY-SA-4.0'))
  print('programming source',len(pages),len(new),flush=True)
 write(BASE/'sources/phase1-pages.jsonl',old);write(BASE/'sources/pages.jsonl',old+pages)
 p=json.loads((BASE/'sources/provenance.json').read_text());p.update(programming_categories=seen,programming_added=len(pages),pages=len(old)+len(pages),source_sha256=sha(BASE/'sources/pages.jsonl'));save(BASE/'sources/provenance.json',p)
 log(f'Added whole programming category ({len(pages)} new pages), based on dev-only failure of broad internet lexicon. No held-out coverage or scores consulted.')
if __name__=='__main__':main()
