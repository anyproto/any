# EG2 direct weight surgery

Public-data experiment for SYN-391. All generated files belong under
`bench/eg2-patch/`; these scripts are the only repository changes. No gradients,
optimizer, account services, local network ports, tokenizer changes, prompt
changes, pooling changes, commits or branch changes.

The authoritative experiment specification is `bench/eg2-patch/PLAN.md`.
`FROZEN.json` records candidate specs, dataset hashes, the 47-pass dev budget used,
and immutable artifact hashes before held-out scoring or opening cleared rig text.
It is a supplied experiment record consumed by the confirmation scripts, not
an output of `artifact.py`. Preserve it, the saved sweep specs, public source
inputs and the cleared export when reproducing this frozen experiment.
`PROGRESS.md` is the chronological log; `REPORT.md` and numeric tables describe
the outcome. Development uses only photosynthesis articles. All other wiki
articles, BEIR test queries, MIRACL samples and the cleared rig are confirmation.

## Environment and inputs

Run from the repository root on this Mac. Existing environments/models are read
only. The scripts use the existing MLX Python and the existing stock gguf-py source.

```sh
export PYTHONDONTWRITEBYTECODE=1
export TMPDIR="$PWD/bench/eg2-patch/tmp"
export HF_HOME="$PWD/bench/eg2-patch/cache/hf"
export OMP_NUM_THREADS=2 OPENBLAS_NUM_THREADS=2 TOKENIZERS_PARALLELISM=false
mkdir -p bench/eg2-patch/{tmp,cache,hf,sources,dev,out,validation,results,vectors,sets}
```

Required existing inputs: `bench/embeddinggemma-2-{BF16,Q8_0}.gguf`,
`bench/Qwen3-Embedding-0.6B-Q8_0.gguf`, Qwen config/tokenizer under
`bench/mlx/qwen3-emb-0.6b`, public FiQA under `bench/fiqa`, and frozen public sets
under `bench/granite-quality/sets`. The source GGUF hashes are in the artifact
manifests. The HF source is `google/embeddinggemma-2`, revision
`f9972ac22e696a105c482202646979e33e4b5a1e`; config and tokenizer JSON are saved in
`bench/eg2-patch/hf`. Text weights are read directly from GGUF, avoiding a redundant
checkpoint download.

The MIRACL text repacks are public `Hieuman/{ru,de}_MIRACL` parquet files. Official
MIRACL dev topic and qrel TSVs cross-check query strings and positive/negative
counts. Repack passage IDs are unavailable, a documented limitation. PyArrow is
installed only in `bench/eg2-patch/deps`; no environment outside the allowed paths
is modified. Each language uses a fixed hash-selected 200-query sample and the
union of its judged passages, not the full collection. Provenance is saved beside
each set. See `prepare_miracl.py` for the deterministic selection.

## Development sequence

```sh
py=bench/mlx/.venv/bin/python
code=scripts/embedbench/research/eg2_patch
"$py" "$code/prepare.py"
"$py" "$code/prepare_sources.py"
"$py" "$code/prepare_miracl.py"
"$py" "$code/probe.py"
"$py" "$code/sweep.py" isolate
"$py" "$code/sweep.py" dose --specs bench/eg2-patch/dev/dose-specs.json
"$py" "$code/sweep.py" sparse --specs bench/eg2-patch/dev/sparse-specs.json
"$py" "$code/head_sweep.py"
"$py" "$code/sweep.py" custom --specs bench/eg2-patch/dev/control-specs.json
```

Saved specs preserve the adaptive order without repeating selection. Do not run
these commands over the original results to create extra passes. `passes.jsonl`
counts every retrieval variant; model parity and activation-only diagnostics are
separate. A fresh reproduction should use an empty generated-output tree while
preserving public source inputs and the cleared export.

`probe.py` records every layer's residual and attention/MLP/PLE writer inputs,
raw outputs and normalized residual contributions. All matched pairs have pooled,
BOS, prefix and content summaries. Complete per-token traces are collected for
representative pairs; `prompt_check.py` also collects actual dev-query traces and
uses tokenizer offsets for precise prefix boundaries. Large temporary traces may
be compacted after analysis; scripts recreate them. No layer/weight selection
uses the post-freeze audit.

## Rebuild frozen weights

```sh
"$py" "$code/artifact.py" bench/eg2-patch/dev/head-spec.json embeddinggemma-2-patched
"$py" "$code/artifact.py" bench/eg2-patch/dev/writer-spec.json embeddinggemma-2-writer
"$py" "$code/artifact.py" bench/eg2-patch/dev/head-random-spec.json embeddinggemma-2-head-random
"$py" "$code/artifact.py" bench/eg2-patch/dev/writer-random-spec.json embeddinggemma-2-writer-random
```

The writer edit is `W[:,J] -= 0.5 * outer(u, u @ W[:,J])` in
`blk.23.ffn_down.weight`, with 32 frozen columns and a unit residual contrast
direction. The separate head edit is `W -= outer(u, u @ W)` in `output.weight`,
where `u` is the unit mean of dev document unit embeddings. `directions.npz`,
specs and manifests contain every value, column index and hash needed to rebuild.
Random directions are seeded, orthogonal to the target direction, use the same
columns, and match the pre-rounding Frobenius change norm.

The publisher refuses to overwrite existing GGUFs. It copies each original,
changes only named tensor payloads, and verifies that metadata, tensor names,
shapes, types and all other payloads are byte-identical. BF16 rounding precedes
Q8_0 reference quantization. Unedited Q8 tensors retain ggml-org's exact bytes.
The Q8 reference quantizer reproduces the original bytes at both edited sites.
BF16 random-control files are temporary; the Q8 controls and reconstruction specs
are retained.

For a reconstruction from the small packaged sidecars, use the spec JSONs in
`bench/eg2-patch/out/` and pass
`--directions bench/eg2-patch/out/patch-directions.npz` to `artifact.py`.
The publisher and `edit_math.py` need NumPy and gguf-py, not MLX. Reconstruct in
a clean output tree or choose a new basename, since overwriting frozen GGUFs is
deliberately refused. For example:

```sh
"$py" "$code/artifact.py" bench/eg2-patch/out/head-spec.json \
  embeddinggemma-2-rebuilt --directions bench/eg2-patch/out/patch-directions.npz
```

## Confirmation and native checks

```sh
"$py" "$code/validate.py"
"$py" "$code/rig_prepare.py"  # requires FROZEN.json; final-only cleared export
bash "$code/run_confirmation.sh"
```

`validate.py` invokes the production `scripts/embedbench/mac/run.sh` harness with
an explicit public/synthetic JSONL corpus override, a 2048-token context and the
production prefixes. Both stock b11454 and b11600 load the primary BF16/Q8 files.
It checks cosine agreement >=0.9999, Q8/BF16 drift and exact linear head caching.
The Qwen float16 arithmetic check is in `validation/qwen-f16-parity.json`; the
confirmation runner requires it. All native children communicate through the
authorized benchmark harness, not account servers.

For an explicit manual native run:

```sh
TMPDIR="$PWD/bench/eg2-patch/tmp" ANY_BENCH_CTX=2048 \
ANY_BENCH_CORPUS_OVERRIDE="$PWD/bench/eg2-patch/validation/corpus.jsonl" \
EMBEDBENCH_BIN="$PWD/bench/granite/bin" \
EMBEDBENCH_LIBDIR="$PWD/bench/llamacpp-b11600" \
scripts/embedbench/mac/run.sh eg2-patch/validation/manual \
  eg2-patch/out/embeddinggemma-2-patched-Q8_0.gguf eg2 metal
```

`encode_joint.py` computes the unchanged prefix once, then each distinct final
MLP/PLE/output path. It requires >=0.999999 cosine agreement with independent
full forward passes. Head-only candidates use the exact linear map on cached
unnormalized original outputs; the pseudoinverse reconstructs the existing pooled
512-state, not a fitted head. Separate direct-forward/native checks verify it.
Chunk files permit resuming interrupted encodings. Run only one heavy process.

`evaluate.py` refuses to score an already-scored set. It reports nDCG@10 (graded
gains where available), MRR@10, Recall@10 and top-ten fractions below 80/400 Unicode
characters. Every difference against original EG2 or Qwen has a seeded 4,000-draw
paired bootstrap interval. Russian paraphrases and rig known-items resample by
target; wiki article-cluster intervals supplement query intervals. Results are
pointwise, not corrected for multiple comparisons. Dev intervals are exploratory.

All rankings are exact cosine. Rig topical queries rerank their own judged pools;
known-item queries search the pool/gold union. These results do not reproduce the
rig's full-index IVF-SQ scores or production BM25/RRF hybrid scores. Any deployed
weight change requires re-embedding documents and rebuilding the vector index.

After confirmation, render the tables and finish the immutable-artifact audit:

```sh
"$py" "$code/summarize.py"
"$py" "$code/cross_condition.py"
"$py" "$code/corpus_audit.py"
"$py" "$code/finalize_checks.py" --native --cleanup-traces
"$py" "$code/plot_doses.py"
"$py" "$code/write_report.py"
```

The final audit also checks both writer formats with both stock llama.cpp builds,
verifies every retained GGUF against the pre-evaluation hashes, and writes
`out/SHA256SUMS`. Cleanup removes only this experiment's reproducible full token
dumps; pooled activation summaries, directions, per-query metrics and vector
banks remain. `plot_doses.py` uses Matplotlib installed under the allowed data
directory. Tables are regenerated from saved measurements without new retrieval
passes. To resume a partially completed scoring run, pass only unscored sets to
`evaluate.py`; do not remove the already-scored results or tune from them.
`corpus_audit.py` measures frozen residual-direction alignment with dev corpus,
prefix, BOS and query means after confirmation; it neither scores retrieval nor
changes parameters.
The original run needed `interval_audit.py` to correct omitted target grouping
in two short-positive subset intervals. The scorer now applies that grouping
directly. The audit preserves old/new bounds and never reruns retrieval.
`write_report.py` assembles this experiment's interpreted report from saved
results and the accompanying `bench/eg2-patch/report-methods.md` narrative.
To verify delivered files, run `shasum -a 256 -c SHA256SUMS` from
`bench/eg2-patch/out/`.
