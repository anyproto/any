"""Gate threshold sweep under the RRF-only tree (item 4) on Roma's M1 judged queries: each query's own tree page,
content hits (titles excluded), score = fused / (sum weight / (k + 1)) for the query's branch. Caps 3 and 5.
python gate_tree.py <searchbench dir> -> GATE_TREE.md"""
import sys, json, hashlib, re, collections
import numpy as np
W = sys.argv[1]
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
G = json.load(open(f'{W}/m1_grades.json')); pool = json.load(open(f'{W}/m1_pool.json'))
mods = {}
for sub in ['', '/m0b']:
    g = {}; sys.argv = ['m0.py', W + sub]; exec(open(f'{W}/m0.py').read().split("SHAPES = [")[0], g); mods[sub] = g
def grade(qid, k, t):
    if t['kind'] != 'editor_blocks' and k == (t['objectId'], t['dataset'], t['recordId']): return 3
    return G.get(hashlib.sha1(f"{qid}|{k}".encode()).hexdigest()[:12])
def rrf(fts, vec, wf, wv, k):
    acc = {}
    for lst, w in ((fts, wf), (vec, wv)):
        if w <= 0: continue
        for rank, h in enumerate(lst):
            kk = (h['o'], h['d'], h['r'], h['c']); acc[kk] = acc.get(kk, 0.) + w / (k + rank + 1)
    mx = (wf + wv) / (k + 1); best = {}
    for kk, s in acc.items(): best[kk[:3]] = max(best.get(kk[:3], 0.), s / mx)
    return best
def branch(f):
    if f['cov'] < .8: return ('vector-heavy', 1, 2, 20) if f['vgap'] < .1031 else ('vector only', 0, 1, 60)
    return ('lexical-heavy', 1, .5, 20) if f['fgap'] < .1838 else ('FTS only', 1, 0, 60)
Q = []  # per query: branch, [(score, grade or None)] in page order
for qid, info in pool.items():
    g = mods[info['sub']]; q = next(x for x in g['Q'] if x['Qid'] == qid); r = g['D'][qid]; t = g['T'][q['tid']]
    vl = g['vec_leg']('qwen', r); fs = [h['s'] for h in r['fts']]; vs = [h['s'] for h in vl]
    f = dict(cov=info['cov'], vgap=(vs[0] - vs[min(4, len(vs) - 1)]) if vs else 0., fgap=(fs[0] - fs[1]) / fs[0] if len(fs) > 1 and fs[0] > 0 else (1. if fs else 0.))
    b = branch(f); sc = rrf(r['fts'], vl, *b[1:]); tk = {(h['o'], h['d'], h['r']) for h in r['titles']}
    hits = [(s, grade(qid, k, t)) for k, s in sorted(sc.items(), key=lambda a: -a[1]) if k not in tk][:10]
    Q.append(dict(b=b[0], hits=hits))
n = len(Q); judged = sum(x[1] is not None for q in Q for x in q['hits'][:5]); allh = sum(len(q['hits'][:5]) for q in Q)
out = ['# Gate threshold sweep under the RRF-only tree (Roma\'s M1 judged queries, Qwen3)', '',
       f'{n} queries (benchmark lookups in 9 shapes, not real recall prompts). Each query\'s own tree page, content hits only. '
       f'{judged}/{allh} of the top-5 content hits are judged; unjudged hits count as injected but are left out of the relevant share. Relevant = grade ≥ 2 of 3.', '',
       'Branch mix: ' + ', '.join(f'{k} {v} ({v / n:.0%})' for k, v in collections.Counter(q['b'] for q in Q).most_common()), '',
       '| floor | cap 3: injected / query | relevant | queries injecting | cap 5: injected / query | relevant | queries injecting |', '|---|---|---|---|---|---|---|']
def at(th, cap, sel=None):
    sel = sel or Q; inj = [[x for x in q['hits'][:cap] if x[0] >= th] for q in sel]
    flat = [x for v in inj for x in v]; jd = [x for x in flat if x[1] is not None]
    return len(flat) / len(sel), (np.mean([x[1] >= 2 for x in jd]) if jd else float('nan')), np.mean([bool(v) for v in inj])
for th in [round(.60 + .05 * i, 2) for i in range(8)] + [.915]:
    a = at(th, 3); b = at(th, 5)
    out.append(f'| {th} | {a[0]:.2f} | {a[1]:.0%} | {a[2]:.0%} | {b[0]:.2f} | {b[1]:.0%} | {b[2]:.0%} |')
out += ['', '## By branch (cap 3)', '', '| branch (queries) | floor 0.80: injected / relevant | 0.90 | 0.95 | top content score, median |', '|---|---|---|---|---|']
for bn in ['vector-heavy', 'lexical-heavy', 'FTS only', 'vector only']:
    sel = [q for q in Q if q['b'] == bn]
    if not sel: continue
    cells = [f'{at(th, 3, sel)[0]:.2f} / {at(th, 3, sel)[1]:.0%}' for th in (.8, .9, .95)]
    out.append(f'| {bn} ({len(sel)}) | ' + ' | '.join(cells) + f" | {np.median([q['hits'][0][0] for q in sel if q['hits']]):.3f} |")
open(f'{W}/GATE_TREE.md', 'w').write('\n'.join(out) + '\n'); print('\n'.join(out))
# single-leg branches: score is 61/(61+rank), so the floor can't separate; precision by rank, and two alternatives
out += ['', '## Single-leg branches (FTS only, vector only): the score is 61 / (61 + rank)', '',
        '| branch | rank 1 relevant | rank 2 | rank 3 | rank 4 | rank 5 |', '|---|---|---|---|---|---|']
for bn in ['FTS only', 'vector only']:
    sel = [q for q in Q if q['b'] == bn]
    cells = []
    for i in range(5):
        v = [q['hits'][i][1] >= 2 for q in sel if len(q['hits']) > i and q['hits'][i][1] is not None]
        cells.append(f'{np.mean(v):.0%} ({len(v)})' if v else '–')
    out.append(f'| {bn} | ' + ' | '.join(cells) + ' |')
def alt(th, cap, mode):
    inj = []
    for q in Q:
        hs = q['hits'][:cap]
        if q['b'] in ('FTS only', 'vector only'):
            if mode == 'two-leg scale': hs = [(s / 2, gr) for s, gr in hs]   # divide by the (1,1) max: rank 1 = 0.5
            elif mode == 'rank 1 only': hs = hs[:1]
        inj.append([x for x in hs if x[0] >= th])
    flat = [x for v in inj for x in v]; jd = [x for x in flat if x[1] is not None]
    rel = sum(x[1] >= 2 for x in jd)
    return len(flat) / len(Q), np.mean([x[1] >= 2 for x in jd]) if jd else float('nan'), rel / len(Q)
out += ['', '## Alternatives for single-leg hits (cap 3)', '', '| floor | as decided: injected / relevant / relevant per query | single-leg on the two-leg scale (rank 1 = 0.5) | single-leg: rank 1 only |', '|---|---|---|---|']
for th in [.8, .85, .9, .915, .95]:
    out.append(f'| {th} | ' + ' | '.join('{:.2f} / {:.0%} / {:.2f}'.format(*alt(th, 3, m)) for m in ['as decided', 'two-leg scale', 'rank 1 only']) + ' |')
open(f'{W}/GATE_TREE.md', 'w').write('\n'.join(out) + '\n'); print('\n'.join(out[-16:]))
