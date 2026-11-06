"""Fetch public metadata/tokenizers and small MIRACL text repacks, no credentials."""
from common import *
import urllib.request,re

def fetch(url,p):
    if p.exists():return
    p.parent.mkdir(parents=True,exist_ok=True)
    with urllib.request.urlopen(url,timeout=90) as f,p.open('wb') as out:
        while chunk:=f.read(1<<20):out.write(chunk)

for name in ['config.json','tokenizer.json','tokenizer_config.json']:
    fetch('https://huggingface.co/google/embeddinggemma-2/resolve/f9972ac22e696a105c482202646979e33e4b5a1e/'+name,BASE/'hf'/name)
for lang in ['ru','de']:
    for kind in ['topics','qrels']:
        fetch(f'https://huggingface.co/datasets/miracl/miracl/resolve/main/miracl-v1.0-{lang}/{kind}/{kind}.miracl-v1.0-{lang}-dev.tsv',BASE/'sources'/f'miracl-{lang}-{kind}.tsv')
    fetch(f'https://huggingface.co/datasets/Hieuman/{lang}_MIRACL/resolve/main/data/train-00000-of-00001.parquet',BASE/'sources'/f'miracl-{lang}.parquet')
# Full corpus uses precisely the same test queries and qrels as the 10k subset.
p=BASE/'sets/fiqa-full';p.mkdir(parents=True,exist_ok=True)
if not (p/'docs.jsonl').exists():
    docs=[dict(id=d['_id'],text=(d.get('title','')+'\n'+d['text']).strip()) for d in read(ROOT/'bench/fiqa/corpus.jsonl')]
    write(p/'docs.jsonl',docs)
for name in ['queries.jsonl','qrels.json']:
    if not (p/name).exists():(p/name).write_bytes((BASE/'sets/fiqa10k'/name).read_bytes())
texts=['Photosynthesis','Discussion: Photosynthesis','# Chloroplasts','Фотосинтез превращает энергию света в химическую энергию.','Die Photosynthese findet in den Chloroplasten statt.','光合成は光のエネルギーを化学エネルギーに変換する。','光合作用将光能转化为化学能。','',('Photosynthesis converts light energy into chemical energy. '*300)]
write(BASE/'validation/corpus.jsonl',[dict(text=t) for t in texts])
source=(ROOT/'internal/indexer/embed_bench_test.go').read_text().split('queries := []string{')[1].split('}')[0]
queries=re.findall('"(.*)"',source)
write(BASE/'validation/records.jsonl',[dict(text=t,role=r) for r,ts in [('doc',texts),('query',queries)] for t in ts])
log('Public source files, full FiQA and native parity fixtures prepared; existing downloaded snapshots reused.')
