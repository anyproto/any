import sys, re
sys.argv = ['m0.py', sys.argv[1]]
src = open('m0.py').read().split("SHAPES = [")[0]
exec(src)
SHAPES = ['1 word', 'id-like', 'name', 'typo', '2-3 words', '4-5 words', 'question']
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
qs = [q for q in Q if q['Qid'] in D]
def cov(q):
    r = D[q['Qid']]; qt = set(tok(q['Query']))
    if not qt or not r['fts']: return 0.
    return max(len(qt & set(tok(h.get('t', '')))) / len(qt) for h in r['fts'][:3])
rules = {
  'w1': lambda q, c: 1, 'w2': lambda q, c: 2,
  'cov: full->0.5 else 2': lambda q, c: 0.5 if c >= 1 else 2,
  'cov: full->1 else 2': lambda q, c: 1 if c >= 1 else 2,
  'cov: full->0.5 else 1': lambda q, c: 0.5 if c >= 1 else 1,
  'cov: >=.5->0.5 else 2': lambda q, c: 0.5 if c >= .5 else 2,
}
for emb in ['qwen', 'eg2']:
    print(f'\n### {emb}: MRR, Δ vs w1 (paired cluster bootstrap)')
    res = {}
    for q in qs:
        r = D[q['Qid']]; vl = vec_leg(emb, r); c = cov(q)
        res[q['Qid']] = {k: rr(rank_of(fuse(r['titles'], r['fts'], vl, 1, f(q, c)), q['tid'])) for k, f in rules.items()}
    for split in ['tune', 'heldout', None]:
        for sh in SHAPES + ['all']:
            sel = [q for q in qs if (sh == 'all' or q['shape'] == sh) and (split is None or q['split'] == split)]
            if sh != 'all' and split is not None: continue
            b = [res[q['Qid']]['w1'] for q in sel]
            cells = [f"w1 {np.mean(b):.3f}"]
            for k in rules:
                if k == 'w1': continue
                v = [res[q['Qid']][k] for q in sel]
                cells.append(f"{k}: {fmt(boot(v, b, [q['tid'] for q in sel]))}")
            print(f"{split or 'both'} | {sh} ({len(sel)}) | " + ' | '.join(cells))
