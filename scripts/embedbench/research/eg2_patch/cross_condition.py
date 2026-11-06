"""Paired corpus-condition contrasts from saved rankings, with no rescoring."""
from common import *


def fmt(x):
    return f'{x["delta"]:+.4f} [{x["lo"]:+.4f}, {x["hi"]:+.4f}]'


def main():
    _, qs, _ = loadset('wiki')
    groups = [q['lang']+'/'+q['article'] for q in qs]
    original = np.load(BASE/'results/wiki-per-query.npz')
    names = ['original', 'qwen', 'writer', 'writer_random', 'head', 'head_random', 'centered']
    metrics = ['ndcg10', 'mrr10', 'recall10', 'short80', 'short400']
    result = {}; lines = ['# Paired wiki corpus-condition contrasts', '',
        'Computed from existing frozen per-query results; no retrieval pass or parameter selection. Intervals resample source article/language clusters (4,000 draws, seed 391). The same 65 queries appear in all three conditions.', '']
    for condition in ['wiki-mix', 'wiki-fold']:
        _, other_qs, _ = loadset(condition)
        assert [q['id'] for q in qs] == [q['id'] for q in other_qs]
        changed = np.load(BASE/'results'/f'{condition}-per-query.npz')
        row = {}; lines += [f'## {condition} minus wiki', '',
            '| Variant | nDCG@10 | MRR@10 | Recall@10 | Under 80 chars | Under 400 chars |',
            '|---|---|---|---|---|---|']
        for name in names:
            row[name] = {m: delta(changed[f'{name}/{m}'], original[f'{name}/{m}'], groups) for m in metrics}
            lines.append('| '+name+' | '+' | '.join(fmt(row[name][m]) for m in metrics)+' |')
        result[condition] = row
        result[condition+'_difference_in_differences'] = {
            name: {m: delta(changed[f'{name}/{m}']-original[f'{name}/{m}'],
                            changed[f'original/{m}']-original[f'original/{m}'], groups)
                   for m in metrics} for name in ['head', 'writer', 'centered', 'qwen']}
        lines += ['']
    save(BASE/'results/cross-condition.json', result)
    (BASE/'CROSS_CONDITION.md').write_text('\n'.join(lines)+'\n')
    log('Computed prespecified stress/folding condition differences and paired article-cluster intervals from saved frozen rankings; no new retrieval passes.')


if __name__ == '__main__':
    main()
