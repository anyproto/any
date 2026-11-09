"""Acquire entire published category, not a test-derived term list."""
from util2 import *
import urllib.request,urllib.parse,urllib.error
API='https://ru.wiktionary.org/w/api.php'
def fetch(params):
 u=API+'?'+urllib.parse.urlencode(dict(params,format='json',formatversion=2))
 req=urllib.request.Request(u,headers={'User-Agent':'EG2Ru2Research/1.0 (offline public dictionary research)'})
 cache=BASE/'sources'/('http-'+hashlib.sha256(u.encode()).hexdigest()+'.json')
 if cache.exists():return json.loads(cache.read_text())
 for attempt in range(6):
  try:
   time.sleep(2)
   with urllib.request.urlopen(req,timeout=45) as r:x=json.load(r)
   save(cache,x);return x
  except urllib.error.HTTPError as e:
   if e.code not in [429,502,503,504]:raise
   print('public API backoff',e.code,attempt,flush=True);time.sleep(min(10*(attempt+1),40))
 raise RuntimeError('Public API unavailable after bounded retries')
def main():
 verify_seal();assert not (BASE/'sources/pages.jsonl').exists()
 categories=['Категория:Компьютерный жаргон/ru'];members={};seen=[]
 while categories:
  cat=categories.pop(0)
  if cat in seen:continue
  seen.append(cat);params=dict(action='query',list='categorymembers',cmtitle=cat,cmlimit=500)
  while True:
   x=fetch(params);save(BASE/'sources'/f'category-{len(seen)}-{len(members)}.json',x)
   for p in x['query']['categorymembers']:
    if p['ns']==0:members[p['title']]=p
    elif p['ns']==14 and p['title'].endswith('/ru'):categories.append(p['title'])
   if 'continue' not in x:break
   params.update(x['continue'])
 titles=sorted(members)+['Приложение:Компьютерный сленг'];pages=[]
 for start in range(0,len(titles),40):
  existing=BASE/'sources'/f'api-pages-{start:04}.json'
  x=json.loads(existing.read_text()) if existing.exists() else fetch(dict(action='query',prop='revisions',rvprop='ids|timestamp|content',rvslots='main',titles='|'.join(titles[start:start+40])))
  save(BASE/'sources'/f'api-pages-{start:04}.json',x)
  for p in x['query']['pages']:
   if 'revisions' not in p:continue
   r=p['revisions'][0];pages.append(dict(title=p['title'],pageid=p['pageid'],revid=r['revid'],timestamp=r['timestamp'],wikitext=r['slots']['main']['content'],url='https://ru.wiktionary.org/w/index.php?oldid='+str(r['revid']),history='https://ru.wiktionary.org/w/index.php?title='+urllib.parse.quote(p['title'])+'&action=history',attribution='Wiktionary contributors',license='CC-BY-SA-4.0'))
  print('acquired',len(pages),'/',len(titles),flush=True)
 write(BASE/'sources/pages.jsonl',pages)
 save(BASE/'sources/provenance.json',dict(acquired=datetime.datetime.now(datetime.timezone.utc).isoformat(),categories=seen,pages=len(pages),selection='All mainspace entries in Russian computer slang category and its Russian subcategories, plus whole computer-slang appendix; no test term lookup',api=API,license='CC BY-SA 4.0',license_url='https://creativecommons.org/licenses/by-sa/4.0/',terms='https://foundation.wikimedia.org/wiki/Policy:Terms_of_Use',source_sha256=sha(BASE/'sources/pages.jsonl')))
 log(f'Acquired {len(pages)} public dictionary pages from whole categories, independently of sealed vocabulary; retained raw revision snapshots and attribution.')
if __name__=='__main__':main()
