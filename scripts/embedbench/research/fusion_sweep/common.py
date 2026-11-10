"""Offline analysis of TestFusionSweep output (SYN-391 scratch).

1. Reproduction: re-fuse the dumped legs exactly like Search (weighted RRF per chunk, groupHits by max,
   titles first) and compare with the production page at every swept weight.
2. Sensitivity: how often a weight change moves the page at all.
3. Known-item quality per query shape (q1 one word, q2 2-3 words, q3 question): MRR@10 / hit@1 / recall@10 of the
   target object, production weights and offline variants, paired bootstrap over targets.
"""
import json, sys, random, collections, math

D = __import__('os').environ['SWEEP_DIR']
T = {t['tid']: t for t in json.load(open(f'{D}/targets.json'))}
weak = set(filter(None, __import__('os').environ.get('SWEEP_WEAK', '').split(',')))
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



rows=[r for r in rows if r['qid'].split('-')[0] not in weak]
def blend_page(r, alpha):
    titles=r['titles']; rest=LIMIT-len(titles)
    if rest<=0: return titles[:LIMIT]
    def mm(lst):
        if not lst: return {}
        s=[h['s'] for h in lst]; lo,hi=min(s),max(s)
        return {ckey(h):((h['s']-lo)/(hi-lo) if hi>lo else 1.0) for h in lst}
    F,V=mm(r['fts']),mm(r['vec']); H={ckey(h):h for h in r['fts']+r['vec']}
    sc={k:(1-alpha)*F.get(k,0)+alpha*V.get(k,0) for k in H}
    groups={}
    for k,s in sorted(sc.items(), key=lambda a:(-a[1],a[0])):
        g=k[:3]
        if g not in groups: groups[g]=(H[k],s)
    gs=sorted(groups.values(), key=lambda a:(-a[1],a[0]['o'],a[0]['d'],a[0]['r']))
    return titles+[h for h,_ in gs[:rest]]

