"""M1: graded relevance on Roma's snapshot (126 stratified queries, pooled top-10, blind judge). Qwen3 legs.
nDCG@10, P@5 (grade >= 2), success@1 (grade >= 2) per system and shape, against known-item MRR; recall gate precision.
python m1.py <searchbench dir> <judge workflow output json> -> M1.md"""
import sys, json, hashlib, re, collections
import numpy as np
W, RES = sys.argv[1], sys.argv[2]
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
G = dict(json.load(open(f'{W}/judge_grades.json')))
res = json.load(open(RES)); res = res.get('result', res) if isinstance(res, dict) else res
nsplit = 0; ab = []
for b in res:
    if not b: continue
    for p in set(b['a']) | set(b['b']):
        a, c = b['a'].get(p), b['b'].get(p)
        if p in b['tb']: G[p] = b['tb'][p]; nsplit += 1
        elif a is not None and c is not None: G[p] = round((a + c) / 2 + 1e-9); ab.append((a, c))
        else: G[p] = a if a is not None else c
json.dump({p: G[p] for p in G}, open(f'{W}/m1_grades.json', 'w'))
pool = json.load(open(f'{W}/m1_pool.json'))
mods = {}
for sub in ['', '/m0b']:
    g = {}; sys.argv = ['m0.py', W + sub]; exec(open(f'{W}/m0.py').read().split("SHAPES = [")[0], g)
    TXT = {}
    for sp, ds in g['docs'].items():
        for d_ in ds: TXT[(d_['o'], d_['d'], d_['r'], d_['c'])] = d_['text']
    g['TXT'] = TXT; mods[sub] = g
LIMIT = 10; unj = collections.Counter(); tot = collections.Counter()
def grade(qid, h, g, t):
    k = g['gkey'](h)
    if t['kind'] != 'editor_blocks' and k == (t['objectId'], t['dataset'], t['recordId']): return 3
    pid = hashlib.sha1(f"{qid}|{k}".encode()).hexdigest()[:12]
    return G.get(pid)
def ndcg(gs, ideal):
    dcg = sum((2 ** x - 1) / np.log2(i + 2) for i, x in enumerate(gs[:LIMIT]))
    idg = sum((2 ** x - 1) / np.log2(i + 2) for i, x in enumerate(sorted(ideal, reverse=True)[:LIMIT]))
    return dcg / idg if idg else 0.
SYS = ['L1 qwen', 'L2 fts', 'w0.5', 'w1', 'w2', 'blend.3', 'router C', 'F2 lo0.5', 'F2 two-branch']
M = {}; GATE = []
for qid, info in pool.items():
    g = mods[info['sub']]; q = next(x for x in g['Q'] if x['Qid'] == qid); r = g['D'][qid]; t = g['T'][q['tid']]
    vl = g['vec_leg']('qwen', r); fuse, blend = g['fuse'], g['blend']; c = info['cov']
    P = {'L1 qwen': g['group'](g['exact_sorted']('qwen', r['space'], qid), 10), 'L2 fts': fuse(r['titles'], r['fts'], [], 1, 0),
         'w0.5': fuse(r['titles'], r['fts'], vl, 1, .5), 'w1': fuse(r['titles'], r['fts'], vl, 1, 1), 'w2': fuse(r['titles'], r['fts'], vl, 1, 2),
         'blend.3': blend(r['titles'], r['fts'], vl, .3),
         'router C': fuse(r['titles'], r['fts'], vl, 1, .5) if c >= 1 else (fuse(r['titles'], [], vl, 0, 1) if c < .5 else fuse(r['titles'], r['fts'], vl, 1, 2)),
         'F2 lo0.5': blend(r['titles'], r['fts'], vl, .3) if c >= 1 else (fuse(r['titles'], [], vl, 0, 1) if c < .5 else fuse(r['titles'], r['fts'], vl, 1, 2)),
         'F2 two-branch': blend(r['titles'], r['fts'], vl, .3) if c >= 1 else fuse(r['titles'], r['fts'], vl, 1, 2)}
    pooled = {tuple(k): grade(qid, dict(o=k[0], d=k[1], r=k[2]), g, t) for k in info['recs']}
    ideal = [x for x in pooled.values() if x]
    m = {'_shape': info['shape'], '_tid': info['tid'], '_split': info['split']}
    for s, p in P.items():
        gs = []
        for h in p[:LIMIT]:
            x = grade(qid, h, g, t); tot[s] += 1
            if x is None: unj[s] += 1; x = 0
            gs.append(x)
        rk = g['rank_of'](p, q['tid'])
        m[s] = (ndcg(gs, ideal), np.mean([x >= 2 for x in gs[:5]]) if gs else 0., float(bool(gs) and gs[0] >= 2), g['rr'](rk))
    M[qid] = m
    # recall gate on the w1 page's content hits (titles excluded), fused score per record = max chunk RRF score
    acc = {}
    for lst, w in ((r['fts'], 1.), (vl, 1.)):
        for rank, h in enumerate(lst):
            acc[g['ckey'](h)] = acc.get(g['ckey'](h), 0.) + w / (60 + rank + 1)
    best = {}
    for k, s in acc.items():
        if k[:3] not in best or s > best[k[:3]]: best[k[:3]] = s
    cos = {}
    for h in vl:
        k = g['gkey'](h); cos[k] = max(cos.get(k, -1), h['s'])
    top1 = max(cos.values()) if cos else 1.
    tk = {g['gkey'](h) for h in r['titles']}
    for k, s in sorted(best.items(), key=lambda a: -a[1])[:10]:
        if k in tk: continue
        x = grade(qid, dict(o=k[0], d=k[1], r=k[2]), g, t)
        if x is None: continue
        GATE.append(dict(qid=qid, s=s, cos=cos.get(k), rc=(cos[k] / top1 if k in cos and top1 > 0 else None), rel=x >= 2))

def bs(a, b, groups, n=2000, seed=391):
    d = np.array(a) - np.array(b); rnd = np.random.default_rng(seed); gr = collections.defaultdict(list)
    for i, tt in enumerate(groups): gr[tt].append(i)
    keys = list(gr); o = []
    for _ in range(n):
        idx = [i for k in rnd.integers(0, len(keys), len(keys)) for i in gr[keys[k]]]; o.append(d[idx].mean())
    o = np.sort(o); return d.mean(), o[int(.025 * n)], o[int(.975 * n) - 1]
fm = lambda t: f"{t[0]:+.3f}{'*' if t[1] > 0 or t[2] < 0 else ''}"
exact = sum(a == c for a, c in ab) / max(1, len(ab)); side = sum((a >= 2) == (c >= 2) for a, c in ab) / max(1, len(ab))
qs = list(M.values())
out = ['# M1: graded relevance on Roma\'s snapshot (Qwen3 legs)', '',
       f'{len(M)} queries, 14 per shape, stratified (seed 391). Pool: top-10 of w1, w2, F2 two-branch, F2 lo0.5, top-5 of vector-only and FTS-only. Exact container targets grade 3; everything else blind-judged (2 Haiku votes, Sonnet on splits; {nsplit} splits; Haiku agreement exact {exact:.2f}, same side of ≥2 {side:.2f}).',
       'Unjudged records in a page count as 0; judged share per system: ' + ', '.join(f'{s} {1 - unj[s] / max(1, tot[s]):.2f}' for s in SYS) + '.', '',
       '| system | nDCG@10 | Δ vs w1 | P@5 (≥2) | success@1 (≥2) | known-item MRR | Δ vs w1 |', '|---|---|---|---|---|---|---|']
grp = [x['_tid'] for x in qs]
for s in SYS:
    nd = [x[s][0] for x in qs]; b = [x['w1'][0] for x in qs]; mr = [x[s][3] for x in qs]; bm = [x['w1'][3] for x in qs]
    out.append(f"| {s} | {np.mean(nd):.3f} | {'' if s == 'w1' else fm(bs(nd, b, grp))} | {np.mean([x[s][1] for x in qs]):.3f} | {np.mean([x[s][2] for x in qs]):.3f} | {np.mean(mr):.3f} | {'' if s == 'w1' else fm(bs(mr, bm, grp))} |")
SH = ['1 word', 'id-like', 'name', 'typo', 'prefix', '2-3 words', '4-5 words', 'question', 'paste']
out += ['', '## nDCG@10 by shape (Δ vs w1)', '', '| system | ' + ' | '.join(SH) + ' |', '|' + '---|' * (len(SH) + 1)]
for s in SYS:
    cells = []
    for sh in SH:
        sel = [x for x in qs if x['_shape'] == sh]
        v = [x[s][0] for x in sel]; b = [x['w1'][0] for x in sel]
        cells.append(f'{np.mean(v):.3f}' if s == 'w1' else f'{np.mean(v):.3f} ({fm(bs(v, b, [x["_tid"] for x in sel]))})')
    out.append(f'| {s} | ' + ' | '.join(cells) + ' |')
# gate
def auc(sc, y):
    p = [s for s, l in zip(sc, y) if l]; n = [s for s, l in zip(sc, y) if not l]
    if not p or not n: return float('nan')
    return float(np.mean([(a > b) + .5 * (a == b) for a in p for b in n]))
out += ['', '## Recall gate on Roma\'s data (w1 content hits, Qwen3)', '', '| gate | precision (≥2) | injected per query | queries with ≥1 relevant injected |', '|---|---|---|---|']
byq = collections.defaultdict(list)
for x in GATE: byq[x['qid']].append(x)
for name, th, cap in [('bao 0.015, cap 5', .015, 5), ('any-agents 0.030, cap 3', .030, 3), ('stricter 0.032, cap 3', .032, 3)]:
    inj = [[x for x in sorted(v, key=lambda a: -a['s']) if x['s'] >= th][:cap] for v in [byq[q] for q in M]]
    n = sum(len(v) for v in inj); pr = sum(x['rel'] for v in inj for x in v) / max(1, n)
    out.append(f"| {name} | {pr:.2f} | {n / len(M):.2f} | {np.mean([any(x['rel'] for x in v) for v in inj]):.2f} |")
y = [x['rel'] for x in GATE]
rc = [(x['rc'] if x['rc'] is not None else 0.) for x in GATE]; cs = [(x['cos'] if x['cos'] is not None else 0.) for x in GATE]
out += ['', f"AUC (relevant vs not, {len(GATE)} judged content candidates): fused RRF score {auc([x['s'] for x in GATE], y):.3f}; raw cosine {auc(cs, y):.3f}; cosine relative to the query's top-1 {auc(rc, y):.3f}. Base rate relevant {np.mean(y):.2f}.", '']
open(f'{W}/M1.md', 'w').write('\n'.join(out) + '\n'); print('\n'.join(out))
