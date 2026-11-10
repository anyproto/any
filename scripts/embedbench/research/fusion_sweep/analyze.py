"""Offline analysis of TestFusionSweep output (SYN-391 scratch).

1. Reproduction: re-fuse the dumped legs exactly like Search (weighted RRF per chunk, groupHits by max,
   titles first) and compare with the production page at every swept weight.
2. Sensitivity: how often a weight change moves the page at all.
3. Known-item quality per query shape (q1 one word, q2 2-3 words, q3 question): MRR@10 / hit@1 / recall@10 of the
   target object, production weights and offline variants, paired bootstrap over targets.
"""
import json, sys, random, collections, math

D = sys.argv[1]
T = {t['tid']: t for t in json.load(open(f'{D}/targets.json'))}
weak = set(sys.argv[2].split(',')) if len(sys.argv) > 2 else set()
rows = [json.loads(l) for l in open(f'{D}/sweep.jsonl')]
LIMIT = 10


def gkey(h):
    return (h['o'], h['d'], h['r'])


def ckey(h):
    return (h['o'], h['d'], h['r'], h['c'])


def fuse_page(r, wf, wv, k=60):
    titles = r['titles']
    rest = LIMIT - len(titles)
    if rest <= 0:
        return titles[:LIMIT]
    acc = {}
    for lst, w in ((r['fts'], wf), (r['vec'], wv)):
        if w <= 0:
            continue
        for rank, h in enumerate(lst):
            kk = ckey(h)
            if kk not in acc:
                acc[kk] = [h, 0.0]
            acc[kk][1] += w / (k + rank + 1)
    fused = sorted(acc.values(), key=lambda a: (-a[1], a[0]['o'], a[0]['d'], a[0]['r'], a[0]['c']))
    groups = {}
    for h, s in fused:
        g = gkey(h)
        if g not in groups or s > groups[g][1]:
            groups[g] = (h, s)
    gs = sorted(groups.values(), key=lambda a: (-a[1], a[0]['o'], a[0]['d'], a[0]['r']))
    return titles + [dict(h, s=s) for h, s in gs[:rest]]


def bucket(qid):
    return {'q1': '1 word', 'q2': '2-3 words', 'q3': 'question'}[qid.split('-')[1]]


def target_rank(page, tid):
    o = T[tid]['objectId']
    for i, h in enumerate(page):
        if h['o'] == o:
            return i + 1
    return None


def metrics(ranks):
    return dict(mrr=sum(1 / r for r in ranks if r) / len(ranks), hit1=sum(1 for r in ranks if r == 1) / len(ranks),
                rec10=sum(1 for r in ranks if r) / len(ranks))


def boot(a, b, n=4000, seed=391):
    """paired bootstrap of mean(a - b) over items; returns delta, lo, hi"""
    rnd = random.Random(seed)
    d = [x - y for x, y in zip(a, b)]
    m = sum(d) / len(d)
    bs = sorted(sum(d[rnd.randrange(len(d))] for _ in d) / len(d) for _ in range(n))
    return m, bs[int(.025 * n)], bs[int(.975 * n) - 1]


rows = [r for r in rows if r['qid'].split('-')[0] not in weak]

# 1. reproduction
print('## 1. Offline re-fusion against the production page')
mism = collections.Counter()
for r in rows:
    for ws, p in r['pages'].items():
        off = [gkey(h) for h in fuse_page(r, 1.0, float(ws))]
        prod = [gkey(h) for h in p['hits']]
        if off != prod:
            mism[ws] += 1
print('pages compared per weight:', len(rows), '| mismatches:', dict(mism) or 'none')

# 2. sensitivity
print('\n## 2. How often a vector-weight change moves the page (vs w1)')
for ws in ['0.5', '1.5', '2', '3']:
    ch = top1 = 0
    for r in rows:
        a = [gkey(h) for h in r['pages']['1']['hits']]
        b = [gkey(h) for h in r['pages'][ws]['hits']]
        ch += a != b
        top1 += a[:1] != b[:1]
    print(f'w{ws}: page order changed {ch}/{len(rows)}, top-1 changed {top1}/{len(rows)}')
tf = collections.defaultdict(list)
for r in rows:
    tf[bucket(r['qid'])].append(len(r['titles']))
print('title-block slots of 10, mean by shape:', {b: round(sum(v) / len(v), 2) for b, v in tf.items()},
      '| pages fully titled:', sum(1 for r in rows if len(r['titles']) >= LIMIT))

# 3. quality
print('\n## 3. Known-item quality of the target object (MRR@10 / hit@1 / recall@10), Δ vs production w1 with 95% CI')
variants = {f'prod w{ws}': (lambda r, ws=ws: r['pages'][ws]['hits']) for ws in ['0.5', '1', '1.5', '2', '3']}
variants.update({
    'fts leg only': lambda r: fuse_page(r, 1, 0),
    'vector leg only': lambda r: fuse_page(r, 0, 1),
    'w1 k20': lambda r: fuse_page(r, 1, 1, 20),
    'w2 k20': lambda r: fuse_page(r, 1, 2, 20),
    'w5': lambda r: fuse_page(r, 1, 5),
    'shape: w0.5 / w1 / w2': lambda r: fuse_page(r, 1, {'1 word': .5, '2-3 words': 1, 'question': 2}[bucket(r['qid'])]),
})
by = collections.defaultdict(list)
for r in rows:
    by[bucket(r['qid'])].append(r)
by['all'] = rows
for b in ['1 word', '2-3 words', 'question', 'all']:
    rs = by[b]
    base = [target_rank(variants['prod w1'](r), r['qid'].split('-')[0]) for r in rs]
    bm = metrics(base)
    print(f'\n### {b} (n={len(rs)}): prod w1 MRR {bm["mrr"]:.3f}, hit@1 {bm["hit1"]:.3f}, recall@10 {bm["rec10"]:.3f}')
    for name, fn in variants.items():
        if name == 'prod w1':
            continue
        rk = [target_rank(fn(r), r['qid'].split('-')[0]) for r in rs]
        m = metrics(rk)
        d, lo, hi = boot([1 / x if x else 0 for x in rk], [1 / x if x else 0 for x in base])
        star = '*' if lo > 0 or hi < 0 else ''
        print(f'  {name:24s} MRR {m["mrr"]:.3f} ({d:+.3f}{star} [{lo:+.3f}, {hi:+.3f}])  hit@1 {m["hit1"]:.3f}  recall@10 {m["rec10"]:.3f}')

# 4. where the target sits in each leg
print('\n## 4. Where the target object sits in each leg (first chunk rank, within the leg window)')
for b in ['1 word', '2-3 words', 'question']:
    c = collections.Counter()
    for r in by[b]:
        o = T[r['qid'].split('-')[0]]['objectId']
        intitle = any(h['o'] == o for h in r['titles'])
        fr = next((i + 1 for i, h in enumerate(r['fts']) if h['o'] == o), None)
        vr = next((i + 1 for i, h in enumerate(r['vec']) if h['o'] == o), None)
        c['title' if intitle else ('both' if fr and vr else 'fts only' if fr else 'vec only' if vr else 'neither')] += 1
    print(f'{b}: {dict(c)}')
