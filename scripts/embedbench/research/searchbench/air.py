"""Air Crash Database (Roma's snapshot, 61.6k embedded chunks) with the rig's cleared queries: 51 known-item (kw / para /
cross) + 6 topical with graded judgments. Layers L1 / L2 / L3 and router C. Aggregates only -> AIR.md
"""
import json, sys, re, collections
import numpy as np

W = sys.argv[1]; A = f'{W}/air'; LIMIT = 10
src = open(f'{W}/pb_analyze.py').read()
exec(src[src.index('def rf32'):src.index('rows_all = []')])
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
u = np.load('/Users/roman/anytype/any-wt-syn391/scripts/embedbench/research/eg2_head_patch/head_mean.npy').astype(np.float32)
norm = lambda v: v / np.linalg.norm(v, axis=-1, keepdims=True)

docs = [json.loads(l) for l in open(f'{A}/docs.jsonl')]
TXT = {(d['o'], d['d'], d['r'], d['c']): d['text'] for d in docs}
V = {'qwen': norm(np.load(f'{A}/qwen-docs.npy').astype(np.float32))}
e = norm(rf32(f'{A}/eg2-docs.docs.f32').astype(np.float32)); V['eg2'] = norm(e - np.outer(e @ u, u))
qs = [json.loads(l) for l in open(f'{A}/queries.jsonl')]
QV = {'qwen': norm(rf32(f'{A}/qwen-q.docs.f32').astype(np.float32))}
qe = norm(rf32(f'{A}/eg2-q.docs.f32').astype(np.float32)); QV['eg2'] = norm(qe - np.outer(qe @ u, u))
D = {json.loads(l)['qid']: json.loads(l) for l in open(f'{A}/sweep.jsonl')}
rels = json.load(open(f'{A}/qrels.json'))


def recrel(qid):
    out = {}
    for k, g in rels[qid].items():
        o, d, r = k.split('|'); r = r.split(':')[0]
        out[(o, d, r)] = max(out.get((o, d, r), 0), g)
    return out


def score(page, rel):
    gains = [2 ** rel.get((h['o'], h['d'], h['r']), 0) - 1 for h in page[:LIMIT]]
    disc = [1 / np.log2(i + 2) for i in range(LIMIT)]
    ideal = sorted([2 ** g - 1 for g in rel.values() if g > 0], reverse=True)[:LIMIT]
    nd = sum(g * d for g, d in zip(gains, disc)) / sum(g * d for g, d in zip(ideal, disc)) if ideal else 0.
    mrr = next((1 / (i + 1) for i, g in enumerate(gains) if g > 0), 0.)
    return nd, mrr


def exact_leg(emb, i, r):
    s = V[emb] @ QV[emb][i]; order = np.argsort(-s)[:1000]
    rest = LIMIT - len(r['titles']); fetch = min(max(rest * 3, 30), 100); skip = {gkey(h) for h in r['titles']}
    allh = [dict(o=docs[j]['o'], d=docs[j]['d'], r=docs[j]['r'], c=docs[j]['c'], s=float(s[j])) for j in order if s[j] > 0]
    k = fetch
    while True:
        kept = [h for h in allh[:k] if gkey(h) not in skip]
        if (len(kept) >= fetch and len({gkey(h) for h in kept}) >= rest) or k >= 1000: return kept, allh
        k = min(k * 4, 1000)


def l1page(allh):
    seen, out = set(), []
    for h in allh:
        if gkey(h) in seen: continue
        seen.add(gkey(h)); out.append(h)
        if len(out) == LIMIT: break
    return out


def cov(q, fts):
    qt = set(tok(q))
    if not qt or not fts: return 0.
    return max(len(qt & set(tok(TXT.get(ckey(h), h.get('t', ''))))) / len(qt) for h in fts[:3])


ROUTE_C = lambda c: (1, .5) if c >= 1 else ((0, 1) if c < .5 else (1, 2))
M = collections.defaultdict(dict); ov = []
for i, q in enumerate(qs):
    r = D[q['Qid']]; rel = recrel(q['Qid']); c = cov(q['Query'], r['fts'])
    M[q['Qid']]['L2 fts'] = score(fuse(r['titles'], r['fts'], [], 1, 0), rel)
    for emb in ['qwen', 'eg2']:
        vl, allh = exact_leg(emb, i, r)
        if emb == 'qwen' and r['vec']:
            a = [ckey(h) for h in r['vec']][:10]; b = [ckey(h) for h in vl][:10]; ov.append(len(set(a) & set(b)) / max(1, len(a)))
        M[q['Qid']][f'L1 {emb}'] = score(l1page(allh), rel)
        for w in [0.5, 1, 2]:
            M[q['Qid']][f'{emb} w{w}'] = score(fuse(r['titles'], r['fts'], vl, 1, w), rel)
        wf, wv = ROUTE_C(c); M[q['Qid']][f'{emb} router C'] = score(fuse(r['titles'], r['fts'], vl, wf, wv), rel)
    M[q['Qid']]['_cov'] = c

out = ['# Air Crash Database: the rig\'s cleared queries on Roma\'s snapshot (61,577 embedded chunks)', '',
       f'L0: production Qwen3 vector leg against exact search, top-10 chunk overlap {np.mean(ov):.3f} ({len(ov)} queries).',
       'Known-item: MRR@10 at record level. Topical: graded nDCG@10. Exact vector legs with production window rule; production title block and FTS leg.', '']
cols = ['L1 qwen', 'L1 eg2', 'L2 fts', 'qwen w0.5', 'qwen w1', 'qwen w2', 'qwen router C', 'eg2 w0.5', 'eg2 w1', 'eg2 w2', 'eg2 router C']
out += ['| set / kind (n) | metric | ' + ' | '.join(cols) + ' |', '|' + '---|' * (len(cols) + 2)]
groups = collections.defaultdict(list)
for q in qs:
    groups[(q['set'], q['kind'])].append(q)
    groups[(q['set'], 'all')].append(q)
for (s, k), sel in sorted(groups.items()):
    mi = 1 if s == 'rig-known' else 0
    cells = [f"{np.mean([M[q['Qid']][c][mi] for q in sel]):.3f}" for c in cols]
    out.append(f"| {s} / {k} ({len(sel)}) | {'MRR' if mi else 'nDCG@10'} | " + ' | '.join(cells) + ' |')
cb = collections.Counter('full' if M[q['Qid']]['_cov'] >= 1 else '<0.5' if M[q['Qid']]['_cov'] < .5 else 'partial' for q in qs)
out += ['', f'coverage buckets: {dict(cb)}', '']
open(f'{W}/AIR.md', 'w').write('\n'.join(out) + '\n')
print('\n'.join(out))
