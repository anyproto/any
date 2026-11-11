import sys, json, re, collections, numpy as np
W = sys.argv[1]
src = open(f'{W}/m0.py').read().split("SHAPES = [")[0]
res = collections.defaultdict(list)
for sub in ['', '/m0b']:
    g = {}; sys.argv = ['m0.py', W + sub]; exec(src, g)
    n = lambda v: v / np.linalg.norm(v, axis=-1, keepdims=True)
    for sp in g['docs']: g['vecs']['q768'] = g['vecs'].get('q768', {}); g['vecs']['q768'][sp] = n(g['vecs']['qwen'][sp][:, :768])
    g['qv']['q768'] = n(g['qv']['qwen'][:, :768])
    for q in [q for q in g['Q'] if q['Qid'] in g['D']]:
        r = g['D'][q['Qid']]; row = dict(shape=q['shape'], tid=q['tid'])
        for e in ['qwen', 'q768']:
            row[f'L1 {e}'] = g['rr'](g['rank_of'](g['group'](g['exact_sorted'](e, r['space'], q['Qid']), 10), q['tid']))
            vl = g['vec_leg'](e, r)
            for w in [1, 2]: row[f'{e} w{w}'] = g['rr'](g['rank_of'](g['fuse'](r['titles'], r['fts'], vl, 1, w), q['tid']))
        res['rows'].append(row)
rows = res['rows']
def boot(a, b, t, n=2000):
    d = np.array(a) - np.array(b); gr = collections.defaultdict(list)
    for i, x in enumerate(t): gr[x].append(i)
    keys = list(gr); rnd = np.random.default_rng(391); bs = []
    for _ in range(n):
        idx = [i for k in rnd.integers(0, len(keys), len(keys)) for i in gr[keys[k]]]; bs.append(d[idx].mean())
    bs = np.sort(bs); return d.mean(), bs[50], bs[1949]
f = lambda t: f"{t[0]:+.3f}{'*' if t[1] > 0 or t[2] < 0 else ''} [{t[1]:+.3f}, {t[2]:+.3f}]"
print('| shape (n) | L1 1024 | L1 768 − 1024 | w1 1024 | w1 768 − 1024 | w2 768 − 1024 |'); print('|---|---|---|---|---|---|')
for sh in ['1 word', 'id-like', 'name', 'typo', 'prefix', '2-3 words', '4-5 words', 'question', 'paste', 'all']:
    s = [r for r in rows if sh == 'all' or r['shape'] == sh]; t = [r['tid'] for r in s]
    v = lambda k: [r[k] for r in s]
    print(f"| {sh} ({len(s)}) | {np.mean(v('L1 qwen')):.3f} | {f(boot(v('L1 q768'), v('L1 qwen'), t))} | {np.mean(v('qwen w1')):.3f} | {f(boot(v('q768 w1'), v('qwen w1'), t))} | {f(boot(v('q768 w2'), v('qwen w2'), t))} |")
