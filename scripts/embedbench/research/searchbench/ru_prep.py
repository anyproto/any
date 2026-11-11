import json, sys, re, numpy as np
W = sys.argv[1]; S = '/Users/roman/anytype/any-wt-syn391/bench/eg2-ru2/sets'
u = np.load('/Users/roman/anytype/any-wt-syn391/scripts/embedbench/research/eg2_head_patch/head_mean.npy').astype(np.float32)
norm = lambda v: v / np.linalg.norm(v, axis=-1, keepdims=True)
def rf32(p):
    b = open(p, 'rb').read(); n, d = np.frombuffer(b[:8], '<u4'); return np.frombuffer(b[8:], '<f4').reshape(int(n), int(d))
def wf32(p, M):
    M = np.ascontiguousarray(M, dtype='<f4')
    with open(p, 'wb') as f: f.write(np.array(M.shape, dtype='<u4').tobytes()); f.write(M.tobytes())
IDLIKE = re.compile(r'^(?:[A-Z]{2,6}-\d{1,5}|[0-9a-f]{7,12}|\S*\d\S*|[a-z]+_[a-z0-9_]+)$')
def shape(q):
    t = q.split()
    if len(t) == 1: return 'id-like' if IDLIKE.match(q) else '1 word'
    return '2-3 words' if len(t) <= 3 else '4-5 words' if len(t) <= 5 else 'question'
for s in ['sealed', 'new-rig']:
    P = f'{W}/pb/ru-{s}'
    docs = [json.loads(l) for l in open(f'{S}/{s}-docs.jsonl')]; qs = [json.loads(l) for l in open(f'{S}/{s}-queries.jsonl')]
    ids = {d['id'] for d in docs}
    for e in ['eg2', 'qwen']:
        Dv, Qv = norm(rf32(f'{P}/{e}-d.docs.f32')), norm(rf32(f'{P}/{e}-q.docs.f32'))
        if e == 'eg2': Dv, Qv = norm(Dv - np.outer(Dv @ u, u)), norm(Qv - np.outer(Qv @ u, u))
        wf32(f'{P}/{e}.docs.f32', Dv); wf32(f'{P}/{e}.q.f32', Qv)
    with open(f'{P}/corpus.jsonl', 'w') as o:
        for d in docs: o.write(json.dumps(dict(o='chat:' + (d.get('group') or d['id']), d='chat_messages', r=d['id'], c=0, text=d['text'], name=''), ensure_ascii=False) + '\n')
    with open(f'{P}/queries.jsonl', 'w') as o:
        for q in qs: o.write(json.dumps(dict(Qid=q['id'], Query=q['text'], set='ru-' + s, lang='ru' if q.get('kind') != 'english' else 'en', kind=q.get('kind', ''), shape=shape(q['text'])), ensure_ascii=False) + '\n')
    json.dump({q['id']: {q['target']: 1} for q in qs}, open(f'{P}/qrels.json', 'w'))
    print(s, len(docs), 'docs', len(qs), 'queries', 'targets present', sum(q['target'] in ids for q in qs))
