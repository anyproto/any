import json, sys, collections
sys.argv=[sys.argv[0], sys.argv[1], __import__('os').environ.get('SWEEP_WEAK', '')]
exec(open(sys.argv[0].replace('analyze2.py','analyze.py')).read().split("rows = [r for r in rows if r['qid']")[0])
rows=[r for r in rows if r['qid'].split('-')[0] not in weak]
by=collections.defaultdict(list)
for r in rows: by[bucket(r['qid'])].append(r)
by['all']=rows

def composition(r, page):
    f={ckey(h) for h in r['fts']}; v={ckey(h) for h in r['vec']}
    fo={gkey(h) for h in r['fts']}; vo={gkey(h) for h in r['vec']}
    c=collections.Counter()
    for h in page[len(r['titles']):]:
        g=gkey(h); c['both' if g in fo and g in vo else 'fts-only' if g in fo else 'vec-only']+=1
    return c

print('## Finer weight grid (vector weight, FTS 1, k=60): MRR by shape, and content slots held by records found by one leg only')
print('| w | 1 word | 2-3 words | question | all | fts-only slots | vec-only slots |')
print('|---|---|---|---|---|---|---|')
for w in [0.5,0.8,0.9,0.95,1.0,1.02,1.05,1.1,1.2,1.3,1.5,2,3]:
    cells=[]
    for b in ['1 word','2-3 words','question','all']:
        rk=[target_rank(fuse_page(r,1,w), r['qid'].split('-')[0]) for r in by[b]]
        cells.append(f"{metrics(rk)['mrr']:.3f}")
    comp=collections.Counter()
    for r in rows: comp+=composition(r, fuse_page(r,1,w))
    tot=sum(comp.values())
    print(f"| {w} | "+' | '.join(cells)+f" | {comp['fts-only']/tot:.0%} | {comp['vec-only']/tot:.0%} |")

def blend_page(r, alpha):
    titles=r['titles']; rest=LIMIT-len(titles)
    if rest<=0: return titles[:LIMIT]
    def mm(lst):
        if not lst: return {}
        s=[h['s'] for h in lst]; lo,hi=min(s),max(s)
        return {ckey(h):((h['s']-lo)/(hi-lo) if hi>lo else 1.0) for h in lst}
    F,V=mm(r['fts']),mm(r['vec']); H={ckey(h):h for h in r['fts']+r['vec']}
    sc={k:(1-alpha)*F.get(k,0)+alpha*V.get(k,0) for k in H}
    groups={}
    for k,s in sorted(sc.items(), key=lambda a:(-a[1],a[0])):
        g=k[:3]
        if g not in groups: groups[g]=(H[k],s)
    gs=sorted(groups.values(), key=lambda a:(-a[1],a[0]['o'],a[0]['d'],a[0]['r']))
    return titles+[h for h,_ in gs[:rest]]

print('\n## Convex blend of min-max-normalized leg scores (alpha = vector share), same legs and titles; exploratory, not tuned on held-out data')
print('| alpha | 1 word | 2-3 words | question | all |')
print('|---|---|---|---|---|')
for a in [0.2,0.3,0.4,0.5,0.6,0.7,0.8]:
    cells=[]
    for b in ['1 word','2-3 words','question','all']:
        rk=[target_rank(blend_page(r,a), r['qid'].split('-')[0]) for r in by[b]]
        cells.append(f"{metrics(rk)['mrr']:.3f}")
    print(f"| {a} | "+' | '.join(cells)+' |')

# window sizes
print('\nleg window sizes (chunks): fts median', sorted(len(r['fts']) for r in rows)[len(rows)//2], 'vec median', sorted(len(r['vec']) for r in rows)[len(rows)//2],
      '| fts windows < 30 chunks:', sum(1 for r in rows if len(r['fts'])<30), 'of', len(rows))
