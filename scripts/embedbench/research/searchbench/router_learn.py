"""Learned query router: per-query features from the two legs -> one of 7 fusion actions, a greedy depth-1/2 tree
trained on the tune halves of M0, AirM and the 9 public sets (equal weight per dataset), scored on held-out halves.
Qwen3 production legs. python router_learn.py <searchbench dir> -> ROUTER_LEARN.md"""
import json, sys, re, collections, hashlib
import numpy as np
W = sys.argv[1]; LIMIT = 10
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
pb = open(f'{W}/pb_analyze.py').read(); exec(pb[pb.index('def rf32'):pb.index('rows_all = []')])
ACT = {'fts only': lambda t, f, v: fuse(t, f, [], 1, 0), 'vector only': lambda t, f, v: fuse(t, [], v, 0, 1),
       'w0.5': lambda t, f, v: fuse(t, f, v, 1, .5), 'w1': lambda t, f, v: fuse(t, f, v, 1, 1), 'w2': lambda t, f, v: fuse(t, f, v, 1, 2),
       'blend.3': lambda t, f, v: blend(t, f, v, .3), 'blend.7': lambda t, f, v: blend(t, f, v, .7)}
def feats(query, titles, fts, vec, txt):
    qt = set(tok(query)); c = max((len(qt & set(tok(txt(h)))) / len(qt) for h in fts[:3]), default=0.) if qt else 0.
    fs = [h['s'] for h in fts]; vs = [h['s'] for h in vec]
    rf = []; [rf.append(gkey(h)) for h in fts if gkey(h) not in rf]; rv = []; [rv.append(gkey(h)) for h in vec if gkey(h) not in rv]
    a, b = set(rf[:5]), set(rv[:5])
    return dict(cov=c, fgap=(fs[0] - fs[1]) / fs[0] if len(fs) > 1 and fs[0] > 0 else (1. if fs else 0.), nfts=len(rf),
                vtop=vs[0] if vs else 0., vgap=(vs[0] - vs[min(4, len(vs) - 1)]) if vs else 0.,
                agree=len(a & b) / max(1, len(a | b)), qlen=len(query.split()), titles=len(titles))
cases = []  # dict(ds, split, group, f, m{action: score})
for sub in ['', '/m0b']:
    g = {}; sys.argv = ['m0.py', W + sub]; exec(open(f'{W}/m0.py').read().split("SHAPES = [")[0], g)
    TXT = {}
    for sp, ds in g['docs'].items():
        for d_ in ds: TXT[(d_['o'], d_['d'], d_['r'], d_['c'])] = d_['text']
    tx = lambda h, TXT=TXT: TXT.get((h['o'], h['d'], h['r'], h['c'])) or h.get('t', '')
    for q in [q for q in g['Q'] if q['Qid'] in g['D']]:
        r = g['D'][q['Qid']]; vl = r['vec']
        m = {k: g['rr'](g['rank_of'](fn(r['titles'], r['fts'], vl), q['tid'])) for k, fn in ACT.items()}
        cases.append(dict(ds='M0', split=q['split'], group=q['tid'], shape=q['shape'], f=feats(q['Query'], r['titles'], r['fts'], vl, tx), m=m))
A = f'{W}/air'; adocs = {}
for l in open(f'{A}/docs.jsonl'):
    d = json.loads(l); adocs[(d['o'], d['d'], d['r'], d['c'])] = d['text']
AT = {t['tid']: t for t in json.load(open(f'{W}/airm/targets.json'))}
AD = {json.loads(l)['qid']: json.loads(l) for l in open(f'{W}/airm/sweep.jsonl')}
for l in open(f'{W}/airm/queries.jsonl'):
    q = json.loads(l)
    if q.get('rewrite'): continue
    r = AD[q['Qid']]; t = AT[q['tid']]
    sc = lambda p: next((1 / (i + 1) for i, h in enumerate(p[:LIMIT]) if h['o'] == t['o']), 0.)
    m = {k: sc(fn(r['titles'], r['fts'], r['vec'])) for k, fn in ACT.items()}
    cases.append(dict(ds='AirM', split=q['split'], group=q['tid'], shape=q['shape'], f=feats(q['Query'], r['titles'], r['fts'], r['vec'], lambda h: adocs.get(ckey(h), h.get('t', ''))), m=m))
for s in ['ru', 'wiki-mix', 'miracl-ru-pool', 'miracl-de-pool', 'scifact', 'nfcorpus', 'fiqa10k', 'ru-sealed', 'ru-new-rig']:
    d = f'{W}/pb/{s}'; T2 = {}
    for l in open(f'{d}/corpus.jsonl'):
        c = json.loads(l); T2[(c['o'], c['d'], c['r'], c['c'])] = c['text']
    rels = json.load(open(f'{d}/qrels.json'))
    for l in open(f'{d}/qwen.sweep.jsonl'):
        r = json.loads(l); rel = rels[r['qid']]
        m = {k: metrics(fn(r['titles'], r['fts'], r['vec']), rel)[0] for k, fn in ACT.items()}
        sp = 'tune' if int(hashlib.sha1(f'{s}|{r["qid"]}'.encode()).hexdigest()[:6], 16) % 2 == 0 else 'heldout'
        cases.append(dict(ds='public', split=sp, group=(s, r['qid']), shape=s, f=feats(r['query'], r['titles'], r['fts'], r['vec'], lambda h, T2=T2: T2.get(ckey(h), '')), m=m))
DS = ['M0', 'AirM', 'public']
def objective(sel, rule):
    # equal-weight mean over datasets of the mean metric of the chosen action
    return float(np.mean([np.mean([x['m'][rule(x['f'])] for x in sel if x['ds'] == ds]) for ds in DS if any(x['ds'] == ds for x in sel)]))
def best_leaf(sel):
    return max(ACT, key=lambda a: objective(sel, lambda f, a=a: a))
FEATS = ['cov', 'fgap', 'nfts', 'vtop', 'vgap', 'agree', 'qlen', 'titles']
def thresholds(sel, fe):
    v = np.array([x['f'][fe] for x in sel]); return sorted(set(np.quantile(v, np.linspace(.1, .9, 9)).round(4)))
def split1(sel):
    best = (objective(sel, lambda f, a=best_leaf(sel): a), None)
    for fe in FEATS:
        for th in thresholds(sel, fe):
            L = [x for x in sel if x['f'][fe] < th]; R = [x for x in sel if x['f'][fe] >= th]
            if len(L) < 30 or len(R) < 30: continue
            al, ar = best_leaf(L), best_leaf(R)
            rule = lambda f, fe=fe, th=th, al=al, ar=ar: al if f[fe] < th else ar
            o = objective(sel, rule)
            if o > best[0] + 1e-9: best = (o, (fe, th, al, ar))
    return best
train = [x for x in cases if x['split'] == 'tune']; test = [x for x in cases if x['split'] != 'tune']
o1, s1 = split1(train)
fe, th, al, ar = s1
L = [x for x in train if x['f'][fe] < th]; R = [x for x in train if x['f'][fe] >= th]
_, sl = split1(L); _, sr = split1(R)
def tree2(f):
    if f[fe] < th:
        if sl: return sl[2] if f[sl[0]] < sl[1] else sl[3]
        return al
    if sr: return sr[2] if f[sr[0]] < sr[1] else sr[3]
    return ar
RULES = {'w1': lambda f: 'w1', 'w2': lambda f: 'w2', 'best single action': (lambda a: (lambda f: a))(best_leaf(train)),
         'F2 two-branch': lambda f: 'blend.3' if f['cov'] >= 1 else 'w2',
         'F2 lo0.5': lambda f: 'blend.3' if f['cov'] >= 1 else ('vector only' if f['cov'] < .5 else 'w2'),
         'learned depth 1': lambda f: al if f[fe] < th else ar, 'learned depth 2': tree2}
def bs(a, b, groups, n=2000, seed=391):
    d = np.array(a) - np.array(b); rnd = np.random.default_rng(seed); gr = collections.defaultdict(list)
    for i, t in enumerate(groups): gr[t].append(i)
    keys = list(gr); o = []
    for _ in range(n):
        idx = [i for k in rnd.integers(0, len(keys), len(keys)) for i in gr[keys[k]]]; o.append(d[idx].mean())
    o = np.sort(o); return d.mean(), o[int(.025 * n)], o[int(.975 * n) - 1]
fm = lambda t: f"{t[0]:+.3f}{'*' if t[1] > 0 or t[2] < 0 else ''}"
out = ['# Learned query router (Qwen3 production legs)', '',
       f'Features per query: {", ".join(FEATS)} (cov = FTS coverage; fgap = (bm25 top1 − top2)/top1; vtop = top cosine; vgap = top1 − top5 cosine; agree = Jaccard of the legs\' top-5 records). Actions: {", ".join(ACT)}.',
       f'Greedy tree, leaves pick the action with the best equal-weight mean over M0 (MRR), AirM (article MRR) and the 9 public sets (nDCG@10); trained on tune halves ({len(train)} queries), scored on held-out halves ({len(test)}). Held-out = new targets for M0/AirM, a hash half of queries for public.', '',
       f'Learned depth 1: {fe} < {th} → {al}, else → {ar}.',
       f'Learned depth 2: left ({fe} < {th}): ' + (f'{sl[0]} < {sl[1]} → {sl[2]}, else {sl[3]}' if sl else al) + f'; right: ' + (f'{sr[0]} < {sr[1]} → {sr[2]}, else {sr[3]}' if sr else ar) + '.', '',
       '| rule | M0 held-out | Δ vs w1 | AirM held-out | Δ vs w1 | public held-out | Δ vs w1 | train objective |', '|---|---|---|---|---|---|---|---|']
for name, rule in RULES.items():
    cells = []
    for ds in DS:
        sel = [x for x in test if x['ds'] == ds]
        a = [x['m'][rule(x['f'])] for x in sel]; b = [x['m']['w1'] for x in sel]
        cells += [f'{np.mean(a):.3f}', '' if name == 'w1' else fm(bs(a, b, [x['group'] for x in sel]))]
    out.append(f'| {name} | ' + ' | '.join(cells) + f' | {objective(train, rule):.3f} |')
RULES['two blends (cov=1 → α0.3, else α0.7)'] = lambda f: 'blend.3' if f['cov'] >= 1 else 'blend.7'
out += ['', '## Against F2 two-branch (held-out; * = 95% CI excludes 0)', '', '| rule | M0 | AirM | public |', '|---|---|---|---|']
for name in ['learned depth 1', 'learned depth 2', 'two blends (cov=1 → α0.3, else α0.7)', 'F2 lo0.5']:
    rule = RULES[name]; cells = []
    for ds in DS:
        sel = [x for x in test if x['ds'] == ds]
        a = [x['m'][rule(x['f'])] for x in sel]; b = [x['m']['blend.3' if x['f']['cov'] >= 1 else 'w2'] for x in sel]
        cells.append(fm(bs(a, b, [x['group'] for x in sel])))
    out.append(f'| {name} | ' + ' | '.join(cells) + ' |')
out.append('')
orc = [np.mean([max(x['m'].values()) for x in test if x['ds'] == ds]) for ds in DS]
out += [f'| per-query oracle | {orc[0]:.3f} | | {orc[1]:.3f} | | {orc[2]:.3f} | | |', '']
# which action the oracle wants, by coverage bucket
out += ['## Which action wins per query (oracle ties broken toward w1), by coverage bucket, held-out + train', '']
for ds in DS:
    for bk, f in [('full', lambda c: c >= 1), ('partial', lambda c: .5 <= c < 1), ('<0.5', lambda c: c < .5)]:
        sel = [x for x in cases if x['ds'] == ds and f(x['f']['cov'])]
        if not sel: continue
        cnt = collections.Counter()
        for x in sel:
            mx = max(x['m'].values())
            if x['m']['w1'] == mx: cnt['w1 (tie)'] += 1; continue
            cnt[max(x['m'], key=lambda k: x['m'][k])] += 1
        out.append(f"- {ds} {bk} ({len(sel)}): " + ', '.join(f'{k} {v}' for k, v in cnt.most_common(5)))
out.append('')
open(f'{W}/ROUTER_LEARN.md', 'w').write('\n'.join(out) + '\n'); print('\n'.join(out))
