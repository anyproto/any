import json, sys, collections, random
from common import *
tids=sorted({r['qid'].split('-')[0] for r in rows})
def rr(page,r):
    x=target_rank(page, r['qid'].split('-')[0]); return 1/x if x else 0
base={r['qid']:rr(r['pages']['1']['hits'],r) for r in rows}
alphas=[0.2,0.3,0.35,0.4,0.45,0.5,0.6]
cache={(a,r['qid']):rr(blend_page(r,a),r) for a in alphas for r in rows}
# 2-fold CV over targets, repeated 50 random splits: pick alpha on one half (all shapes), score the other
rnd=random.Random(391); deltas=collections.defaultdict(list); picks=collections.Counter()
for rep in range(50):
    t=tids[:]; rnd.shuffle(t); halves=[set(t[:len(t)//2]), set(t[len(t)//2:])]
    for i in (0,1):
        tr=[r for r in rows if r['qid'].split('-')[0] in halves[i]]; te=[r for r in rows if r['qid'].split('-')[0] in halves[1-i]]
        a=max(alphas, key=lambda a: sum(cache[(a,r['qid'])] for r in tr)); picks[a]+=1
        for b in ['1 word','2-3 words','question','all']:
            sel=[r for r in te if b=='all' or bucket(r['qid'])==b]
            deltas[b].append(sum(cache[(a,r['qid'])]-base[r['qid']] for r in sel)/len(sel))
print('alpha picked on the training half (100 folds):', dict(picks))
for b in ['1 word','2-3 words','question','all']:
    d=sorted(deltas[b]); print(f'CV Δ MRR vs production w1, {b}: mean {sum(d)/len(d):+.3f}, 5-95% of folds [{d[5]:+.3f}, {d[94]:+.3f}]')
# fixed alpha 0.4 with paired bootstrap (in-sample)
for b in ['1 word','2-3 words','question','all']:
    sel=[r for r in rows if b=='all' or bucket(r['qid'])==b]
    dd,lo,hi=boot([cache[(0.4,r['qid'])] for r in sel],[base[r['qid']] for r in sel])
    print(f'alpha 0.4 in-sample, {b}: {dd:+.3f} [{lo:+.3f}, {hi:+.3f}]')
