"""Coverage router: route the vector weight by how much of the query the top FTS hits contain (a query-time signal).

python router.py <searchbench dir>  -> ROUTER.md
Evaluates on M0 (Roma's snapshot, known-item MRR, tune/held-out) and on the public sets (graded nDCG@10).
"""
import json, sys, re, collections
import numpy as np

W = sys.argv[1]
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
LIMIT = 10
exec(open(f'{W}/pb_analyze.py').read().split('rows_all = []')[0].split('PB = sys.argv[1]')[0])  # imports only
src = open(f'{W}/pb_analyze.py').read()
exec(src[src.index('def rf32'):src.index('rows_all = []')])


def coverage(q, fts, txt):
    qt = set(tok(q))
    if not qt or not fts: return 0.
    return max(len(qt & set(tok(txt(h)))) / len(qt) for h in fts[:3])


ROUTES = {
    'w1': lambda c: (1, 1),
    'w2': lambda c: (1, 2),
    'w0.5': lambda c: (1, .5),
    'A: full→w0.5 · else w1': lambda c: (1, .5) if c >= 1 else (1, 1),
    'B: full→w0.5 · <0.5→vec only · else w1': lambda c: (1, .5) if c >= 1 else ((0, 1) if c < .5 else (1, 1)),
    'C: full→w0.5 · <0.5→vec only · else w2': lambda c: (1, .5) if c >= 1 else ((0, 1) if c < .5 else (1, 2)),
    'D: full→w1 · <0.5→vec only · else w2': lambda c: (1, 1) if c >= 1 else ((0, 1) if c < .5 else (1, 2)),
    'E: full→w0.5 · <1→vec only': lambda c: (1, .5) if c >= 1 else (0, 1),
}


def bstat(a, b, n=2000, groups=None, seed=391):
    d = np.array(a) - np.array(b); rnd = np.random.default_rng(seed)
    if groups is None:
        bs = np.sort(d[rnd.integers(0, len(d), (n, len(d)))].mean(1))
    else:
        g = collections.defaultdict(list)
        for i, t in enumerate(groups): g[t].append(i)
        keys = list(g); bs = []
        for _ in range(n):
            idx = [i for k in rnd.integers(0, len(keys), len(keys)) for i in g[keys[k]]]
            bs.append(d[idx].mean())
        bs = np.sort(bs)
    return d.mean(), bs[int(.025 * n)], bs[int(.975 * n) - 1]


fm = lambda t: f"{t[0]:+.3f}{'*' if t[1] > 0 or t[2] < 0 else ''}"
out = ['# Coverage router', '',
       'cov = share of the query words (stop words removed) found in the best of the top-3 FTS chunks, full chunk text. Δ against w1; * = 95% CI excludes 0.', '']

# ---------- M0 (Roma's snapshot), known-item MRR, reuse m0 machinery
m0src = open(f'{W}/m0.py').read().split("SHAPES = [")[0]
g = {'__name__': 'm0'}; sys.argv = ['m0.py', W]; exec(m0src, g)
TXT = {}
for sp, ds in g['docs'].items():
    for d_ in ds: TXT[(d_['o'], d_['d'], d_['r'], d_['c'])] = d_['text']
txt = lambda h: TXT.get((h['o'], h['d'], h['r'], h['c'])) or h.get('t', '')
qs = [q for q in g['Q'] if q['Qid'] in g['D']]
for emb in ['qwen', 'eg2']:
    res = collections.defaultdict(dict); covs = {}
    for q in qs:
        r = g['D'][q['Qid']]; vl = g['vec_leg'](emb, r); c = coverage(q['Query'], r['fts'], txt); covs[q['Qid']] = c
        for k, fn in ROUTES.items():
            wf, wv = fn(c)
            res[k][q['Qid']] = g['rr'](g['rank_of'](g['fuse'](r['titles'], r['fts'], vl, wf, wv), q['tid']))
    out += [f'## M0, Roma\'s snapshot, {emb} vector leg (known-item MRR)', '', '| route | tune (340) | held-out (339) | 1 word | id-like | 2-5 words | question | all |', '|---|---|---|---|---|---|---|---|']
    for k in ROUTES:
        cells = []
        for name, sel in [('tune', [q for q in qs if q['split'] == 'tune']), ('held', [q for q in qs if q['split'] == 'heldout']),
                          ('1w', [q for q in qs if q['shape'] == '1 word']), ('id', [q for q in qs if q['shape'] == 'id-like']),
                          ('mid', [q for q in qs if q['shape'] in ('2-3 words', '4-5 words')]), ('qn', [q for q in qs if q['shape'] == 'question']), ('all', qs)]:
            v = [res[k][q['Qid']] for q in sel]; b = [res['w1'][q['Qid']] for q in sel]
            cells.append(f'{np.mean(v):.3f}' if k == 'w1' else f'{np.mean(v):.3f} ({fm(bstat(v, b, groups=[q["tid"] for q in sel]))})')
        out.append(f'| {k} | ' + ' | '.join(cells) + ' |')
    cb = collections.Counter('full' if c >= 1 else '<0.5' if c < .5 else 'partial' for c in covs.values())
    out += ['', f'coverage buckets: {dict(cb)}', '']

# ---------- public sets, graded nDCG@10
PB = f'{W}/pb'
SETS = ['ru', 'wiki-mix', 'miracl-ru-pool', 'miracl-de-pool', 'scifact', 'nfcorpus', 'fiqa10k', 'ru-sealed', 'ru-new-rig']
for emb in ['qwen', 'eg2']:
    out += [f'## Public sets, {emb} vector leg (nDCG@10)', '', '| route | ' + ' | '.join(SETS) + ' | all |', '|' + '---|' * (len(SETS) + 2)]
    per = {k: {} for k in ROUTES}; per['L1 vector only'] = {}
    for s in SETS:
        import os
        d = f'{PB}/{s}'
        if not os.path.exists(f'{d}/{emb}.sweep.jsonl'): continue
        corpus = [json.loads(l) for l in open(f'{d}/corpus.jsonl')]
        T2 = {(c['o'], c['d'], c['r'], c['c']): c['text'] for c in corpus}
        qsx = [json.loads(l) for l in open(f'{d}/queries.jsonl')]; QI = {q['Qid']: i for i, q in enumerate(qsx)}
        rels = json.load(open(f'{d}/qrels.json'))
        sw = {json.loads(l)['qid']: json.loads(l) for l in open(f'{d}/{emb}.sweep.jsonl')}
        Dv, Qv = rf32(f'{d}/{emb}.docs.f32'), rf32(f'{d}/{emb}.q.f32')
        for q in qsx:
            r = sw[q['Qid']]; c = coverage(q['Query'], r['fts'], lambda h: T2.get(ckey(h), ''))
            for k, fn in ROUTES.items():
                wf, wv = fn(c)
                per[k][(s, q['Qid'])] = metrics(fuse(r['titles'], r['fts'], r['vec'], wf, wv), rels[q['Qid']])[0]
            sc = Dv @ Qv[QI[q['Qid']]]; top = np.argsort(-sc)[:30]; l1, seen = [], set()
            for i in top:
                cc = corpus[i]; key = (cc['o'], cc['d'], cc['r'])
                if key in seen: continue
                seen.add(key); l1.append(dict(o=cc['o'], d=cc['d'], r=cc['r'], c=cc['c']))
            per['L1 vector only'][(s, q['Qid'])] = metrics(l1, rels[q['Qid']])[0]
    for k in list(ROUTES) + ['L1 vector only']:
        cells = []
        for s in SETS + ['all']:
            keys = [x for x in per['w1'] if s == 'all' or x[0] == s]
            if not keys: cells.append('–'); continue
            v = [per[k][x] for x in keys]; b = [per['w1'][x] for x in keys]
            cells.append(f'{np.mean(v):.3f}' if k == 'w1' else f'{np.mean(v):.3f} ({fm(bstat(v, b))})')
        out.append(f'| {k} | ' + ' | '.join(cells) + ' |')
    out.append('')
open(f'{W}/ROUTER.md', 'w').write('\n'.join(out) + '\n')
print('\n'.join(out))
