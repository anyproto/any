"""Phase B prep: public and cleared sets -> TestCorpusBench inputs, with vectors from the eg2-patch banks.

Per set: corpus.jsonl {o,d,r,c,text,name}, {eg2,qwen}.docs.f32, queries.jsonl {Qid,Query,set,lang,kind,shape},
{eg2,qwen}.q.f32, qrels.json {qid: {docid: grade}}.
EG2 = stock EG2 bank ('original') with the head projection; Qwen3 = the 'qwen' bank (1024 dims).
"""
import json, sys, re, os
import numpy as np

B = '/Users/roman/anytype/any-wt-syn391/bench'
OUT = sys.argv[1]
SETS = sys.argv[2].split(',') if len(sys.argv) > 2 else ['scifact', 'nfcorpus', 'miracl-ru-pool', 'miracl-de-pool', 'ru', 'wiki-mix', 'fiqa10k']
u = np.load('/Users/roman/anytype/any-wt-syn391/scripts/embedbench/research/eg2_head_patch/head_mean.npy').astype(np.float32)


def norm(v): return v / np.linalg.norm(v, axis=-1, keepdims=True)


def load_bank(name):
    raw = np.load(f'{B}/eg2-patch/vectors/{name}/raw.npy', mmap_mode='r')
    idx = {}
    for i, l in enumerate(open(f'{B}/eg2-patch/vectors/{name}/records.jsonl')):
        r = json.loads(l)
        idx[(r['role'], r['text'])] = i
    return raw, idx


banks = {'eg2': load_bank('original'), 'qwen': load_bank('qwen')}
IDLIKE = re.compile(r'^(?:[A-Z]{2,6}-\d{1,5}|[0-9a-f]{7,12}|\S*\d\S*|[a-z]+_[a-z0-9_]+)$')


def shape(q):
    t = q.split()
    if len(t) == 1: return 'id-like' if IDLIKE.match(q) else '1 word'
    if len(t) <= 3: return '2-3 words'
    if len(t) <= 5: return '4-5 words'
    return 'question'


def wf32(path, M):
    M = np.ascontiguousarray(M, dtype='<f4')
    with open(path, 'wb') as f:
        f.write(np.array(M.shape, dtype='<u4').tobytes()); f.write(M.tobytes())


def read(p): return [json.loads(l) for l in open(p) if l.strip()]


for s in SETS:
    d = f'{OUT}/{s}'; os.makedirs(d, exist_ok=True)
    docs = read(f'{B}/eg2-patch/sets/{s}/docs.jsonl')
    qs = read(f'{B}/eg2-patch/sets/{s}/queries.jsonl')
    rels = json.load(open(f'{B}/eg2-patch/sets/{s}/qrels.json'))
    rels = {k: (dict.fromkeys(v, 1) if isinstance(v, list) else v) for k, v in rels.items()}
    qs = [q for q in qs if q['id'] in rels]
    corpus, miss = [], {'eg2': 0, 'qwen': 0}
    vec = {'eg2': [], 'qwen': []}
    keep = []
    for x in docs:
        ok = all(('doc', x['text']) in banks[e][1] for e in banks)
        if not ok:
            for e in banks: miss[e] += ('doc', x['text']) not in banks[e][1]
            continue
        keep.append(x)
        for e in banks:
            raw, idx = banks[e]; vec[e].append(np.asarray(raw[idx[('doc', x['text'])]], dtype=np.float32))
        art = x.get('article')
        corpus.append(dict(o=('art:' + art) if art else ('doc:' + x['id']), d='editor_blocks', r=x['id'], c=0,
                           text=x['text'], name=art or ''))
    qkeep, qvec = [], {'eg2': [], 'qwen': []}
    for q in qs:
        if not all(('query', q['text']) in banks[e][1] for e in banks):
            continue
        qkeep.append(q)
        for e in banks:
            raw, idx = banks[e]; qvec[e].append(np.asarray(raw[idx[('query', q['text'])]], dtype=np.float32))
    for e in banks:
        Dv, Qv = norm(np.stack(vec[e])), norm(np.stack(qvec[e]))
        if e == 'eg2':
            Dv, Qv = norm(Dv - np.outer(Dv @ u, u)), norm(Qv - np.outer(Qv @ u, u))
        wf32(f'{d}/{e}.docs.f32', Dv); wf32(f'{d}/{e}.q.f32', Qv)
    with open(f'{d}/corpus.jsonl', 'w') as o:
        for c in corpus: o.write(json.dumps(c, ensure_ascii=False) + '\n')
    lang = {'miracl-ru-pool': 'ru', 'miracl-de-pool': 'de', 'ru': 'ru'}.get(s, 'en')
    with open(f'{d}/queries.jsonl', 'w') as o:
        for q in qkeep:
            o.write(json.dumps(dict(Qid=q['id'], Query=q['text'], set=s, lang=q.get('lang') or lang, kind=q.get('kind') or q.get('section') and 'section' or '',
                                    shape=shape(q['text'])), ensure_ascii=False) + '\n')
    json.dump({q['id']: rels[q['id']] for q in qkeep}, open(f'{d}/qrels.json', 'w'))
    print(s, 'docs', len(corpus), 'of', len(docs), 'missing', miss, 'queries', len(qkeep), 'of', len(qs), 'named objects', len({c['o'] for c in corpus if c['name']}))
