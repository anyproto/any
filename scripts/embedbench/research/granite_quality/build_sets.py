"""Rebuild public sets, downloading only missing source files (use --refresh to update)."""
from common import *
import argparse,subprocess,sys,time,urllib.request,urllib.parse
ap=argparse.ArgumentParser();ap.add_argument('--refresh',action='store_true');args=ap.parse_args()
def fetch(url,path):
    if path.exists() and not args.refresh:return
    path.parent.mkdir(parents=True,exist_ok=True)
    for attempt in range(4):
        try:
            req=urllib.request.Request(url,headers={'User-Agent':'embedbench-public-research/1.0'})
            with urllib.request.urlopen(req,timeout=45) as response:path.write_bytes(response.read())
            return
        except Exception:
            if attempt==3:raise
            time.sleep(1)
articles={'en':[('London','london'),('Photosynthesis','photo'),('Python_(programming_language)','python')],
 'de':[('Berlin','berlin'),('Photosynthese','photo'),('Python_(Programmiersprache)','python')],
 'ru':[('Москва','moscow'),('Фотосинтез','photo'),('Python','python')],
 'ja':[('東京都','tokyo'),('光合成','photo'),('Python','python')],
 'zh':[('北京市','beijing'),('光合作用','photo'),('Python','python')]}
for lang,items in articles.items():
    for title,name in items:
        query=urllib.parse.urlencode(dict(action='query',format='json',prop='extracts',explaintext=1,redirects=1,titles=title))
        fetch(f'https://{lang}.wikipedia.org/w/api.php?{query}',BASE/'sets/wiki-source'/f'{lang}-{name}.json')
fetch('https://tinyurl.com/flores200dataset',BASE/'tmp/flores200.tar.gz')
research=ROOT/'scripts/embedbench/research'
def run(script,*args):subprocess.run([sys.executable,str(research/script),*map(str,args)],check=True)
run('subset.py',ROOT/'bench/fiqa',BASE/'sets/fiqa10k',10000)
run('wikiwin.py',BASE/'sets/wiki-source',BASE/'sets/wiki')
run('wikiq.py',BASE/'sets/wiki')
run('wikifold.py',BASE/'sets/wiki',BASE/'sets/wiki-fold')
run('ruq.py',BASE/'sets/wiki',BASE/'sets/ru')
run('granite_quality/prepare.py','--flores')
files=sorted((BASE/'sets').rglob('*.json*'))
manifest={str(p.relative_to(BASE)):hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
(BASE/'debug/data_hashes.json').write_text(json.dumps(manifest,indent=2))
log('Public source and generated-set SHA256 manifest recorded; rebuild uses frozen source downloads unless --refresh is requested.')
