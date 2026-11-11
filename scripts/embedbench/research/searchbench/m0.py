"""SearchBench M0 analysis (SYN-391). Layers: L0 integrity, L1 vector-only, L2 FTS-only, L3 fusion on frozen legs.

python m0.py <workdir>   writes <workdir>/RESULTS.md and results.json (aggregates only)
"""
import json, sys, re, collections, random
import numpy as np

W = sys.argv[1]
SP = {'bafyreia756b': 'bafyreia756bc7zleehqtrlusiluqayvwobzhj5qzvuy4gbfkfzbezm6wsy.2lhdbvetdrq52',
      'bafyreicqlpc': 'bafyreicqlpctaey5b6qzgr3lzohftdqln63payz3zrg4onar3ozjzilki4.n2uaqee5lbvr'}
LIMIT = 10
T = {t['tid']: t for t in json.load(open(f'{W}/targets_all.json'))}
Q = [json.loads(l) for l in open(f'{W}/queries.jsonl')]
D = {json.loads(l)['qid']: json.loads(l) for l in open(f'{W}/sweep.jsonl')} if True else {}


def rf32(p):
    b = open(p, 'rb').read(); n, d = np.frombuffer(b[:8], '<u4')
    v = np.frombuffer(b[8:], '<f4').reshape(int(n), int(d)).astype(np.float32)
    return v / np.linalg.norm(v, axis=1, keepdims=True)


def norm(v):
    return v / np.linalg.norm(v, axis=-1, keepdims=True)


u = np.load('/Users/roman/anytype/any-wt-syn391/scripts/embedbench/research/eg2_head_patch/head_mean.npy').astype(np.float32)
head = lambda v: norm(v - np.outer(v @ u, u))

docs, vecs = {}, {'qwen': {}, 'eg2': {}, 'eg2raw': {}}
eg2_all = rf32(f'{W}/eg2-docs.docs.f32'); off = 0
for short, sp in SP.items():
    ds = [json.loads(l) for l in open(f'{W}/docs-{short}.jsonl')]
    docs[sp] = ds
    vecs['qwen'][sp] = norm(np.load(f'{W}/qwen-docs-{short}.npy').astype(np.float32))
    e = eg2_all[off:off + len(ds)]; off += len(ds)
    vecs['eg2raw'][sp] = e
    vecs['eg2'][sp] = head(e)
assert off == len(eg2_all)
qv = {'qwen': rf32(f'{W}/qwen-q.docs.f32')}
qe = rf32(f'{W}/eg2-q.docs.f32'); qv['eg2raw'] = qe; qv['eg2'] = head(qe)
QI = {q['Qid']: i for i, q in enumerate(Q)}
EMB = ['qwen', 'eg2', 'eg2raw']


def gkey(h): return (h['o'], h['d'], h['r'])
def ckey(h): return (h['o'], h['d'], h['r'], h['c'])


def exact_sorted(emb, sp, qid):
    s = vecs[emb][sp] @ qv[emb][QI[qid]]
    order = np.lexsort((np.arange(len(s)), -s))
    ds = docs[sp]
    return [dict(o=ds[i]['o'], d=ds[i]['d'], r=ds[i]['r'], c=ds[i]['c'], s=float(s[i])) for i in order if s[i] > 0]


def vec_leg(emb, r):
    """exact leg with production's window rule: K from fetch, x4 until >= fetch chunks and >= rest records."""
    rest = LIMIT - len(r['titles'])
    if rest <= 0:
        return []
    fetch = min(max(rest * 3, 30), 100)
    skip = {gkey(h) for h in r['titles']}
    allh = exact_sorted(emb, r['space'], r['qid'])
    k = fetch
    while True:
        kept = [h for h in allh[:k] if gkey(h) not in skip]
        if (len(kept) >= fetch and len({gkey(h) for h in kept}) >= rest) or k >= 1000 or k >= len(allh):
            return kept
        k = min(k * 4, 1000)


def group(hits, rest):
    groups = {}
    for h in hits:
        g = gkey(h)
        if g not in groups:
            groups[g] = h
    return list(groups.values())[:rest]


def fuse(titles, fts, vec, wf=1., wv=1., k=60):
    rest = LIMIT - len(titles)
    if rest <= 0:
        return titles[:LIMIT]
    acc = {}
    for lst, w in ((fts, wf), (vec, wv)):
        if w <= 0:
            continue
        for rank, h in enumerate(lst):
            kk = ckey(h)
            if kk not in acc:
                acc[kk] = [h, 0.]
            acc[kk][1] += w / (k + rank + 1)
    fused = sorted(acc.values(), key=lambda a: (-a[1], a[0]['o'], a[0]['d'], a[0]['r'], a[0]['c']))
    best = {}
    for h, s in fused:
        g = gkey(h)
        if g not in best or s > best[g][1]:
            best[g] = (h, s)
    gs = sorted(best.values(), key=lambda a: (-a[1], a[0]['o'], a[0]['d'], a[0]['r']))
    return titles + [h for h, _ in gs[:rest]]


def blend(titles, fts, vec, alpha):
    rest = LIMIT - len(titles)
    if rest <= 0:
        return titles[:LIMIT]
    def mm(lst):
        if not lst: return {}
        s = [h['s'] for h in lst]; lo, hi = min(s), max(s)
        return {ckey(h): ((h['s'] - lo) / (hi - lo) if hi > lo else 1.) for h in lst}
    F, V = mm(fts), mm(vec); H = {ckey(h): h for h in fts + vec}
    sc = sorted(((1 - alpha) * F.get(k, 0) + alpha * V.get(k, 0), k) for k in H)
    best = {}
    for s, k in sorted(sc, key=lambda a: (-a[0], a[1])):
        if k[:3] not in best:
            best[k[:3]] = H[k]
    return titles + list(best.values())[:rest]


def rank_of(page, tid):
    t = T[tid]
    if t['kind'] == 'editor_blocks':
        m = lambda h: h['o'] == t['objectId']
    else:
        m = lambda h: (h['o'], h['d'], h['r']) == (t['objectId'], t['dataset'], t['recordId'])
    for i, h in enumerate(page[:LIMIT]):
        if m(h):
            return i + 1
    return None


rr = lambda x: 1 / x if x else 0.


def boot(a, b, tids, n=3000, seed=391):
    """paired cluster bootstrap over targets: mean(a-b), 95% CI"""
    d = np.array(a) - np.array(b)
    groups = collections.defaultdict(list)
    for i, t in enumerate(tids):
        groups[t].append(i)
    keys = list(groups); rnd = random.Random(seed)
    m = float(d.mean()); bs = []
    for _ in range(n):
        idx = [i for _ in keys for i in groups[keys[rnd.randrange(len(keys))]]]
        bs.append(d[idx].mean())
    bs.sort()
    return m, bs[int(.025 * n)], bs[int(.975 * n) - 1]


def fmt(d):
    m, lo, hi = d
    return f"{m:+.3f}{'*' if lo > 0 or hi < 0 else ''} [{lo:+.3f}, {hi:+.3f}]"


SHAPES = ['1 word', 'id-like', 'name', 'typo', 'prefix', '2-3 words', '4-5 words', 'question', 'paste']
qs = [q for q in Q if q['Qid'] in D]
out = ['# SearchBench M0: results', '',
       f"Snapshot: Roma's Any Nightly (Team Agents, Agents), {len(qs)} queries on {len({q['tid'] for q in qs})} targets.",
       'Known-item MRR@10. A page target matches by object (title hits count); memories, chats and config match by record.',
       'Δ = paired cluster bootstrap over targets, 95% CI; * = CI excludes 0.', '']

# ---------- L0 integrity
ov, same, mism, emptyv = [], 0, 0, 0
for q in qs:
    r = D[q['Qid']]
    prod = [ckey(h) for h in r['vec']][:10]
    ex = [ckey(h) for h in vec_leg('qwen', r)][:10]
    if prod:
        ov.append(len(set(prod) & set(ex)) / max(1, len(prod)))
        same += prod == ex
    for ws, p in r['pages'].items():
        off_ = [gkey(h) for h in fuse(r['titles'], r['fts'], r['vec'], 1, float(ws))]
        mism += off_ != [gkey(h) for h in p['hits']]
        if p['vectorStatus'] == 'used' and LIMIT - len(r['titles']) > 0 and not r['vec']:
            emptyv += 1
out += ['## L0 integrity', '',
        f'- Offline re-fusion against the production page, all weights: {mism} mismatches.',
        f'- Pages reporting vectorStatus "used" with an empty vector leg: {emptyv}.',
        f'- Production Qwen3 vector leg (app build b10620) against exact search (b11600 Metal query vectors), top-10 chunks:',
        f'  mean overlap {np.mean(ov):.3f}, identical top-10 on {same}/{len(ov)} queries.', '']

# ---------- pages per system
def pages(q):
    r = D[q['Qid']]; P = {}
    for e in EMB:
        allh = exact_sorted(e, r['space'], q['Qid'])
        P[f'L1 {e}'] = group(allh, LIMIT)
    P['L2 fts+titles'] = fuse(r['titles'], r['fts'], [], 1, 0)
    P['L2 fts no titles'] = group(r['fts'], LIMIT)
    for e in ['qwen', 'eg2']:
        vl = vec_leg(e, r)
        for w in [0.5, 1, 2]:
            P[f'L3 {e} w{w}'] = fuse(r['titles'], r['fts'], vl, 1, w)
        w_shape = 1 if q['shape'] in ('1 word', 'id-like', 'typo', 'name') else 2
        P[f'L3 {e} shape'] = fuse(r['titles'], r['fts'], vl, 1, w_shape)
        P[f'L3 {e} blend.4'] = blend(r['titles'], r['fts'], vl, .4)
        P[f'L3 {e} vec+titles'] = fuse(r['titles'], [], vl, 0, 1)
    return P

R = {}
for q in qs:
    R[q['Qid']] = {k: rank_of(p, q['tid']) for k, p in pages(q).items()}
systems = list(next(iter(R.values())).keys())


def table(title, cols, base=None, split=None, note=''):
    rows = ['| shape (n) | ' + ' | '.join(cols) + ' |', '|' + '---|' * (len(cols) + 1)]
    for sh in SHAPES + ['all']:
        sel = [q for q in qs if (sh == 'all' or q['shape'] == sh) and (split is None or q['split'] == split)]
        if not sel: continue
        cells = []
        for c in cols:
            v = [rr(R[q['Qid']][c]) for q in sel]
            if base and c != base:
                b = [rr(R[q['Qid']][base]) for q in sel]
                cells.append(f'{np.mean(v):.3f} ({fmt(boot(v, b, [q["tid"] for q in sel]))})')
            else:
                cells.append(f'{np.mean(v):.3f}')
        rows.append(f'| {sh} ({len(sel)}) | ' + ' | '.join(cells) + ' |')
    return [f'## {title}', ''] + ([note, ''] if note else []) + rows + ['']


out += table('L1 vector only (exact, no titles, no FTS)', ['L1 qwen', 'L1 eg2', 'L1 eg2raw'], base='L1 qwen',
             note='EG2 = EG2 with the head projection; eg2raw = stock EG2 output. Δ against Qwen3.')
out += table('L2 FTS only', ['L2 fts+titles', 'L2 fts no titles'], note='Production FTS leg and title pass, grouped like production.')
for e in ['qwen', 'eg2']:
    out += table(f'L3 fusion, {e} vector leg (Δ against w1)',
                 [f'L3 {e} w1', f'L3 {e} w0.5', f'L3 {e} w2', f'L3 {e} shape', f'L3 {e} blend.4'], base=f'L3 {e} w1',
                 note='Legs: production title block and FTS leg, exact vector leg with production window rule.')
for sp in ['tune', 'heldout']:
    out += table(f'L3 Qwen3 rules on the {sp} split', ['L3 qwen w1', 'L3 qwen w2', 'L3 qwen shape', 'L3 qwen blend.4'],
                 base='L3 qwen w1', split=sp,
                 note='The shape rule and α=0.4 were picked on the first 177 tune queries; the held-out split has new targets.')
out += table('Embedder inside fusion: EG2 − Qwen3 at the same rule', ['L3 qwen w1', 'L3 eg2 w1'], base='L3 qwen w1')
out += table('Embedder inside fusion at w2', ['L3 qwen w2', 'L3 eg2 w2'], base='L3 qwen w2')

# oracle bounds and leg preference
out += ['## Oracle bounds and which leg wins', '',
        '| shape | Qwen3 w1 | best rule per query (Qwen3) | FTS-only or vector-only, better per query | FTS better | vector better | tie |',
        '|---|---|---|---|---|---|---|']
for sh in SHAPES + ['all']:
    sel = [q for q in qs if sh == 'all' or q['shape'] == sh]
    w1 = np.mean([rr(R[q['Qid']]['L3 qwen w1']) for q in sel])
    rules = [k for k in systems if k.startswith('L3 qwen')]
    orc = np.mean([max(rr(R[q['Qid']][k]) for k in rules) for q in sel])
    legs = np.mean([max(rr(R[q['Qid']]['L2 fts+titles']), rr(R[q['Qid']]['L3 qwen vec+titles'])) for q in sel])
    f = sum(rr(R[q['Qid']]['L2 fts+titles']) > rr(R[q['Qid']]['L3 qwen vec+titles']) for q in sel)
    v = sum(rr(R[q['Qid']]['L2 fts+titles']) < rr(R[q['Qid']]['L3 qwen vec+titles']) for q in sel)
    out.append(f'| {sh} ({len(sel)}) | {w1:.3f} | {orc:.3f} | {legs:.3f} | {f} | {v} | {len(sel) - f - v} |')
out.append('')

# lexical overlap buckets: query tokens found in the target text
tok = lambda s: set(re.findall(r'\w+', s.lower()))
out += ['## FTS vs vector by query-target word overlap (Qwen3)', '',
        '| overlap | n | FTS+titles | vector+titles | w1 | w2 |', '|---|---|---|---|---|---|']
buck = collections.defaultdict(list)
for q in qs:
    qt = tok(q['Query']); tt = tok(T[q['tid']]['text'] + ' ' + (T[q['tid']].get('objectName') or ''))
    o = len(qt & tt) / max(1, len(qt))
    buck['0' if o == 0 else '<0.5' if o < .5 else '<1' if o < 1 else '1 (all words)'].append(q)
for b in ['0', '<0.5', '<1', '1 (all words)']:
    sel = buck[b]
    if not sel: continue
    m = lambda k: np.mean([rr(R[q['Qid']][k]) for q in sel])
    out.append(f"| {b} | {len(sel)} | {m('L2 fts+titles'):.3f} | {m('L3 qwen vec+titles'):.3f} | {m('L3 qwen w1'):.3f} | {m('L3 qwen w2'):.3f} |")
out.append('')

open(f'{W}/RESULTS.md', 'w').write('\n'.join(out) + '\n')
json.dump({'ranks': R, 'systems': systems}, open(f'{W}/results.json', 'w'))
print('\n'.join(out))
