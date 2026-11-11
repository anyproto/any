"""Constant maps for fusion: RRF k x vector weight, blend alpha, router F2 cut points. Qwen3 legs.
M0 (875 known-item, MRR) and 9 public sets (2,064, nDCG@10). -> CONSTS.md
"""
import json, sys, re, collections
import numpy as np

W = sys.argv[1]; LIMIT = 10
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
pb = open(f'{W}/pb_analyze.py').read(); exec(pb[pb.index('def rf32'):pb.index('rows_all = []')])

cases = []  # (dataset, titles, fts, vec, cov, scorer)
for sub in ['', '/m0b']:
    g = {}; sys.argv = ['m0.py', W + sub]; exec(open(f'{W}/m0.py').read().split("SHAPES = [")[0], g)
    TXT = {}
    for sp, ds in g['docs'].items():
        for d_ in ds: TXT[(d_['o'], d_['d'], d_['r'], d_['c'])] = d_['text']
    for q in [q for q in g['Q'] if q['Qid'] in g['D']]:
        r = g['D'][q['Qid']]; vl = g['vec_leg']('qwen', r); qt = set(tok(q['Query']))
        c = max((len(qt & set(tok(TXT.get((h['o'], h['d'], h['r'], h['c'])) or h.get('t', '')))) / len(qt) for h in r['fts'][:3]), default=0.) if qt else 0.
        tid = q['tid']; rank_of, rr = g['rank_of'], g['rr']
        cases.append(('M0', r['titles'], r['fts'], vl, c, (lambda p, tid=tid, rank_of=rank_of, rr=rr: rr(rank_of(p, tid)))))
SETS = ['ru', 'wiki-mix', 'miracl-ru-pool', 'miracl-de-pool', 'scifact', 'nfcorpus', 'fiqa10k', 'ru-sealed', 'ru-new-rig']
for s in SETS:
    d = f'{W}/pb/{s}'; corpus = [json.loads(l) for l in open(f'{d}/corpus.jsonl')]
    T2 = {(c['o'], c['d'], c['r'], c['c']): c['text'] for c in corpus}; rels = json.load(open(f'{d}/qrels.json'))
    for l in open(f'{d}/qwen.sweep.jsonl'):
        r = json.loads(l); qt = set(tok(r['query']))
        c = max((len(qt & set(tok(T2.get(ckey(h), '')))) / len(qt) for h in r['fts'][:3]), default=0.) if qt else 0.
        rel = rels[r['qid']]
        cases.append(('public', r['titles'], r['fts'], r['vec'], c, (lambda p, rel=rel: metrics(p, rel)[0])))


def mean(ds, fn):
    v = [sc(fn(t, f, vv, c)) for (dd, t, f, vv, c, sc) in cases if dd == ds]
    return float(np.mean(v))


out = ['# Fusion constant maps (Qwen3 legs)', '', 'M0 = Roma\'s snapshot, 875 known-item queries, MRR. public = 9 sets, 2,064 queries, nDCG@10.', '']
out += ['## RRF: k × vector weight (FTS weight 1)', '', '| k | ' + ' | '.join(f'w{w} M0 / public' for w in [0.5, 1, 1.5, 2, 3]) + ' |', '|' + '---|' * 6]
for k in [5, 10, 20, 60, 120]:
    cells = []
    for w in [0.5, 1, 1.5, 2, 3]:
        fn = lambda t, f, v, c, k=k, w=w: fuse(t, f, v, 1, w, k)
        cells.append(f'{mean("M0", fn):.3f} / {mean("public", fn):.3f}')
    out.append(f'| {k} | ' + ' | '.join(cells) + ' |')
out += ['', '## Min-max score blend: α (vector share)', '', '| α | M0 | public |', '|---|---|---|']
for a in [0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8]:
    fn = lambda t, f, v, c, a=a: blend(t, f, v, a)
    out.append(f'| {a} | {mean("M0", fn):.3f} | {mean("public", fn):.3f} |')
out += ['', '## Router F2 cut points: low (vector only below), α at full coverage, middle weight', '',
        '| low cut | α at full | middle w | M0 | public |', '|---|---|---|---|---|']
for lo in [0.25, 0.5, 0.75]:
    for a in [0.2, 0.3, 0.5]:
        for mw in [1, 2]:
            fn = lambda t, f, v, c, lo=lo, a=a, mw=mw: blend(t, f, v, a) if c >= 1 else (fuse(t, [], v, 0, 1) if c < lo else fuse(t, f, v, 1, mw))
            out.append(f'| {lo} | {a} | w{mw} | {mean("M0", fn):.3f} | {mean("public", fn):.3f} |')
fn1 = lambda t, f, v, c: fuse(t, f, v, 1, 1)
out += ['', f'Reference: RRF w1 k60 = {mean("M0", fn1):.3f} / {mean("public", fn1):.3f}; vector leg only = {mean("M0", lambda t, f, v, c: fuse(t, [], v, 0, 1)):.3f} / {mean("public", lambda t, f, v, c: fuse(t, [], v, 0, 1)):.3f}; FTS only = {mean("M0", lambda t, f, v, c: fuse(t, f, [], 1, 0)):.3f} / {mean("public", lambda t, f, v, c: fuse(t, f, [], 1, 0)):.3f}.', '']
open(f'{W}/CONSTS.md', 'w').write('\n'.join(out) + '\n'); print('\n'.join(out))
