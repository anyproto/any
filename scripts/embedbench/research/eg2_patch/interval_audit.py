"""Repair omitted target grouping in two short-positive slice CIs, without rescoring."""
from common import *


def main():
    all_results = json.loads((BASE/'results/all.json').read_text()); audit = {}
    metrics = ['ndcg10', 'mrr10', 'recall10', 'short80', 'short400']
    for name in ['ru', 'rig-known']:
        ds, qs, qr = loadset(name); lengths = {d['id']: len(d['text']) for d in ds}; ii = []
        for i, q in enumerate(qs):
            rel = qr[q['id']]; positive = rel if isinstance(rel, list) else [x for x, v in rel.items() if v > 0]
            if positive and all(lengths[x] < 400 for x in positive): ii.append(i)
        groups = np.array([q['target'] for q in qs])[ii]
        arrays = np.load(BASE/'results'/f'{name}-per-query.npz')
        result = all_results[name]; sub = result['slices']['all_positive_under400']
        assert sub['n'] == len(ii)
        old = sub['comparisons']; new = {}
        for variant in result['scores']:
            new[variant] = {}
            for ref in ['original', 'qwen']:
                if variant == ref: continue
                new[variant][ref] = {}
                for metric in metrics:
                    d = delta(arrays[f'{variant}/{metric}'][ii], arrays[f'{ref}/{metric}'][ii], groups)
                    assert abs(d['delta']-old[variant][ref][metric]['delta']) < 1e-12
                    new[variant][ref][metric] = d
        audit[name] = dict(queries=len(ii), targets=len(set(groups)), old=old, corrected=new)
        sub['comparisons'] = new
        sub['interval_note'] = 'Target-cluster grouping corrected from saved per-query arrays; point estimates/rankings unchanged.'
        save(BASE/'results'/f'{name}.json', result)
    save(BASE/'results/all.json', all_results)
    save(BASE/'results/interval-audit.json', audit)
    log('Post-score statistics audit: corrected missing target grouping in RU and rig-known all-positive-under400 subset CIs from saved per-query arrays. Rankings, points, main-set intervals, candidates and evaluation-pass count are unchanged.')


if __name__ == '__main__':
    main()
