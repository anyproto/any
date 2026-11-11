"""Can a recall gate threshold a min-max blend score? Roma's M1 judged recall candidates (w1 content hits, Qwen3),
scored under each fusion's own score: normalized RRF (fused / max for that query's weights and k) or the min-max blend,
and under the two learned trees (each query scored by its own branch). AUC + precision at matched injection rates.
python gate_blend.py <searchbench dir> -> GATE_BLEND.md"""
import sys, json, hashlib, re, collections
import numpy as np
W = sys.argv[1]
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
G = json.load(open(f'{W}/m1_grades.json')); pool = json.load(open(f'{W}/m1_pool.json'))
mods = {}
for sub in ['', '/m0b']:
    g = {}; sys.argv = ['m0.py', W + sub]; exec(open(f'{W}/m0.py').read().split("SHAPES = [")[0], g); mods[sub] = g
def grade(qid, k, g, t):
    if t['kind'] != 'editor_blocks' and k == (t['objectId'], t['dataset'], t['recordId']): return 3
    return G.get(hashlib.sha1(f"{qid}|{k}".encode()).hexdigest()[:12])
def rrf_scores(fts, vec, wf, wv, k):  # per record: max chunk score, normalized by the best possible sum
    acc = {}
    for lst, w in ((fts, wf), (vec, wv)):
        if w <= 0: continue
        for rank, h in enumerate(lst):
            kk = (h['o'], h['d'], h['r'], h['c']); acc[kk] = acc.get(kk, 0.) + w / (k + rank + 1)
    mx = (max(wf, 0) + max(wv, 0)) / (k + 1); best = {}
    for kk, s in acc.items(): best[kk[:3]] = max(best.get(kk[:3], 0.), s / mx)
    return best
def blend_scores(fts, vec, a):
    def mm(lst):
        if not lst: return {}
        s = [h['s'] for h in lst]; lo, hi = min(s), max(s)
        return {(h['o'], h['d'], h['r'], h['c']): ((h['s'] - lo) / (hi - lo) if hi > lo else 1.) for h in lst}
    F, V = mm(fts), mm(vec); best = {}
    for kk in set(F) | set(V):
        s = (1 - a) * F.get(kk, 0.) + a * V.get(kk, 0.); best[kk[:3]] = max(best.get(kk[:3], 0.), s)
    return best
RRF = lambda wf, wv, k=60: ('rrf', wf, wv, k)
BL = lambda a: ('blend', a)
def score(spec, fts, vec):
    return rrf_scores(fts, vec, *spec[1:]) if spec[0] == 'rrf' else blend_scores(fts, vec, spec[1])
def tree_blend(f):
    if f['cov'] < 1: return RRF(1, 2) if f['vgap'] < .0714 else BL(.7)
    return RRF(1, .5) if f['qlen'] < 2 else BL(.3)
def tree_rrf(f):
    if f['cov'] < .8: return RRF(1, 2, 20) if f['vgap'] < .1031 else RRF(0, 1)
    return RRF(1, .5, 20) if f['fgap'] < .1838 else RRF(1, 0)
SYS = {'RRF w1 (today)': lambda f: RRF(1, 1), 'RRF w2': lambda f: RRF(1, 2), 'blend α0.3': lambda f: BL(.3), 'blend α0.7': lambda f: BL(.7),
       'F2 two-branch': lambda f: BL(.3) if f['cov'] >= 1 else RRF(1, 2),
       'learned depth-2, blends': tree_blend, 'learned depth-2, RRF only': tree_rrf}
C = []  # candidates: qid, rel, scores{sys: s}, branch kind per sys
for qid, info in pool.items():
    g = mods[info['sub']]; q = next(x for x in g['Q'] if x['Qid'] == qid); r = g['D'][qid]; t = g['T'][q['tid']]
    vl = g['vec_leg']('qwen', r); fs = [h['s'] for h in r['fts']]; vs = [h['s'] for h in vl]
    f = dict(cov=info['cov'], qlen=len(q['Query'].split()), vgap=(vs[0] - vs[min(4, len(vs) - 1)]) if vs else 0.,
             fgap=(fs[0] - fs[1]) / fs[0] if len(fs) > 1 and fs[0] > 0 else (1. if fs else 0.))
    w1 = rrf_scores(r['fts'], vl, 1, 1, 60); tk = {(h['o'], h['d'], h['r']) for h in r['titles']}
    cands = [k for k, _ in sorted(w1.items(), key=lambda a: -a[1]) if k not in tk][:10]  # same candidate set as M1's gate
    sc = {n: (fn(f), score(fn(f), r['fts'], vl)) for n, fn in SYS.items()}
    for k in cands:
        x = grade(qid, k, g, t)
        if x is None: continue
        C.append(dict(qid=qid, rel=x >= 2, s={n: v[1].get(k, 0.) for n, v in sc.items()}, kind={n: v[0][0] for n, v in sc.items()}))
def auc(sc, y):
    p = np.array([s for s, l in zip(sc, y) if l]); n = np.array([s for s, l in zip(sc, y) if not l])
    return float(((p[:, None] > n[None, :]).mean() + .5 * (p[:, None] == n[None, :]).mean()))
y = [c['rel'] for c in C]; nq = len({c['qid'] for c in C})
def at_rate(name, rate, cap=3):  # global threshold giving `rate` injected per query (cap per query); precision
    byq = collections.defaultdict(list)
    for c in C: byq[c['qid']].append(c)
    ths = sorted({c['s'][name] for c in C}, reverse=True); best = None
    for th in ths:
        inj = [x for v in byq.values() for x in sorted(v, key=lambda a: -a['s'][name])[:cap] if x['s'][name] >= th]
        if len(inj) / nq >= rate: best = (th, np.mean([x['rel'] for x in inj]), len(inj) / nq); break
    return best
out = ['# Recall gate on blend scores (Roma\'s M1 judged candidates)', '',
       f'{len(C)} judged content candidates from {nq} queries (the w1 page\'s top 10 content records, as in M1\'s gate); base rate relevant {np.mean(y):.2f}. '
       'Each candidate is scored by the fusion\'s own score: RRF normalized to fused / max for that query\'s weights and k, or the min-max blend. '
       'One global threshold across queries, cap 3 per query.', '',
       '| score | AUC | precision at 0.7 injected / query | at 1.6 / query | top hit ≥ 0.9 (share of queries) |', '|---|---|---|---|---|']
for n in SYS:
    a = at_rate(n, .7); b = at_rate(n, 1.6)
    top = collections.defaultdict(float)
    for c in C: top[c['qid']] = max(top[c['qid']], c['s'][n])
    out.append(f"| {n} | {auc([c['s'][n] for c in C], y):.3f} | {a[1]:.2f} (th {a[0]:.3f}) | {b[1]:.2f} (th {b[0]:.3f}) | {np.mean([v >= .9 for v in top.values()]):.2f} |")
for n in ['learned depth-2, blends', 'F2 two-branch']:
    kinds = collections.Counter(c['kind'][n] for c in C)
    for kd in kinds:
        sel = [c for c in C if c['kind'][n] == kd]
        out.append(f"\n{n}: {kd} branch, {len(sel)} candidates, AUC within branch {auc([c['s'][n] for c in sel], [c['rel'] for c in sel]):.3f}, mean score relevant {np.mean([c['s'][n] for c in sel if c['rel']]):.3f} / not {np.mean([c['s'][n] for c in sel if not c['rel']]):.3f}")
open(f'{W}/GATE_BLEND.md', 'w').write('\n'.join(out) + '\n'); print('\n'.join(out))
