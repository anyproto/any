import sys, json, hashlib
sys.argv = ['m0.py', sys.argv[1]]
exec(open('m0.py').read().split("SHAPES = [")[0])
qs = [q for q in Q if q['Qid'] in D]
NEED = {json.loads(l)['tid']: json.loads(l)['need'] for l in open(f'{W}/needs_all.jsonl')}
TXT = {}
for sp, ds in docs.items():
    for d_ in ds: TXT[(d_['o'], d_['d'], d_['r'], d_['c'])] = d_['text']
def text_of(h):
    t = TXT.get((h['o'], h['d'], h['r'], h['c']))
    return t if t else h.get('t', '')
def is_target(h, tid):
    t = T[tid]
    return h['o'] == t['objectId'] if t['kind'] == 'editor_blocks' else (h['o'], h['d'], h['r']) == (t['objectId'], t['dataset'], t['recordId'])
pairs = {}; per_q = {}
for q in qs:
    r = D[q['Qid']]
    P = {}
    for e in ['qwen', 'eg2']:
        vl = vec_leg(e, r)
        P[f'{e} w1'] = fuse(r['titles'], r['fts'], vl, 1, 1)
        P[f'{e} w2'] = fuse(r['titles'], r['fts'], vl, 1, 2)
        P[f'{e} w0.5'] = fuse(r['titles'], r['fts'], vl, 1, .5)
        P[f'L1 {e}'] = group(exact_sorted(e, r['space'], q['Qid']), LIMIT)
    tops = {k: (p[0] if p else None) for k, p in P.items()}
    keys = {gkey(h) for h in tops.values() if h}
    if len(keys) < 2: continue
    per_q[q['Qid']] = {k: (list(gkey(h)) if h else None) for k, h in tops.items()}
    for k, h in tops.items():
        if h is None or is_target(h, q['tid']): continue
        pid = hashlib.sha1(f"{q['Qid']}|{gkey(h)}".encode()).hexdigest()[:12]
        pairs[pid] = dict(pid=pid, qid=q['Qid'], query=q['Query'], need=NEED[q['tid']], rec=list(gkey(h)),
                          text=(text_of(h) or h.get('t') or '')[:1200])
json.dump(per_q, open(f'{W}/judge_tops.json', 'w'))
with open(f'{W}/judge_pairs.jsonl', 'w') as o:
    for p in pairs.values(): o.write(json.dumps(p, ensure_ascii=False) + '\n')
print('queries with rank-1 disagreement', len(per_q), 'unique non-target pairs', len(pairs))
