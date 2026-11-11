"""Query-side typo correction against the index vocabulary (no index change): every query word missing from the
space's vocabulary is replaced by the most frequent vocabulary word at edit distance 1 (2 for words of 8+ chars).
Writes rewrites of the M0 and AirM typo queries for the production sweep. python spell_prep.py <searchbench dir>"""
import json, sys, re, collections, glob
W = sys.argv[1]
def vocab(paths):
    c = collections.Counter()
    for p in paths:
        for l in open(p):
            d = json.loads(l); c.update(set(re.findall(r'\w+', d.get('text', '').lower())))
    return c
L = 'abcdefghijklmnopqrstuvwxyz0123456789'
def e1(w):
    s = [(w[:i], w[i:]) for i in range(len(w) + 1)]
    return set([a + b[1:] for a, b in s if b] + [a + b[1] + b[0] + b[2:] for a, b in s if len(b) > 1] +
               [a + c + b[1:] for a, b in s if b for c in L] + [a + c + b for a, b in s for c in L])
def correct(w, V):
    lw = w.lower()
    if lw in V or len(lw) < 4 or not lw.isalpha(): return w
    c = [x for x in e1(lw) if x in V]
    if not c and len(lw) >= 8: c = [y for x in e1(lw) for y in e1(x) if y in V]
    return max(c, key=lambda x: (V[x], x)) if c else w
spaces = {'bafyreia756bc7zleehqtrlusiluqayvwobzhj5qzvuy4gbfkfzbezm6wsy.2lhdbvetdrq52': [f'{W}/docs-bafyreia756b.jsonl'],
          'bafyreicqlpctaey5b6qzgr3lzohftdqln63payz3zrg4onar3ozjzilki4.n2uaqee5lbvr': [f'{W}/docs-bafyreicqlpc.jsonl'],
          'bafyreiam536orsyz3b2fwl7tvlac7lsi6ocdjeb6opfr2vufzhfbuvhc4i.2lhdbvetdrq52': [f'{W}/air/docs.jsonl']}
V = {s: vocab(p) for s, p in spaces.items()}
out = []; changed = fixed = 0
for src, tag in [(f'{W}/queries.jsonl', 'm0'), (f'{W}/airm/queries.jsonl', 'airm')]:
    for l in open(src):
        q = json.loads(l)
        if q['shape'] != 'typo' or q.get('rewrite') or q['Qid'].endswith('~pfx'): continue
        new = ' '.join(correct(w, V[q['Space']]) for w in re.findall(r'\S+', q['Query']))
        changed += new != q['Query']
        out.append(dict(q, Qid=q['Qid'] + '~spell', Query=new, base=q['Qid'], set=tag))
with open(f'{W}/spell/queries.jsonl', 'w') as o:
    for q in out: o.write(json.dumps(q, ensure_ascii=False) + '\n')
print(len(out), 'typo queries,', changed, 'changed by correction')
for q in out[:6] + out[-6:]: print(' ', q['set'], repr(q['Query']))
