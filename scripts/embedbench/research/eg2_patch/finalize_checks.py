"""Finish native writer checks and audit immutable artifacts after confirmation."""
from common import *
import argparse


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--native', action='store_true')
    ap.add_argument('--cleanup-traces', action='store_true')
    args = ap.parse_args()
    assert (BASE/'results/all.json').exists(), 'Wait for the serial confirmation job'
    frozen = json.loads((BASE/'FROZEN.json').read_text())
    if args.native:
        from validate import native, vectors, compare
        p = BASE/'validation/results.json'
        res = json.loads(p.read_text())
        for dtype, version in [('BF16', 'b11600'), ('BF16', 'b11454'), ('Q8_0', 'b11454')]:
            stem = f'writer-{dtype}-{version}'
            if stem in res:
                assert res[stem]['min_cosine'] >= .9999
                continue
            native(stem, f'eg2-patch/out/embeddinggemma-2-writer-{dtype}.gguf', 'eg2', version)
            res[stem] = compare(np.load(BASE/'validation'/f'writer-{dtype}.npy'), vectors(stem))
            save(p, res)
            assert res[stem]['min_cosine'] >= .9999, (stem, res[stem])
            log(f'Stock {stem} parity passed: min cosine {res[stem]["min_cosine"]:.8f}.')
    verified = {}; missing_temporary = []; source_expected = {}
    for manifest_name, manifest in frozen['manifests'].items():
        assert json.loads((BASE/'out'/manifest_name).read_text()) == manifest
        for dtype, info in manifest['files'].items():
            if dtype in source_expected:
                assert source_expected[dtype] == info['source_sha256']
            source_expected[dtype] = info['source_sha256']
            path = ROOT/info['path']
            if not path.exists():
                assert dtype == 'BF16' and 'random' in path.name
                missing_temporary.append(path.name)
                continue
            digest = sha(path)
            assert digest == info['sha256'], path
            verified[path.name] = digest
    for relative, expected in frozen['data_sha256'].items():
        assert sha(BASE/relative) == expected, relative
    assert sha(BASE/'dev/directions.npz') == frozen['direction_sha256']
    assert sha(BASE/'out/patch-directions.npz') == frozen['direction_sha256']
    # Rebuild edited payloads from packaged sidecars without writing duplicate GGUFs.
    import gguf
    from edit_math import build_edit
    from types import SimpleNamespace
    source_reader = gguf.GGUFReader(str(ROOT/'bench/embeddinggemma-2-BF16.gguf'))
    wanted = {site['tensor'] for m in frozen['manifests'].values() for site in m['spec']['sites']}
    source_matrices = {t.name: gguf.dequantize(t.data, t.tensor_type).copy()
                       for t in source_reader.tensors if t.name in wanted}
    directions = np.load(BASE/'out/patch-directions.npz')
    for manifest in frozen['manifests'].values():
        edits, _ = build_edit(SimpleNamespace(original=source_matrices), manifest['spec'], directions)
        for info in manifest['files'].values():
            path = ROOT/info['path']
            if not path.exists():
                continue
            reader = gguf.GGUFReader(str(path))
            for t in reader.tensors:
                if t.name in edits:
                    assert np.array_equal(gguf.quantize(edits[t.name], t.tensor_type), t.data), (path, t.name)
    source_verified = {}
    for dtype, expected in source_expected.items():
        path = ROOT/'bench'/f'embeddinggemma-2-{dtype}.gguf'
        source_verified[path.name] = sha(path)
        assert source_verified[path.name] == expected
    source_verified['Qwen3-Embedding-0.6B-Q8_0.gguf'] = sha(ROOT/'bench/Qwen3-Embedding-0.6B-Q8_0.gguf')
    rig = json.loads((BASE/'results/rig-provenance.json').read_text())
    for name, expected in rig['source_hashes'].items():
        assert sha(BASE/'from-rig'/name) == expected
    assert sha(BASE/'from-rig/README.md') == rig['readme_sha256']
    rig_prepared = {str(p.relative_to(BASE)): sha(p) for name in ['rig-known', 'rig-topical']
                    for p in (BASE/'sets'/name).iterdir() if p.is_file()}
    (BASE/'out/SHA256SUMS').write_text(''.join(f'{digest}  {name}\n' for name, digest in sorted(verified.items())))
    removed = []
    if args.cleanup_traces:
        # These are generated full per-token dumps, not source/export files.
        # Pooled activations, offset-corrected audits and directions remain.
        for pattern in ['tokens-*.npz', 'query-tokens-*.npz']:
            for path in sorted((BASE/'dev').glob(pattern)):
                removed.append(dict(path=str(path.relative_to(BASE)), bytes=path.stat().st_size))
                path.unlink()
    save(BASE/'results/final-audit.json', dict(
        verified_sha256=verified, source_sha256=source_verified,
        cleared_source_unchanged=True, cleared_prepared_sha256=rig_prepared,
        reconstructed_edited_tensor_payloads_match=True,
        removed_temporary_random_bf16=missing_temporary,
        removed_token_dumps=removed, development_passes=len(read(BASE/'dev/passes.jsonl')),
        frozen_data_unchanged=True, directions_unchanged=True))
    log(f'Final audit: {len(verified)} frozen GGUF hashes and all frozen input/direction hashes match; SHA256SUMS saved. Removed {sum(r["bytes"] for r in removed)/2**20:.1f} MiB of reproducible temporary token traces.')


if __name__ == '__main__':
    main()
