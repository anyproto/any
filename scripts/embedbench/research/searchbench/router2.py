"""Router variants with confidence-aware handling of full-coverage queries. M0 (+ prefix/paste) and 9 public sets.
python router2.py <searchbench dir> -> ROUTER2.md
"""
import json, sys, re, collections, os
import numpy as np

W = sys.argv[1]; LIMIT = 10
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
pb = open(f'{W}/pb_analyze.py').read(); exec(pb[pb.index('def rf32'):pb.index('rows_all = []')])


def lexfirst(titles, fts, vec):
    rest = LIMIT - len(titles); out, seen = [], set()
    for h in fts + vec:
        if gkey(h) in seen: continue
        seen.add(gkey(h)); out.append(h)
        if len(out) == rest: break
    return titles + out


def route(name, titles, fts, vec, c):
    if name == 'w1': return fuse(titles, fts, vec, 1, 1)
    if name == 'w2': return fuse(titles, fts, vec, 1, 2)
    low = c < .5
    if low and name != 'w1': return fuse(titles, [], vec, 0, 1)
    full = c >= 1
    if name == 'C': return fuse(titles, fts, vec, 1, .5) if full else fuse(titles, fts, vec, 1, 2)
    if name == 'F1 lexical-first': return lexfirst(titles, fts, vec) if full else fuse(titles, fts, vec, 1, 2)
    if name == 'F2 blend.3': return blend(titles, fts, vec, .3) if full else fuse(titles, fts, vec, 1, 2)
    if name == 'F3 blend.3 everywhere-not-low': return blend(titles, fts, vec, .3)
    raise KeyError(name)


ROUTES = ['w1', 'w2', 'C', 'F1 lexical-first', 'F2 blend.3', 'F3 blend.3 everywhere-not-low']


def bs(a, b, groups, n=2000, seed=391):
    d = np.array(a) - np.array(b); rnd = np.random.default_rng(seed)
    g = collections.defaultdict(list)
    for i, t in enumerate(groups): g[t].append(i)
    keys = list(g); out = []
    for _ in range(n):
        idx = [i for k in rnd.integers(0, len(keys), len(keys)) for i in g[keys[k]]]; out.append(d[idx].mean())
    out = np.sort(out); return d.mean(), out[int(.025 * n)], out[int(.975 * n) - 1]


fm = lambda t: f"{t[0]:+.3f}{'*' if t[1] > 0 or t[2] < 0 else ''}"
out = ['# Router variants', '', 'Low coverage (<0.5) → vector only in every variant except w1/w2. Δ against w1; * = 95% CI excludes 0 (clustered by target on M0).', '']

# M0 + m0b
for emb in ['qwen', 'eg2']:
    rows = []
    for sub in ['', '/m0b']:
        g = {}; sys.argv = ['m0.py', W + sub]; exec(open(f'{W}/m0.py').read().split("SHAPES = [")[0], g)
        TXT = {}
        for sp, ds in g['docs'].items():
            for d_ in ds: TXT[(d_['o'], d_['d'], d_['r'], d_['c'])] = d_['text']
        for q in [q for q in g['Q'] if q['Qid'] in g['D']]:
            r = g['D'][q['Qid']]; vl = g['vec_leg'](emb, r); qt = set(tok(q['Query']))
            c = max((len(qt & set(tok(TXT.get((h['o'], h['d'], h['r'], h['c'])) or h.get('t', '')))) / len(qt) for h in r['fts'][:3]), default=0.) if qt else 0.
            rows.append(dict(shape=q['shape'], split=q['split'], tid=q['tid'], m={k: g['rr'](g['rank_of'](route(k, r['titles'], r['fts'], vl, c), q['tid'])) for k in ROUTES}))
    shapes = ['1 word', 'id-like', 'name', 'typo', 'prefix', '2-3 words', '4-5 words', 'question', 'paste']
    out += [f'## M0 Roma\'s snapshot, {emb} (known-item MRR, {len(rows)} queries)', '',
            '| route | ' + ' | '.join(shapes) + ' | held-out | all |', '|' + '---|' * (len(shapes) + 3)]
    for k in ROUTES:
        cells = []
        for sh in shapes + ['held-out', 'all']:
            sel = [x for x in rows if sh == 'all' or (sh == 'held-out' and x['split'] == 'heldout') or x['shape'] == sh]
            v = [x['m'][k] for x in sel]; b = [x['m']['w1'] for x in sel]
            cells.append(f'{np.mean(v):.3f}' if k == 'w1' else f'{np.mean(v):.3f} ({fm(bs(v, b, [x["tid"] for x in sel]))})')
        out.append(f'| {k} | ' + ' | '.join(cells) + ' |')
    out.append('')

# public sets
SETS = ['ru', 'wiki-mix', 'miracl-ru-pool', 'miracl-de-pool', 'scifact', 'nfcorpus', 'fiqa10k', 'ru-sealed', 'ru-new-rig']
for emb in ['qwen', 'eg2']:
    per = collections.defaultdict(dict)
    for s in SETS:
        d = f'{W}/pb/{s}'
        corpus = [json.loads(l) for l in open(f'{d}/corpus.jsonl')]; T2 = {(c['o'], c['d'], c['r'], c['c']): c['text'] for c in corpus}
        rels = json.load(open(f'{d}/qrels.json'))
        for l in open(f'{d}/{emb}.sweep.jsonl'):
            r = json.loads(l); qt = set(tok(r['query']))
            c = max((len(qt & set(tok(T2.get(ckey(h), '')))) / len(qt) for h in r['fts'][:3]), default=0.) if qt else 0.
            for k in ROUTES: per[k][(s, r['qid'])] = metrics(route(k, r['titles'], r['fts'], r['vec'], c), rels[r['qid']])[0]
    out += [f'## Public sets, {emb} (nDCG@10)', '', '| route | ' + ' | '.join(SETS) + ' | all |', '|' + '---|' * (len(SETS) + 2)]
    for k in ROUTES:
        cells = []
        for s in SETS + ['all']:
            keys = [x for x in per['w1'] if s == 'all' or x[0] == s]
            v = [per[k][x] for x in keys]; b = [per['w1'][x] for x in keys]
            cells.append(f'{np.mean(v):.3f}' if k == 'w1' else f'{np.mean(v):.3f} ({fm(bs(v, b, list(range(len(v)))))})')
        out.append(f'| {k} | ' + ' | '.join(cells) + ' |')
    out.append('')
open(f'{W}/ROUTER2.md', 'w').write('\n'.join(out) + '\n'); print('\n'.join(out))
