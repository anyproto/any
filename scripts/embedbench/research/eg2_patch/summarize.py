"""Render numeric appendices from saved per-query results, without model selection."""
from common import *

def ci(x,digits=4):return f'{x["delta"]:+.{digits}f} [{x["lo"]:+.{digits}f}, {x["hi"]:+.{digits}f}]'
def main():
    results=json.loads((BASE/'results/all.json').read_text());lines=['# Frozen held-out results','',
    'Exact cosine search; pointwise paired 95% bootstrap intervals (4,000 draws, seed 391). Russian paired queries and rig known-items are grouped by target. Wiki article-cluster intervals and language-restricted sensitivity checks are in the per-set JSON. MIRACL and rig scores use closed pools. All scores below are for the frozen Q8 artifacts; Qwen uses validated float16 arithmetic.','']
    for setname,s in results.items():
        lines += [f'## {setname}', '', f'{s["queries"]} queries; {s["docs"]} passages.','']
        for metric in ['ndcg10','mrr10','recall10','short80','short400']:
            lines += [f'### {metric}','','| Variant | Score | Difference from original EG2, 95% CI | Difference from Qwen, 95% CI |','|---|---:|---|---|']
            for name,score in s['scores'].items():
                comp=s['comparisons'][name]
                lines.append(f'| {name} | {score[metric]:.4f} | '+(ci(comp['original'][metric]) if name!='original' else '—')+' | '+(ci(comp['qwen'][metric]) if name!='qwen' else '—')+' |')
            lines += ['']
        lines += ['Targeted minus norm-matched random control and direct candidate comparisons:','','| Difference | nDCG@10 | MRR@10 | Recall@10 | Under 80 chars | Under 400 chars |','|---|---|---|---|---|---|']
        direct={f'{name}_minus_random':vals for name,vals in s['target_minus_random'].items()}
        direct.update(s['candidate_comparisons'])
        for name,vals in direct.items():lines.append('| '+name+' | '+' | '.join(ci(vals[k]) for k in ['ndcg10','mrr10','recall10','short80','short400'])+' |')
        lines += ['']
    (BASE/'TABLES.md').write_text('\n'.join(lines)+'\n')
    dev=[]
    for p in (BASE/'dev').glob('*.json'):
        r=json.loads(p.read_text())
        if isinstance(r,dict) and 'pass_id' in r and 'metrics' in r:dev.append(r)
    original=read(BASE/'dev/passes.jsonl')[0]
    lines=['# Development results','',
    'Only photosynthesis calibration queries are used here. These intervals are exploratory and conditional on adaptive selection; they are not confirmation evidence. Original nDCG@10 = '+f'{original["metrics"]["ndcg10"]:.5f}.','',
    '| Pass | Variant | nDCG@10 | Difference, paired 95% CI | MRR@10 | Under 80 chars |','|---:|---|---:|---|---:|---:|']
    for r in sorted(dev,key=lambda r:r['pass_id']):
        name=r.get('name',r.get('spec',{}).get('name'));m=r['metrics']
        lines.append(f'| {r["pass_id"]} | {name} | {m["ndcg10"]:.5f} | {ci(r["delta"]["ndcg10"])} | {m["mrr10"]:.5f} | {m["short80"]:.3f} |')
    (BASE/'DEV_TABLES.md').write_text('\n'.join(lines)+'\n')
    lines=['# Report-ready confirmation tables','','| Set | Queries | Head ΔnDCG vs EG2 | Head ΔnDCG vs Qwen | Writer ΔnDCG vs EG2 |','|---|---:|---|---|---|']
    for name,s in results.items():
        c=s['comparisons'];lines.append(f'| {name} | {s["queries"]} | {ci(c["head"]["original"]["ndcg10"])} | {ci(c["head"]["qwen"]["ndcg10"])} | {ci(c["writer"]["original"]["ndcg10"])} |')
    lines += ['','| Set | Head ΔMRR vs EG2 | Writer ΔMRR vs EG2 | Head ΔRecall vs EG2 | Head Δshort80 vs EG2 | Head Δshort400 vs EG2 |','|---|---|---|---|---|---|']
    for name,s in results.items():
        h=s['comparisons']['head']['original'];w=s['comparisons']['writer']['original']
        lines.append(f'| {name} | {ci(h["mrr10"])} | {ci(w["mrr10"])} | {ci(h["recall10"])} | {ci(h["short80"])} | {ci(h["short400"])} |')
    (BASE/'results/REPORT_NUMBERS.md').write_text('\n'.join(lines)+'\n')
    log('Rendered complete held-out and dev metric appendices from frozen per-query arrays; no model changes or extra evaluation passes.')

if __name__=='__main__':main()
