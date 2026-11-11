"""M1: graded pool on Roma's snapshot. Stratified sample of M0 queries (main set + prefix/paste), pool = top-10 of
qwen w1 / w2 / F2 two-branch / F2 lo0.5 + top-5 of L1 qwen and FTS only. Known-item targets grade 3 without judging;
pairs graded in the first judge run are reused (same pid rule). python m1_pairs.py <searchbench dir> [per_shape]"""
import sys, json, hashlib, random, re, collections
W = sys.argv[1]; PER = int(sys.argv[2]) if len(sys.argv) > 2 else 14
STOP = set('a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your'.split())
tok = lambda s: [w for w in re.findall(r'\w+', s.lower()) if w not in STOP]
NEED = {json.loads(l)['tid']: json.loads(l)['need'] for l in open(f'{W}/needs_all.jsonl')}
old = json.load(open(f'{W}/judge_grades.json'))
rows = []  # (sub, q, r, vl_qwen, txt)
for sub in ['', '/m0b']:
    g = {}; sys.argv = ['m0.py', W + sub]; exec(open(f'{W}/m0.py').read().split("SHAPES = [")[0], g)
    TXT = {}
    for sp, ds in g['docs'].items():
        for d_ in ds: TXT[(d_['o'], d_['d'], d_['r'], d_['c'])] = d_['text']
    for q in [q for q in g['Q'] if q['Qid'] in g['D']]:
        rows.append((sub, q, g['D'][q['Qid']], g, TXT))
rnd = random.Random(391); by = collections.defaultdict(list)
for x in rows: by[x[1]['shape']].append(x)
sample = []
for sh in sorted(by):
    xs = by[sh][:]; rnd.shuffle(xs); sample += xs[:PER]
pairs, pool, auto = {}, {}, 0
for sub, q, r, g, TXT in sample:
    vl = g['vec_leg']('qwen', r); qt = set(tok(q['Query']))
    text_of = lambda h: TXT.get((h['o'], h['d'], h['r'], h['c'])) or h.get('t', '')
    c = max((len(qt & set(tok(text_of(h)))) / len(qt) for h in r['fts'][:3]), default=0.) if qt else 0.
    fuse, blend = g['fuse'], g['blend']
    P = {'w1': fuse(r['titles'], r['fts'], vl, 1, 1), 'w2': fuse(r['titles'], r['fts'], vl, 1, 2),
         'F2 two-branch': blend(r['titles'], r['fts'], vl, .3) if c >= 1 else fuse(r['titles'], r['fts'], vl, 1, 2),
         'F2 lo0.5': blend(r['titles'], r['fts'], vl, .3) if c >= 1 else (fuse(r['titles'], [], vl, 0, 1) if c < .5 else fuse(r['titles'], r['fts'], vl, 1, 2)),
         'L1 qwen': g['group'](g['exact_sorted']('qwen', r['space'], q['Qid']), 10), 'L2 fts': fuse(r['titles'], r['fts'], [], 1, 0)}
    depth = {'L1 qwen': 5, 'L2 fts': 5}
    t = g['T'][q['tid']]
    is_t = lambda h: h['o'] == t['objectId'] if t['kind'] == 'editor_blocks' else (h['o'], h['d'], h['r']) == (t['objectId'], t['dataset'], t['recordId'])
    recs = {}
    for k, p in P.items():
        for h in p[:depth.get(k, 10)]:
            recs.setdefault(g['gkey'](h), h)
    pool[q['Qid']] = dict(sub=sub, shape=q['shape'], tid=q['tid'], split=q['split'], cov=c, recs=[list(k) for k in recs],
                          target=[list(k) for k, h in recs.items() if is_t(h)], page_target=t['kind'] == 'editor_blocks')
    exact = lambda h: t['kind'] != 'editor_blocks' and is_t(h)
    for k, h in recs.items():
        if exact(h): auto += 1; continue
        pid = hashlib.sha1(f"{q['Qid']}|{k}".encode()).hexdigest()[:12]
        if pid in old: continue
        pairs[pid] = dict(pid=pid, query=q['Query'], need=NEED[q['tid']], record=(text_of(h) or '')[:1200])
json.dump(pool, open(f'{W}/m1_pool.json', 'w'))
P_ = list(pairs.values()); rnd.shuffle(P_)
BS = 150; nb = (len(P_) + BS - 1) // BS
for i in range(nb):
    with open(f'{W}/m1_batch_{i:02d}.jsonl', 'w') as o:
        for p in P_[i * BS:(i + 1) * BS]: o.write(json.dumps(p, ensure_ascii=False) + '\n')
json.dump([f'{W}/m1_batch_{i:02d}.jsonl' for i in range(nb)], open(f'{W}/m1_batches.json', 'w'))
tot = sum(len(v['recs']) for v in pool.values())
print(f'{len(sample)} queries, pool {tot} records ({tot / len(sample):.1f}/query), targets auto {auto}, reused {tot - auto - len(pairs)}, new pairs {len(pairs)} in {nb} batches')
print(dict(collections.Counter(v['shape'] for v in pool.values())))
