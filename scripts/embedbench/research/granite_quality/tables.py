"""Render the numeric evidence appendix from saved sufficient statistics."""
from common import *
R=json.loads((BASE/'debug/results.json').read_text());G=json.loads((BASE/'debug/geometry.json').read_text());P=json.loads((BASE/'debug/probes.json').read_text());M=json.loads((BASE/'debug/mechanism.json').read_text())
def ci(x):return f"{x['delta']:+.4f} [{x['lo']:+.4f}, {x['hi']:+.4f}]"
lines=['# Numeric evidence','', 'All differences have paired bootstrap 95% CIs. Retrieval uses exact cosine. MRR is truncated at 10. See REPORT.md and the scripts README for scope and bootstrap units.','', '## Baseline retrieval','', '| Set | Metric | Granite | Qwen3 | Granite − Qwen3, 95% CI |','|---|---|---:|---:|---|']
for s in ['fiqa10k','wiki','wiki-mix','wiki-fold','ru']:
    for k in ['ndcg','mrr10','short80','irrelevant_short10','length_slope']:
        lines.append(f"| {s} | {k} | {R[s]['granite'][k]:.4f} | {R[s]['qwen'][k]:.4f} | {ci(R[s]['granite_minus_qwen'][k])} |")
lines+=['','## Language slices','','| Set | Slice | n | Granite nDCG | Qwen nDCG | Difference, 95% CI |','|---|---|---:|---:|---:|---|']
for s in ['wiki','wiki-mix','ru']:
    for l,x in R[s]['slices'].items():lines.append(f"| {s} | {l} | {x['n']} | {x['granite']['ndcg']:.4f} | {x['qwen']['ndcg']:.4f} | {ci(x['difference']['ndcg'])} |")
lines+=['','## Held-out changes to Granite','','All fitted transforms use photosynthesis calibration documents/queries only, except the language offsets fitted on FLORES dev. Evaluation removes calibration articles entirely. FiQA is an untouched transfer test. Rows are exploratory; CIs are pointwise and not multiple-comparison corrected.','','| Variant | Wiki | Wiki + distractors | Russian pairs | FiQA10k |','|---|---|---|---|---|']
for name in R['fixes']['wiki-mix']['variants']:
    lines.append('| '+name+' | '+' | '.join(ci(R['fixes'][s]['variants'][name]['delta']['ndcg']) for s in ['wiki','wiki-mix','ru','fiqa10k'])+' |')
lines+=['','Fit-only selection: `'+R['calibration']['selected_by_fit_ndcg']+'`. Fit counts: '+str(R['calibration']['fit_docs'])+' docs, '+str(R['calibration']['fit_queries'])+' queries.','', '## Folding / adding distractors','','| Intervention | Model | ΔnDCG, 95% CI | Δshort top-10 share, 95% CI |','|---|---|---|---|']
for k in ['folding','distractor_effect']:
    for m,x in R[k].items():lines.append(f"| {k} | {m} | {ci(x['ndcg'])} | {ci(x['short80'])} |")
lines+=['','## Final-vector geometry','','Comparative PCA variance uses fixed axes fitted to FLORES dev. Full corpus PCA values are descriptive and are not these held-out comparisons.','','| Corpus | Statistic | Granite | Qwen | Difference, 95% CI |','|---|---|---:|---:|---|']
for s in ['wiki-mix','fiqa10k']:
    for k in ['mean_norm','top1_cal_pc_variance','top5_cal_pc_variance','top10_cal_pc_variance','k10_skew','top8_coordinate_energy']:
        lines.append(f"| {s} | {k} | {G[s]['models']['granite'][k]:.4f} | {G[s]['models']['qwen'][k]:.4f} | {ci(G[s]['granite_minus_qwen'][k])} |")
lines+=['','Full corpus PCA cumulative variance:','']
for s in ['wiki-mix','fiqa10k']:
    for m,x in G[s]['models'].items():lines.append(f"- {s}, {m}: "+', '.join(f"PC1..{k} = {v:.4f}" for k,v in x['corpus_pca_variance'].items()))
lines+=['','## Aligned-language diagnostics','','| Direction | Granite MRR@10 | Qwen MRR@10 | Difference, 95% CI | Granite offset correction ΔMRR, 95% CI |','|---|---:|---:|---|---|']
for k,x in G['flores']['directions'].items():lines.append(f"| {k} | {x['granite']['base']['mrr10']:.4f} | {x['qwen']['base']['mrr10']:.4f} | {ci(x['granite_minus_qwen']['mrr10'])} | {ci(x['granite']['offset_delta']['mrr10'])} |")
lines+=['','| Language | Granite tokens | Qwen tokens | Difference, 95% CI |','|---|---:|---:|---|']
for l,x in G['flores']['tokenization'].items():lines.append(f"| {l} | {x['granite']['mean_tokens']:.4f} | {x['qwen']['mean_tokens']:.4f} | {ci(x['granite_minus_qwen'])} |")
lines+=['','| Balanced same-topic statistic | Granite | Qwen | Difference, 95% CI |','|---|---:|---:|---|']
x=G['flores']['language_geometry']
for k in ['same_language_neighbor_share','same_language_similarity_advantage']:lines.append(f"| {k} | {x['granite'][k]:.4f} | {x['qwen'][k]:.4f} | {ci(x['granite_minus_qwen'][k])} |")
lines+=['','## Matched heading/body and layer probes','','| Model | Heading minus following body | Difference, 95% CI |','|---|---|---|']
for m in ['granite','qwen']:
    for k,x in M[m]['heading_minus_next_body'].items():lines.append(f'| {m} | {k} | {ci(x)} |')
lines+=['','| Layer | Heading cosine to empty | Body cosine to empty | Paired difference, 95% CI |','|---|---:|---:|---|']
for x in P['layers']:lines.append(f"| {x['layer']} | {x['short_empty_cosine']:.4f} | {x['long_empty_cosine']:.4f} | {ci(x['paired_short_minus_long'])} |")
L=json.loads((BASE/'debug/language_control.json').read_text())
lines+=['','## Language-restricted retrieval control','','This changes the candidate set, not the qrels. It is a diagnostic for language-specific relevance labels, not a rejudgment of foreign-language answers.','','| Set / slice | Granite nDCG | Qwen nDCG | Difference, 95% CI |','|---|---:|---:|---|']
for s in L:
    for l in ['all','de','ru']:
        x=L[s][l];lines.append(f"| {s} / {l} | {x['granite']['monolingual_ndcg']:.4f} | {x['qwen']['monolingual_ndcg']:.4f} | {ci(x['monolingual_granite_minus_qwen']['ndcg'])} |")
lines+=['','## Distractor density','','| Added short docs | Granite short top-10 share | Qwen short top-10 share | Difference, 95% CI |','|---|---:|---:|---|']
for n,x in M['distractor_density'].items():lines.append(f"| {n} | {x['granite']['short80']:.4f} | {x['qwen']['short80']:.4f} | {ci(x['granite_minus_qwen']['short80'])} |")
lines+=['','Held-out Granite folding ΔnDCG: '+ci(R['folding_heldout_granite']['ndcg'])+'.']
(BASE/'debug/TABLES.md').write_text('\n'.join(lines)+'\n')
print('\n'.join(lines[:52]))
log('Generated TABLES.md from JSON evidence; every reported metric difference includes its paired bootstrap interval.')
