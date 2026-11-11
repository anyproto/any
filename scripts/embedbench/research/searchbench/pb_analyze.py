"""Phase B analysis: public sets through TestCorpusBench, three layers, graded qrels.

python pb_analyze.py <pb dir> [sets]  -> <pb dir>/RESULTS_PB.md (aggregates only)
"""
import json, sys, re, collections, random, os
import numpy as np

PB = sys.argv[1]
SETS = sys.argv[2].split(',') if len(sys.argv) > 2 else ['ru', 'wiki-mix', 'miracl-ru-pool', 'miracl-de-pool', 'scifact', 'nfcorpus', 'fiqa10k']
LIMIT = 10
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]


def rf32(p):
    b = open(p, 'rb').read(); n, d = np.frombuffer(b[:8], '<u4')
    return np.frombuffer(b[8:], '<f4').reshape(int(n), int(d))


def gkey(h): return (h['o'], h['d'], h['r'])
def ckey(h): return (h['o'], h['d'], h['r'], h['c'])


def fuse(titles, fts, vec, wf=1., wv=1., k=60):
    rest = LIMIT - len(titles)
    if rest <= 0: return titles[:LIMIT]
    acc = {}
    for lst, w in ((fts, wf), (vec, wv)):
        if w <= 0: continue
        for rank, h in enumerate(lst):
            kk = ckey(h)
            if kk not in acc: acc[kk] = [h, 0.]
            acc[kk][1] += w / (k + rank + 1)
    fused = sorted(acc.values(), key=lambda a: (-a[1], a[0]['o'], a[0]['d'], a[0]['r'], a[0]['c']))
    best = {}
    for h, s in fused:
        g = gkey(h)
        if g not in best or s > best[g][1]: best[g] = (h, s)
    gs = sorted(best.values(), key=lambda a: (-a[1], a[0]['o'], a[0]['d'], a[0]['r']))
    return titles + [h for h, _ in gs[:rest]]


def blend(titles, fts, vec, alpha):
    rest = LIMIT - len(titles)
    if rest <= 0: return titles[:LIMIT]
    def mm(lst):
        if not lst: return {}
        s = [h['s'] for h in lst]; lo, hi = min(s), max(s)
        return {ckey(h): ((h['s'] - lo) / (hi - lo) if hi > lo else 1.) for h in lst}
    F, V = mm(fts), mm(vec); H = {ckey(h): h for h in fts + vec}
    best = {}
    for s, k in sorted((((1 - alpha) * F.get(k, 0) + alpha * V.get(k, 0)), k) for k in H):
        pass
    for s, k in sorted((((1 - alpha) * F.get(k, 0) + alpha * V.get(k, 0)), k) for k in H)[::-1]:
        if k[:3] not in best: best[k[:3]] = H[k]
    return titles + list(best.values())[:rest]


def metrics(page, rel):
    gains = [2 ** rel.get(h['r'], 0) - 1 if h['d'] != 'prop' else 0 for h in page[:LIMIT]]
    disc = [1 / np.log2(i + 2) for i in range(LIMIT)]
    ideal = sorted([2 ** g - 1 for g in rel.values() if g > 0], reverse=True)[:LIMIT]
    nd = sum(g * d for g, d in zip(gains, disc)) / sum(g * d for g, d in zip(ideal, disc)) if ideal else 0.
    mrr = next((1 / (i + 1) for i, g in enumerate(gains) if g > 0), 0.)
    return nd, mrr


def boot(a, b, n=2000, seed=391):
    d = np.array(a) - np.array(b); rnd = np.random.default_rng(seed)
    bs = np.sort(d[rnd.integers(0, len(d), (n, len(d)))].mean(1))
    return d.mean(), bs[int(.025 * n)], bs[int(.975 * n) - 1]


f = lambda d: f"{d[0]:+.3f}{'*' if d[1] > 0 or d[2] < 0 else ''}"
rows_all = []
out = ['# SearchBench phase B: public and cleared sets through the production FTS / title / fusion code', '',
       'Each set loads into an in-memory production store (TestCorpusBench) with precomputed vectors. EG2 = stock EG2 bank + head projection; Qwen3 = 1024-dim bank.',
       'nDCG@10 on graded qrels, record level (a title hit never counts). Δ against Qwen3 w1 / own w1; * = 95% bootstrap CI over queries excludes 0.', '']
for s in SETS:
    d = f'{PB}/{s}'
    if not all(os.path.exists(f'{d}/{e}.sweep.jsonl') for e in ['qwen', 'eg2']):
        out.append(f'_{s}: runs missing_'); continue
    corpus = [json.loads(l) for l in open(f'{d}/corpus.jsonl')]
    TXT = {(c['o'], c['d'], c['r'], c['c']): c['text'] for c in corpus}
    qs = [json.loads(l) for l in open(f'{d}/queries.jsonl')]; QI = {q['Qid']: i for i, q in enumerate(qs)}
    rels = json.load(open(f'{d}/qrels.json'))
    sw = {e: {json.loads(l)['qid']: json.loads(l) for l in open(f'{d}/{e}.sweep.jsonl')} for e in ['qwen', 'eg2']}
    vec = {e: (rf32(f'{d}/{e}.docs.f32'), rf32(f'{d}/{e}.q.f32')) for e in ['qwen', 'eg2']}
    for q in qs:
        qid = q['Qid']; rel = rels[qid]; m = {}
        for e in ['qwen', 'eg2']:
            r = sw[e][qid]
            Dv, Qv = vec[e]; sc = Dv @ Qv[QI[qid]]; top = np.argsort(-sc)[:LIMIT * 3]
            l1, seen = [], set()
            for i in top:
                c = corpus[i]; g = (c['o'], c['d'], c['r'])
                if g in seen: continue
                seen.add(g); l1.append(dict(o=c['o'], d=c['d'], r=c['r'], c=c['c']))
            m[f'L1 {e}'] = metrics(l1, rel)
            for w in ['0.5', '1', '2']:
                m[f'{e} w{w}'] = metrics(r['pages'][w]['hits'], rel)
            qt = set(tok(q['Query']))
            cov = max((len(qt & set(tok(TXT.get(ckey(h), '')))) / len(qt) for h in r['fts'][:3]), default=0.) if qt else 0.
            m[f'{e} cov'] = metrics(fuse(r['titles'], r['fts'], r['vec'], 1, .5 if cov >= 1 else 1), rel)
            m[f'{e} shape'] = metrics(fuse(r['titles'], r['fts'], r['vec'], 1, 1 if q['shape'] in ('1 word', 'id-like') else 2), rel)
            m[f'{e} blend.4'] = metrics(blend(r['titles'], r['fts'], r['vec'], .4), rel)
            m[f'{e} vec+titles'] = metrics(fuse(r['titles'], [], r['vec'], 0, 1), rel)
        m['L2 fts'] = metrics(fuse(sw['qwen'][qid]['titles'], sw['qwen'][qid]['fts'], [], 1, 0), rel)
        m['oracle legs'] = tuple(max(a, b) for a, b in zip(m['L2 fts'], m['qwen vec+titles']))
        m['oracle rules'] = tuple(max(m[k][i] for k in m if k.startswith('qwen ')) for i in range(2))
        rows_all.append(dict(set=s, lang=q.get('lang', ''), kind=q.get('kind', ''), shape=q['shape'], m=m))


def block(title, sel, key):
    cols = ['L1 qwen', 'L1 eg2', 'L2 fts', 'qwen w1', 'qwen w0.5', 'qwen w2', 'qwen cov', 'qwen shape', 'qwen blend.4', 'eg2 w1', 'eg2 w2', 'oracle legs', 'oracle rules']
    lines = [f'## {title} (nDCG@10)', '', '| ' + key + ' (n) | ' + ' | '.join(cols) + ' |', '|' + '---|' * (len(cols) + 1)]
    groups = collections.defaultdict(list)
    for r in sel: groups[r[key] if key != 'all' else 'all'].append(r)
    for g, rs in sorted(groups.items()):
        cells = []
        for c in cols:
            v = [r['m'][c][0] for r in rs]
            if c in ('L1 qwen', 'qwen w1', 'oracle legs', 'oracle rules', 'L2 fts'):
                cells.append(f'{np.mean(v):.3f}')
            else:
                base = 'L1 qwen' if c.startswith('L1') else ('eg2 w1' if c == 'eg2 w2' else 'qwen w1')
                cells.append(f"{np.mean(v):.3f} ({f(boot(v, [r['m'][base][0] for r in rs]))})")
        lines.append(f'| {g} ({len(rs)}) | ' + ' | '.join(cells) + ' |')
    return lines + ['']


out += ['Δ in parentheses: L1 EG2 against L1 Qwen3; fusion rules against Qwen3 w1; eg2 w2 against eg2 w1. cov = top-3 FTS hits contain every query word → w0.5, else w1.', '']
out += block('Per set', rows_all, 'set')
out += block('Per language', rows_all, 'lang')
out += block('Per query shape, all sets pooled', rows_all, 'shape')
out += block('Per query kind (sets that tag it)', [r for r in rows_all if r['kind']], 'kind')
open(f'{PB}/RESULTS_PB.md', 'w').write('\n'.join(out) + '\n')
print('\n'.join(out))
