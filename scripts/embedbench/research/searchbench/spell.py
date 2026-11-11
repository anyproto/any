"""Typo correction against the index vocabulary: FTS and fusion on the original typo query vs the corrected one.
Production Qwen3 legs from the sweep dumps. M0 (Roma's snapshot, MRR at M0's label rule) and AirM (article MRR).
python spell.py <searchbench dir> -> SPELL.md"""
import json, sys, collections
import numpy as np
W = sys.argv[1]; LIMIT = 10
src = open(f'{W}/pb_analyze.py').read(); exec(src[src.index('def rf32'):src.index('rows_all = []')])
g = {}; sys.argv = ['m0.py', W]; exec(open(f'{W}/m0.py').read().split("SHAPES = [")[0], g)
AT = {t['tid']: t for t in json.load(open(f'{W}/airm/targets.json'))}
AD = {json.loads(l)['qid']: json.loads(l) for l in open(f'{W}/airm/sweep.jsonl')}
SD = {json.loads(l)['qid']: json.loads(l) for l in open(f'{W}/spell/sweep.jsonl')}
SQ = [json.loads(l) for l in open(f'{W}/spell/queries.jsonl')]
def score(q, page):
    if q['set'] == 'm0': return g['rr'](g['rank_of'](page, q['tid']))
    t = AT[q['tid']]; return next((1 / (i + 1) for i, h in enumerate(page[:LIMIT]) if h['o'] == t['o']), 0.)
R = collections.defaultdict(list)
for q in SQ:
    o = (g['D'] if q['set'] == 'm0' else AD).get(q['base']); c = SD.get(q['Qid'])
    if not o or not c: continue
    ch = c['query'] != o['query']
    m = dict(changed=ch,
             fts_o=score(q, fuse(o['titles'], o['fts'], [], 1, 0)), fts_c=score(q, fuse(c['titles'], c['fts'], [], 1, 0)),
             vec_o=score(q, fuse(o['titles'], [], o['vec'], 0, 1)), vec_c=score(q, fuse(c['titles'], [], c['vec'], 0, 1)),
             w1_o=score(q, fuse(o['titles'], o['fts'], o['vec'], 1, 1)), w1_c=score(q, fuse(c['titles'], c['fts'], c['vec'], 1, 1)),
             w1_mix=score(q, fuse(c['titles'], c['fts'], o['vec'], 1, 1)))
    R[q['set']].append(m)
out = ['# Typo correction against the index vocabulary', '',
       'Each query word missing from the space\'s vocabulary is replaced by the most frequent vocabulary word at edit distance 1 (2 for 8+ chars). No index change. Production Qwen3 legs from the sweep. MRR@10 (M0 label rule / AirM article level).', '',
       '| set | n (changed) | FTS orig → corrected | vector only orig → corrected | fused w1 orig → both legs corrected | fused w1, corrected FTS + original vector leg |', '|---|---|---|---|---|---|']
for s, ms in R.items():
    for name, sel in [('all', ms), ('changed only', [m for m in ms if m['changed']])]:
        f = lambda k: np.mean([m[k] for m in sel]) if sel else float('nan')
        out.append(f"| {s} {name} | {len(sel)} ({sum(m['changed'] for m in sel)}) | {f('fts_o'):.3f} → {f('fts_c'):.3f} | {f('vec_o'):.3f} → {f('vec_c'):.3f} | {f('w1_o'):.3f} → {f('w1_c'):.3f} | {f('w1_mix'):.3f} |")
out.append('')
open(f'{W}/SPELL.md', 'w').write('\n'.join(out) + '\n'); print('\n'.join(out))
