"""Build the AirM query set from targets.json + the Haiku items (items.json). Code shapes: name, name-full, id-like,
typo, prefix, paste; agent shapes: 1 word, 2-3 words, 4-5 words, question. Rewrites (~pfx) ride along for the FTS
prefix test. python airm_build.py <searchbench dir>"""
import json, sys, random, re, hashlib
W = sys.argv[1]; M = f'{W}/airm'
AIR = 'bafyreiam536orsyz3b2fwl7tvlac7lsi6ocdjeb6opfr2vufzhfbuvhc4i.2lhdbvetdrq52'
T = {t['tid']: t for t in json.load(open(f'{M}/targets.json'))}
items = {i['tid']: i for i in json.load(open(f'{M}/items.json'))}
def typo(w, rnd):
    if len(w) < 5: return None
    i = rnd.randrange(1, len(w) - 2); k = rnd.choice(['swap', 'drop', 'sub'])
    if k == 'swap': return w[:i] + w[i + 1] + w[i] + w[i + 2:]
    if k == 'drop': return w[:i] + w[i + 1:]
    c = rnd.choice('aeiourstnl'.replace(w[i].lower(), '')); return w[:i] + c + w[i + 1:]
Q = []
def add(t, shape, q, gen, base=None):
    q = q.strip()
    if not q: return
    qid = f"{t['tid']}-{shape.replace(' ', '')}" + (f'~{base}' if base else '')
    Q.append(dict(Qid=qid, Space=AIR, Query=q, tid=t['tid'], split=t['split'], gen=gen, shape=shape, **({'rewrite': base} if base else {})))
for tid, t in T.items():
    rnd = random.Random(int(hashlib.sha256(tid.encode()).hexdigest()[:8], 16))
    it = items.get(tid)
    nw = t['name'].split()
    add(t, 'name', ' '.join(nw[:2]), 'code-name')
    if len(nw) > 2: add(t, 'name-full', t['name'], 'code-name')
    if t['ids']: add(t, 'id-like', t['ids'][0], 'code-id')
    w = t['text'].split(); s = max(0, len(w) // 2 - 5); add(t, 'paste', ' '.join(w[s:s + 10]), 'code-paste')
    if not it: continue
    ow = it['one_word'].strip().split()[0] if it['one_word'].strip() else ''
    add(t, '1 word', ow, 'haiku'); add(t, '2-3 words', it['q23'], 'haiku'); add(t, '4-5 words', it['q45'], 'haiku'); add(t, 'question', it['question'], 'haiku')
    ty = typo(ow, rnd)
    if ty and ty.lower() != ow.lower():
        add(t, 'typo', ty, 'code-typo'); add(t, 'typo', ty[:4] + '*', 'code-typo', 'pfx')
    q23 = it['q23'].split()
    if q23 and len(q23[-1]) >= 5:
        cut = q23[:-1] + [q23[-1][:max(3, len(q23[-1]) // 2)]]
        add(t, 'prefix', ' '.join(cut), 'code-prefix'); add(t, 'prefix', ' '.join(cut) + '*', 'code-prefix', 'pfx')
with open(f'{M}/queries.jsonl', 'w') as o:
    for q in Q: o.write(json.dumps(q, ensure_ascii=False) + '\n')
with open(f'{M}/qtexts.jsonl', 'w') as o:
    for q in Q: o.write(json.dumps({'text': q['Query']}, ensure_ascii=False) + '\n')
import collections; print(len(Q), 'queries', dict(collections.Counter(q['shape'] + ('~' + q['rewrite'] if q.get('rewrite') else '') for q in Q)))
