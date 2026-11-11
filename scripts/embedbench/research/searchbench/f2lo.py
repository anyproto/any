"""Router F2 low-cut sensitivity on M0 (+prefix/paste) and 9 public sets, both embedders; Δ against w1 with bootstrap. -> F2LO.md"""
import json, sys, re, collections
import numpy as np
W = sys.argv[1]; LIMIT = 10
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
pb = open(f'{W}/pb_analyze.py').read(); exec(pb[pb.index('def rf32'):pb.index('rows_all = []')])
cases = []  # (dataset, emb, group, titles, fts, vec, cov, scorer)
for emb in ['qwen', 'eg2']:
    for sub in ['', '/m0b']:
        g = {}; sys.argv = ['m0.py', W + sub]; exec(open(f'{W}/m0.py').read().split("SHAPES = [")[0], g)
        TXT = {}
        for sp, ds in g['docs'].items():
            for d_ in ds: TXT[(d_['o'], d_['d'], d_['r'], d_['c'])] = d_['text']
        for q in [q for q in g['Q'] if q['Qid'] in g['D']]:
            r = g['D'][q['Qid']]; vl = g['vec_leg'](emb, r); qt = set(tok(q['Query']))
            c = max((len(qt & set(tok(TXT.get((h['o'], h['d'], h['r'], h['c'])) or h.get('t', '')))) / len(qt) for h in r['fts'][:3]), default=0.) if qt else 0.
            tid = q['tid']; rank_of, rr = g['rank_of'], g['rr']
            cases.append(('M0', emb, tid, r['titles'], r['fts'], vl, c, (lambda p, tid=tid, rank_of=rank_of, rr=rr: rr(rank_of(p, tid)))))
    for s in ['ru', 'wiki-mix', 'miracl-ru-pool', 'miracl-de-pool', 'scifact', 'nfcorpus', 'fiqa10k', 'ru-sealed', 'ru-new-rig']:
        d = f'{W}/pb/{s}'; corpus = [json.loads(l) for l in open(f'{d}/corpus.jsonl')]
        T2 = {(c['o'], c['d'], c['r'], c['c']): c['text'] for c in corpus}; rels = json.load(open(f'{d}/qrels.json'))
        for l in open(f'{d}/{emb}.sweep.jsonl'):
            r = json.loads(l); qt = set(tok(r['query']))
            c = max((len(qt & set(tok(T2.get(ckey(h), '')))) / len(qt) for h in r['fts'][:3]), default=0.) if qt else 0.
            rel = rels[r['qid']]
            cases.append(('public', emb, (s, r['qid']), r['titles'], r['fts'], r['vec'], c, (lambda p, rel=rel: metrics(p, rel)[0])))

def bs(d, groups, n=2000, seed=391):
    d = np.array(d); rnd = np.random.default_rng(seed); g = collections.defaultdict(list)
    for i, t in enumerate(groups): g[t].append(i)
    keys = list(g); o = []
    for _ in range(n):
        idx = [i for k in rnd.integers(0, len(keys), len(keys)) for i in g[keys[k]]]; o.append(d[idx].mean())
    o = np.sort(o); return d.mean(), o[int(.025 * n)], o[int(.975 * n) - 1]
fm = lambda t: f"{t[0]:+.3f}{'*' if t[1] > 0 or t[2] < 0 else ''}"
def f2(lo):
    return lambda t, f, v, c: blend(t, f, v, .3) if c >= 1 else (fuse(t, [], v, 0, 1) if c < lo else fuse(t, f, v, 1, 2))
V = {'w2': lambda t, f, v, c: fuse(t, f, v, 1, 2)}
for lo in [0.75, 0.5, 0.25, 0.0]: V[f'F2 low cut {lo}'] = f2(lo)
out = ['# Router F2: sensitivity to the low (vector-only) cut', '', 'F2 = full coverage → min-max blend α0.3; coverage < cut → vector leg only; otherwise RRF w2. Cut 0 = two branches only. Δ against RRF w1; * = 95% CI excludes 0 (M0 clustered by target).', '',
       '| variant | M0 qwen | M0 eg2 | public qwen | public eg2 |', '|---|---|---|---|---|']
base = lambda t, f, v, c: fuse(t, f, v, 1, 1)
for name, fn in V.items():
    cells = []
    for ds in ['M0', 'public']:
        for emb in ['qwen', 'eg2']:
            sel = [x for x in cases if x[0] == ds and x[1] == emb]
            a = [x[7](fn(*x[3:7])) for x in sel]; b = [x[7](base(*x[3:7])) for x in sel]
            cells.append(f'{np.mean(a):.3f} ({fm(bs(np.array(a) - np.array(b), [x[2] for x in sel]))})')
    out.append(f'| {name} | ' + ' | '.join([cells[0], cells[1], cells[2], cells[3]]) + ' |')
open(f'{W}/F2LO.md', 'w').write('\n'.join(out) + '\n'); print('\n'.join(out))
