"""Branch mix of the RRF-only tree on Roma's M0 queries with Qwen3 and EG2-head vector legs (the cosine cut's scale)."""
import sys, json, re, collections
import numpy as np
W = sys.argv[1]
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
def br(f):
    if f['cov'] < .8: return 'vector-heavy w2 k20' if f['vgap'] < .1031 else 'vector only'
    return 'lexical-heavy w0.5 k20' if f['fgap'] < .1838 else 'FTS only'
for emb in ['qwen', 'eg2']:
    cnt = collections.Counter(); vg = []; n = 0
    for sub in ['', '/m0b']:
        g = {}; sys.argv = ['m0.py', W + sub]; exec(open(f'{W}/m0.py').read().split("SHAPES = [")[0], g)
        TXT = {}
        for sp, ds in g['docs'].items():
            for d_ in ds: TXT[(d_['o'], d_['d'], d_['r'], d_['c'])] = d_['text']
        for q in [q for q in g['Q'] if q['Qid'] in g['D']]:
            r = g['D'][q['Qid']]; vl = g['vec_leg'](emb, r); qt = set(tok(q['Query']))
            c = max((len(qt & set(tok(TXT.get((h['o'], h['d'], h['r'], h['c'])) or h.get('t', '')))) / len(qt) for h in r['fts'][:3]), default=0.) if qt else 0.
            fs = [h['s'] for h in r['fts']]; vs = [h['s'] for h in vl]
            f = dict(cov=c, vgap=(vs[0] - vs[min(4, len(vs) - 1)]) if vs else 0., fgap=(fs[0] - fs[1]) / fs[0] if len(fs) > 1 and fs[0] > 0 else (1. if fs else 0.))
            cnt[br(f)] += 1; n += 1
            if c < .8: vg.append(f['vgap'])
    print(emb, n, {k: f'{v / n:.0%}' for k, v in cnt.most_common()}, 'vgap quartiles (cov<0.8):', np.round(np.quantile(vg, [.25, .5, .75]), 3))
