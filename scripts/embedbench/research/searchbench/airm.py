"""AirM: known-item queries in all M0 shapes on the Air Crash space (61.6k chunks, many look-alike crash articles).
Layers L1 (exact cosine, both embedders), L2 (production FTS + title pass), L3 (fusion rules incl. router F2 variants),
FTS prefix rewrites, oracle router bound. Article-level MRR@10 primary (M0's page rule), record-level secondary.
python airm.py <searchbench dir> -> AIRM.md"""
import json, sys, re, collections
import numpy as np
W = sys.argv[1]; A = f'{W}/air'; M_ = f'{W}/airm'; LIMIT = 10
src = open(f'{W}/pb_analyze.py').read(); exec(src[src.index('def rf32'):src.index('rows_all = []')])
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
u = np.load('/Users/roman/anytype/any-wt-syn391/scripts/embedbench/research/eg2_head_patch/head_mean.npy').astype(np.float32)
norm = lambda v: v / np.linalg.norm(v, axis=-1, keepdims=True)
docs = [json.loads(l) for l in open(f'{A}/docs.jsonl')]
TXT = {(d['o'], d['d'], d['r'], d['c']): d['text'] for d in docs}
V = {'qwen': norm(np.load(f'{A}/qwen-docs.npy').astype(np.float32))}
e = norm(rf32(f'{A}/eg2-docs.docs.f32').astype(np.float32)); V['eg2'] = norm(e - np.outer(e @ u, u))
qs = [json.loads(l) for l in open(f'{M_}/queries.jsonl')]
QV = {'qwen': norm(rf32(f'{M_}/qwen-q.docs.f32').astype(np.float32))}
qe = norm(rf32(f'{M_}/eg2-q.docs.f32').astype(np.float32)); QV['eg2'] = norm(qe - np.outer(qe @ u, u))
D = {json.loads(l)['qid']: json.loads(l) for l in open(f'{M_}/sweep.jsonl')}
T = {t['tid']: t for t in json.load(open(f'{M_}/targets.json'))}

def sc(page, tid):
    t = T[tid]; rec = (t['o'], t['d'], t['r'])
    ra = next((1 / (i + 1) for i, h in enumerate(page[:LIMIT]) if h['o'] == t['o']), 0.)
    rr_ = next((1 / (i + 1) for i, h in enumerate(page[:LIMIT]) if (h['o'], h['d'], h['r']) == rec), 0.)
    return ra, rr_

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

def f2(t, f, v, c, lo):
    return blend(t, f, v, .3) if c >= 1 else (fuse(t, [], v, 0, 1) if c < lo else fuse(t, f, v, 1, 2))

R = {}; ov = []
for i, q in enumerate(qs):
    r = D[q['Qid']]; c = cov(q['Query'], r['fts']); m = {'_cov': c}
    m['L2 fts'] = sc(fuse(r['titles'], r['fts'], [], 1, 0), q['tid'])
    m['L2 fts no titles'] = sc(fuse([], r['fts'], [], 1, 0), q['tid'])
    for emb in ['qwen', 'eg2']:
        vl, allh = exact_leg(emb, i, r)
        if emb == 'qwen' and r['vec']:
            a = [ckey(h) for h in r['vec']][:10]; b = [ckey(h) for h in vl][:10]; ov.append(len(set(a) & set(b)) / max(1, len(a)))
        m[f'L1 {emb}'] = sc(l1page(allh), q['tid'])
        m[f'{emb} vec+titles'] = sc(fuse(r['titles'], [], vl, 0, 1), q['tid'])
        for w in [0.5, 1, 2]: m[f'{emb} w{w}'] = sc(fuse(r['titles'], r['fts'], vl, 1, w), q['tid'])
        m[f'{emb} blend.3'] = sc(blend(r['titles'], r['fts'], vl, .3), q['tid'])
        m[f'{emb} router C'] = sc(fuse(r['titles'], r['fts'], vl, 1, .5) if c >= 1 else (fuse(r['titles'], [], vl, 0, 1) if c < .5 else fuse(r['titles'], r['fts'], vl, 1, 2)), q['tid'])
        for lo in [0.5, 0.0]: m[f'{emb} F2 lo{lo}'] = sc(f2(r['titles'], r['fts'], vl, c, lo), q['tid'])
        m[f'{emb} oracle'] = tuple(max(m[k][j] for k in [f'{emb} vec+titles', 'L2 fts', f'{emb} w1', f'{emb} w2', f'{emb} blend.3']) for j in (0, 1))
    if r.get('pages'):
        pg = {str(p.get('w')): p for p in r['pages']} if isinstance(r['pages'], list) else r['pages']
    R[q['Qid']] = m

def bs(a, b, groups, n=2000, seed=391):
    d = np.array(a) - np.array(b); rnd = np.random.default_rng(seed); g = collections.defaultdict(list)
    for i, t in enumerate(groups): g[t].append(i)
    keys = list(g); o = []
    for _ in range(n):
        idx = [i for k in rnd.integers(0, len(keys), len(keys)) for i in g[keys[k]]]; o.append(d[idx].mean())
    o = np.sort(o); return d.mean(), o[int(.025 * n)], o[int(.975 * n) - 1]
fm = lambda t: f"{t[0]:+.3f}{'*' if t[1] > 0 or t[2] < 0 else ''}"

base = [q for q in qs if not q.get('rewrite')]
SH = ['1 word', 'name', 'name-full', 'id-like', 'typo', 'prefix', '2-3 words', '4-5 words', 'question', 'paste']
out = ['# AirM: known-item lookups in every shape on the Air Crash space (61,577 chunks)', '',
       f'{len(T)} target passages from distinct articles (seed 391), {len(base)} queries (+{len(qs) - len(base)} FTS rewrites).',
       'Code shapes: name (first 2 words of the title), name-full, id-like (an aircraft registration found in < 5 records), typo (one edit of the one-word query), prefix (2-3 words, last word cut), paste (10 words from the passage). Haiku shapes: 1 word, 2-3 words, 4-5 words, question.',
       f'L0: production Qwen3 vector leg against exact search, top-10 chunk overlap {np.mean(ov):.3f}.', '',
       '## Article-level MRR@10 by shape (a hit on any block of the right article)', '']
cols = ['L1 qwen', 'L1 eg2', 'L2 fts', 'qwen w0.5', 'qwen w1', 'qwen w2', 'qwen blend.3', 'qwen F2 lo0.5', 'qwen F2 lo0.0', 'qwen oracle', 'eg2 w1', 'eg2 w2', 'eg2 F2 lo0.5', 'eg2 F2 lo0.0', 'eg2 oracle']
for mi, title in [(0, None), (1, '## Record-level MRR@10 by shape (the exact passage)')]:
    if title: out += ['', title, '']
    out += ['| shape (n) | ' + ' | '.join(cols) + ' |', '|' + '---|' * (len(cols) + 1)]
    for sh in SH + ['all']:
        sel = [q for q in base if sh == 'all' or q['shape'] == sh]
        if not sel: continue
        out.append(f'| {sh} ({len(sel)}) | ' + ' | '.join(f"{np.mean([R[q['Qid']][c][mi] for q in sel]):.3f}" for c in cols) + ' |')
out += ['', '## Δ against RRF w1 (article MRR; bootstrap clustered by target; * = 95% CI excludes 0)', '',
        '| variant | ' + ' | '.join(SH) + ' | held-out | all |', '|' + '---|' * (len(SH) + 3)]
for emb in ['qwen', 'eg2']:
    for v in [f'L1 {emb}', 'L2 fts', f'{emb} w0.5', f'{emb} w2', f'{emb} blend.3', f'{emb} router C', f'{emb} F2 lo0.5', f'{emb} F2 lo0.0', f'{emb} oracle']:
        cells = []
        for sh in SH + ['held-out', 'all']:
            sel = [q for q in base if sh == 'all' or (sh == 'held-out' and q['split'] == 'heldout') or q['shape'] == sh]
            if not sel: cells.append('–'); continue
            a = [R[q['Qid']][v][0] for q in sel]; b = [R[q['Qid']][f'{emb} w1'][0] for q in sel]
            cells.append(fm(bs(a, b, [q['tid'] for q in sel])))
        out.append(f'| {v if v.startswith(emb) else v + " (vs " + emb + " w1)"} | ' + ' | '.join(cells) + ' |')
out += ['', '## Title pass and FTS rewrites (article MRR)', '']
for sh in ['name', 'name-full', '1 word']:
    sel = [q for q in base if q['shape'] == sh]
    if sel: out.append(f"- {sh}: FTS with title pass {np.mean([R[q['Qid']]['L2 fts'][0] for q in sel]):.3f}, without {np.mean([R[q['Qid']]['L2 fts no titles'][0] for q in sel]):.3f} (n={len(sel)})")
for sh in ['prefix', 'typo']:
    pairs = [(q, next((x for x in qs if x.get('rewrite') and x['tid'] == q['tid'] and x['shape'] == sh), None)) for q in base if q['shape'] == sh]
    pairs = [(a, b) for a, b in pairs if b]
    if not pairs: continue
    f0 = np.mean([R[a['Qid']]['L2 fts'][0] for a, b in pairs]); f1 = np.mean([R[b['Qid']]['L2 fts'][0] for a, b in pairs])
    w0 = np.mean([R[a['Qid']]['qwen w1'][0] for a, b in pairs]); w1_ = np.mean([R[b['Qid']]['qwen w1'][0] for a, b in pairs])
    v0 = np.mean([R[a['Qid']]['L1 qwen'][0] for a, b in pairs])
    out.append(f"- {sh} + '*' rewrite (n={len(pairs)}): FTS {f0:.3f} → {f1:.3f}; fused w1 {w0:.3f} → {w1_:.3f}; vector only {v0:.3f}")
cb = collections.Counter((q['shape'], 'full' if R[q['Qid']]['_cov'] >= 1 else '<0.5' if R[q['Qid']]['_cov'] < .5 else 'partial') for q in base)
out += ['', '## Coverage buckets by shape', ''] + [f"- {sh}: " + ', '.join(f"{b} {cb[(sh, b)]}" for b in ['full', 'partial', '<0.5']) for sh in SH]
out.append('')
open(f'{W}/AIRM.md', 'w').write('\n'.join(out) + '\n'); print('\n'.join(out))
