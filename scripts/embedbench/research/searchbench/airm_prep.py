"""Air Crash big-space known-item set (AirM): pick 100 target passages from distinct articles, build code shapes, write
agent input. python airm_prep.py <searchbench dir>"""
import json, sys, re, random, collections
W = sys.argv[1]; A = f'{W}/air'; M = f'{W}/airm'
docs = [json.loads(l) for l in open(f'{A}/docs.jsonl')]
names = json.load(open(f'{M}/names.json'))
rigrec = {tuple(k.split('|')) for rr in json.load(open(f'{A}/qrels.json')).values() for k in rr}
rigobj = {k[0] for k in rigrec}
clean = lambda t: re.sub(r'\s+', ' ', re.sub(r'\[([^\]]*)\]\([^)]*\)', r'\1', re.sub(r'<!--.*?-->', ' ', t, flags=re.S))).strip()
def prose(t):
    if t.startswith(('Image credits', '<!--', '|', '!')) or 'any:block' in t or 'wikimedia' in t: return False
    w = t.split(); return 50 <= len(w) <= 200 and sum(c.isalpha() for c in t) / max(1, len(t)) > .7
REG = re.compile(r'\b(?:N\d{2,5}[A-Z]{0,2}|[A-Z]{1,2}-[A-Z]{3,4}|[A-Z]{2}-\d{3,5})\b')
regcnt = collections.Counter(m for d in docs if d['d'] == 'editor_blocks' for m in set(REG.findall(d['text'])))
cands = collections.defaultdict(list)
for d in docs:
    if d['d'] == 'editor_blocks' and d['c'] == 0 and d['o'] not in rigobj and names.get(d['o']) and prose(clean(d['text'])):
        cands[d['o']].append(d)
rnd = random.Random(391); objs = sorted(cands); rnd.shuffle(objs)
# half the targets carry a rare registration token (id-like shape), half do not
withid = [o for o in objs if any(regcnt[m] < 5 for c in cands[o] for m in REG.findall(c['text']))]
noid = [o for o in objs if o not in set(withid)]
pick = withid[:40] + noid[:60]
T = []
for i, o in enumerate(pick):
    blocks = cands[o]
    if o in set(withid[:40]): blocks = [c for c in blocks if any(regcnt[m] < 5 for m in REG.findall(c['text']))]
    b = rnd.choice(blocks); txt = clean(b['text'])
    ids = sorted({m for m in REG.findall(b['text']) if regcnt[m] < 5})
    T.append(dict(tid=f'a{i:03d}', o=o, d=b['d'], r=b['r'], name=names[o], text=txt, ids=ids, split='tune' if i % 2 == 0 else 'heldout'))
json.dump(T, open(f'{M}/targets.json', 'w'), ensure_ascii=False)
json.dump([dict(tid=t['tid'], article=t['name'], passage=t['text']) for t in T], open(f'{M}/agent_input.json', 'w'), ensure_ascii=False)
print(len(T), 'targets', sum(bool(t['ids']) for t in T), 'with ids; articles in pool', len(objs))
