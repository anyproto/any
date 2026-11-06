"""Freeze public corpus partitions; this script never opens from-rig."""
from common import *
import argparse,urllib.request,zipfile,csv

def photo(d):return 'photo' in d.get('article','').lower() or d.get('article') in ['Фотосинтез','光合成','光合作用']
def put(name,docs,queries,qr):
    p=BASE/'sets'/name;p.mkdir(exist_ok=True,parents=True)
    write(p/'docs.jsonl',docs);write(p/'queries.jsonl',queries);save(p/'qrels.json',{q['id']:qr[q['id']] for q in queries})
    log(f'Prepared {name}: {len(docs)} docs, {len(queries)} queries.')
def fetch(url,path):
    if path.exists():return
    with urllib.request.urlopen(url,timeout=60) as f,path.open('wb') as out:
        while b:=f.read(1<<20):out.write(b)

def main():
    src=ROOT/'bench/granite-quality/sets'
    docs=read(src/'wiki-mix/docs.jsonl');fit_text={d['text'] for d in docs if photo(d)}
    for name in ['wiki','wiki-mix','wiki-fold','ru','fiqa10k']:
        p=src/name;ds=read(p/'docs.jsonl');qs=read(p/'queries.jsonl');qr=json.loads((p/'qrels.json').read_text())
        dm={d['id']:d for d in ds}
        if name=='fiqa10k':put(name,ds,qs,qr);continue
        isfit=lambda q:all(photo(dm[i]) for i in qr[q['id']])
        if name=='wiki-mix':put('dev',[d for d in ds if photo(d)],[q for q in qs if isfit(q)],qr)
        td=[d for d in ds if not photo(d) and d['text'] not in fit_text];tq=[q for q in qs if not isfit(q)]
        present={d['id'] for d in td};assert all(set(qr[q['id']])<=present for q in tq)
        assert not fit_text.intersection(d['text'] for d in td)
        put(name,td,tq,qr)
    for name in ['scifact','nfcorpus']:
        z=BASE/'tmp'/f'{name}.zip'
        fetch(f'https://public.ukp.informatik.tu-darmstadt.de/thakur/BEIR/datasets/{name}.zip',z)
        with zipfile.ZipFile(z) as f:
            ds=[json.loads(x) for x in f.read(name+'/corpus.jsonl').splitlines()]
            qs=[json.loads(x) for x in f.read(name+'/queries.jsonl').splitlines()]
            rr=csv.DictReader(f.read(name+'/qrels/test.tsv').decode().splitlines(),delimiter='\t');qr={}
            for r in rr:
                if int(r['score'])>0:qr.setdefault(r['query-id'],{})[r['corpus-id']]=int(r['score'])
        ds=[dict(id=r['_id'],text=((r.get('title','')+'\n'+r['text']).strip())) for r in ds]
        qs=[dict(id=r['_id'],text=r['text']) for r in qs if r['_id'] in qr]
        put(name,ds,qs,qr);z.unlink()
    manifest={str(p.relative_to(ROOT)):sha(p) for p in sorted((BASE/'sets').rglob('*')) if p.is_file()}
    save(BASE/'data_hashes.json',manifest)

if __name__=='__main__':main()
