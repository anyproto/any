"""Post-freeze dev-only corpus-mean alignment; no ranking or candidate changes."""
from common import *
from eg2 import EG2, mx, PREFIX


def main():
    assert (BASE/'results/all.json').exists(), 'Run after the serial confirmation job'
    ds, qs, _ = loadset('dev')
    rows = [dict(r, role='doc') for r in ds] + [dict(r, role='query') for r in qs]
    model = EG2(); dirs = np.load(BASE/'dev/directions.npz')
    ids = [model.ids(r['text'], r['role']) for r in rows]
    ends = []
    for row, tokens in zip(rows, ids):
        enc = model.tok.encode(PREFIX[row['role']] + row['text'], add_special_tokens=False)
        ends.append(1 + sum(b <= len(PREFIX[row['role']]) for a, b in enc.offsets[:len(tokens)-2]))
    order = sorted(range(len(rows)), key=lambda i: len(ids[i]))
    regions = ['pooled', 'bos', 'prompt', 'content']
    projections = np.zeros((len(rows), 24, 4), np.float32)
    sums = {role: np.zeros((24, 4, 512), np.float64) for role in ['doc', 'query']}
    units = {role: np.zeros((24, 4, 512), np.float64) for role in ['doc', 'query']}
    done = 0
    while done < len(order):
        end = done + 1
        while end < len(order) and end-done < 32 and len(ids[order[end]])*(end-done+1) <= 4096:
            end += 1
        ix = order[done:end]; n = max(len(ids[i]) for i in ix)
        lens = mx.array([len(ids[i]) for i in ix])[:, None]
        boundary = mx.array([ends[i] for i in ix])[:, None]
        pos = mx.arange(n)[None, :]
        masks = [(pos < lens), (pos == 0), (pos >= 1) & (pos < boundary), (pos >= boundary) & (pos < lens-1)]
        cap = {}
        def capture(kind, layer, x):
            if kind == 'residual':
                cap[layer] = mx.stack([mx.sum(x*m[..., None], axis=1)/mx.maximum(mx.sum(m, axis=1, keepdims=True), 1) for m in masks], axis=1)
        y = model.forward(mx.array([ids[i] + [0]*(n-len(ids[i])) for i in ix]), lens[:, 0], capture)
        mx.eval(y, cap)
        a = np.stack([np.array(cap[l]) for l in range(24)], axis=1)
        for j, i in enumerate(ix):
            role = rows[i]['role']; sums[role] += a[j]; units[role] += norm(a[j])
            for l in range(24): projections[i, l] = a[j, l] @ dirs[f'residual/{l}']
        done = end; del y, cap; mx.clear_cache()
    result = dict(note='Dev-only audit after freeze and confirmation. No retrieval pass or parameter selection.', layers={})
    for l in range(24):
        u = dirs[f'residual/{l}']; result['layers'][str(l)] = {}
        for role in ['doc', 'query']:
            result['layers'][str(l)][role] = {
                region: dict(cosine_raw_mean=float(norm(sums[role][l, j]) @ u),
                             cosine_mean_of_unit_states=float(norm(units[role][l, j]) @ u))
                for j, region in enumerate(regions)}
    result['head_contrast_vs_head_mean_cosine'] = float(dirs['head_contrast'] @ dirs['head_mean'])
    save(BASE/'dev/corpus-alignment.json', result)
    np.savez_compressed(BASE/'dev/corpus-alignment.npz', projections=projections,
                        **{f'{r}/sum': v for r, v in sums.items()},
                        **{f'{r}/unit_sum': v for r, v in units.items()})
    log('Completed dev-only corpus alignment audit for all 24 residual layers, BOS/prefix/content and actual query roles; frozen directions and candidate weights unchanged.')


if __name__ == '__main__':
    main()
