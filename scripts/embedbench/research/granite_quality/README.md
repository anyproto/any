# Granite quality diagnosis

All embedding inputs are public Wikipedia, BEIR FiQA, FLORES-200, or explicitly synthetic
templates made from public headings. No account services or private text are used.
Run from the repository root. All output belongs in `bench/granite-quality/`.
The two existing environments and public model directories are read only.

```sh
export PYTHONDONTWRITEBYTECODE=1
export TMPDIR="$PWD/bench/granite-quality/tmp"
export HF_HOME="$PWD/bench/granite-quality/cache/hf"
export HF_HUB_OFFLINE=1
export TOKENIZERS_PARALLELISM=false
export OMP_NUM_THREADS=2
export OPENBLAS_NUM_THREADS=2
mkdir -p "$TMPDIR"

bench/granite/.venv/bin/python scripts/embedbench/research/granite_quality/build_sets.py
bench/granite/.venv/bin/python scripts/embedbench/research/granite_quality/audit.py
bench/granite/.venv/bin/python scripts/embedbench/research/granite_quality/encode.py granite
bench/granite/.venv/bin/python scripts/embedbench/research/granite_quality/probe.py
bench/mlx/.venv/bin/python scripts/embedbench/research/granite_quality/encode.py qwen
bench/granite/.venv/bin/python scripts/embedbench/research/granite_quality/selfcheck.py
bench/granite/.venv/bin/python scripts/embedbench/research/granite_quality/analyze.py
bench/granite/.venv/bin/python scripts/embedbench/research/granite_quality/geometry.py
bench/granite/.venv/bin/python scripts/embedbench/research/granite_quality/diagnose.py
bench/granite/.venv/bin/python scripts/embedbench/research/granite_quality/language_control.py
bench/granite/.venv/bin/python scripts/embedbench/research/granite_quality/tables.py
```

Run these sequentially: one substantial compute job at a time. The encoder sorts
by token length and batches to a token budget. Granite uses fp32 MPS, CLS, no
prompts, and a 2048-token cap; Qwen uses bf16 MLX, causal right padding, last valid
token pooling, the production query instruction, and terminal EOT. Single versus
batched inference checks are saved. Token truncation preserves terminal EOS as
production does; the initial investigation also recorded the ordinary HF-tail
versus production-tail difference on over-limit texts.

`build_sets.py` reuses downloaded Wikipedia extracts so IDs remain stable. A
`--refresh` changes public text and may invalidate `ruq.py`'s hardcoded target IDs;
manually revalidate all 30 targets before using refreshed results. Data hashes
identify the actual experiment snapshot. The short distractors are derived from
all document headings, independently of queries; they mention a topic but provide
no substantive answer. They are a stress test, not a representative chat sample.

Calibration uses only photosynthesis documents and queries. Evaluation of fixes
uses disjoint city/Python documents and queries, plus the entire FiQA10k subset as
a transfer test. Generic texts appearing identically in both article partitions
are removed from evaluation. FLORES first 160 dev sentence triples train language offsets;
first 240 devtest triples evaluate them, with no source-article overlap. The best
variant is selected on calibration nDCG only; all other variants are exploratory
and their CIs are not corrected for multiple comparisons.

Retrieval metrics are nDCG@10 and MRR@10 (zero beyond rank 10), with exact cosine
search. CIs use 4,000 seeded paired bootstrap resamples, sharing query samples
between variants/models. Russian/English versions of one target are grouped;
FLORES resamples source articles. Wiki article-cluster intervals are a sensitivity
check in the JSON. Nonlinear geometry statistics use 500 paired
resamples. PCA axes for comparative held-out variance are frozen on FLORES dev;
full corpus PCA spectra are also descriptive outputs. No IVF behavior is claimed.

Outputs: `debug/results.json`, `geometry.json`, `probes.json`, per-query metric
arrays, calibration parameters, `mechanism.json`, `public_top_hits.jsonl`,
`TABLES.md`, and `REPORT.md`. `diagnose.py` compares 0/100/400/1670 topic-bearing
short distractors and an unrelated short-FiQA control, plus matched heading/body
norm and direction measurements. `language_control.py` restricts wiki candidates
to each query's language to expose language-specific qrels artifacts; it does not
rejudge foreign-language answers. One empty FiQA document uses CLS as the defined
fallback for content-only mean pooling. The large temporary vector banks
are removed at the end of the original investigation; rerun encoding before
rerunning analysis. The calibration `.npz` contains transformation parameters, not
patched model weights. No model files are changed.

Optional late rig metadata: run `rig_metadata.py` only when the text-free
`from-rig/known-ranks.jsonl` and `from-rig/topical-hits.jsonl` exist. It checks their
schemas and writes `debug/rig_metadata.json`. It never opens query/gold/pool text
files. The resulting 108-query rank analysis includes weak rows, because the weak
flags are only available in a text-bearing file; it must not be presented as the
canonical filtered account metric. No local service is contacted.
