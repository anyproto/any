"""Vector-only retrieval: reference EG2 (llama.cpp, stock bank) vs LiteRT int4 EG2 text-270m, each stock and with the
head projection (u = head_mean.npy). nDCG@10, MRR, top-10 short-hit share (<80 chars). Paired bootstrap, seed 391.
python score.py <litert out> <eg2-patch dir> <searchbench pb> set,set,... -> stdout markdown"""
import sys, json
import numpy as np
OUT, B, PB, SETS = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4].split(',')
raw = np.load(f'{B}/vectors/original/raw.npy', mmap_mode='r'); idx = {}
for i, l in enumerate(open(f'{B}/vectors/original/records.jsonl')):
    r = json.loads(l); idx[(r['role'], r['text'])] = i
u = np.load('/Users/roman/anytype/any-wt-syn391/scripts/embedbench/research/eg2_head_patch/head_mean.npy').astype(np.float32)
n = lambda v: v / np.linalg.norm(v, axis=-1, keepdims=True)
hp = lambda V: n(V - np.outer(V @ u, u))
def boot(a, b, N=2000, seed=391):
    d = np.array(a) - np.array(b); rnd = np.random.default_rng(seed)
    bs = np.sort(d[rnd.integers(0, len(d), (N, len(d)))].mean(1)); return d.mean(), bs[int(.025 * N)], bs[int(.975 * N) - 1]
fm = lambda t: f"{t[0]:+.3f}{'*' if t[1] > 0 or t[2] < 0 else ''}"
SYS = ['reference stock', 'reference head', 'LiteRT stock', 'LiteRT head']
allm = {k: [] for k in SYS}; rows = []
for s in SETS:
    corpus = [json.loads(l) for l in open(f'{PB}/{s}/corpus.jsonl')]; qs = [json.loads(l) for l in open(f'{PB}/{s}/queries.jsonl')]
    rels = json.load(open(f'{PB}/{s}/qrels.json'))
    LD, LQ = np.load(f'{OUT}/{s}/doc.npy'), np.load(f'{OUT}/{s}/query.npy')
    if not (len(LD) == len(corpus) and len(LQ) == len(qs)): print(s, 'incomplete'); continue
    RD = n(np.stack([np.asarray(raw[idx[('doc', c['text'])]], np.float32) for c in corpus]))
    RQ = n(np.stack([np.asarray(raw[idx[('query', q['Query'])]], np.float32) for q in qs]))
    V = {'reference stock': (RD, RQ), 'reference head': (hp(RD), hp(RQ)), 'LiteRT stock': (n(LD), n(LQ)), 'LiteRT head': (hp(n(LD)), hp(n(LQ)))}
    short = np.array([len(c['text']) < 80 for c in corpus]); ids = [c['r'] for c in corpus]
    m = {}
    for k, (D, Q) in V.items():
        top = np.argsort(-(Q @ D.T), axis=1)[:, :10]; nd, sh = [], []
        for qi, q in enumerate(qs):
            rel = rels[q['Qid']]; gains = [2 ** rel.get(ids[j], 0) - 1 for j in top[qi]]
            disc = 1 / np.log2(np.arange(2, 12)); ideal = sorted([2 ** g - 1 for g in rel.values() if g > 0], reverse=True)[:10]
            nd.append(sum(g * d for g, d in zip(gains, disc)) / sum(g * d for g, d in zip(ideal, disc)) if ideal else 0.)
            sh.append(short[top[qi]].mean())
        m[k] = (nd, sh); allm[k] += nd
    cosd = (n(LD) * RD).sum(1).mean()
    rows.append(f"| {s} ({len(qs)} q, {len(corpus)} docs) | {np.mean(m['reference stock'][0]):.3f} | {np.mean(m['reference head'][0]):.3f} | {np.mean(m['LiteRT stock'][0]):.3f} | {np.mean(m['LiteRT head'][0]):.3f} | {fm(boot(m['LiteRT head'][0], m['LiteRT stock'][0]))} | {fm(boot(m['LiteRT head'][0], m['reference head'][0]))} | {100*np.mean(m['LiteRT stock'][1]):.1f}% → {100*np.mean(m['LiteRT head'][1]):.1f}% (ref {100*np.mean(m['reference stock'][1]):.1f}% → {100*np.mean(m['reference head'][1]):.1f}%) | {cosd:.4f} |")
print('| set | ref stock | ref head | LiteRT stock | LiteRT head | head effect on LiteRT | LiteRT head − ref head | top-10 short share, LiteRT (ref) | cos LiteRT·ref, docs |')
print('|---|---|---|---|---|---|---|---|---|')
print('\n'.join(rows))
if rows:
    print(f"| all ({len(allm['LiteRT head'])} q) | {np.mean(allm['reference stock']):.3f} | {np.mean(allm['reference head']):.3f} | {np.mean(allm['LiteRT stock']):.3f} | {np.mean(allm['LiteRT head']):.3f} | {fm(boot(allm['LiteRT head'], allm['LiteRT stock']))} | {fm(boot(allm['LiteRT head'], allm['reference head']))} | | |")
