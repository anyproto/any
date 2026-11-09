# Russian informal retrieval study

The result and limitations are in `bench/eg2-ru/REPORT.md`; full tables are in
`bench/eg2-ru/TABLES.md`. This folder changes no production code. It contains a
public-data experiment and a portable, frozen document-expansion prototype.

The experiment uses the existing Q8 EG2/Qwen loaders in `../eg2_patch` read-only.
Production prefixes, mean/last-token pooling and the 2048-token context are kept.
EG2-head here is the **exact vector projection**, not a fresh quantized weight
artifact. The supplied patched Q8 model is checked separately against native
llama.cpp. No local account server, local network port, account database, or
unapproved workspace/chat export is used. Native inference uses pipe IPC with
an explicit synthetic corpus. Only one substantial process runs at a time.

## Portable pipeline candidate

`expand.py` needs only Python's standard library and the adjacent `glossary.json`.
It preserves `text`, adds `embedding_text` and marks whether expansion fired:

```sh
python3 scripts/embedbench/research/eg2_ru/expand.py \
  < bench/eg2-ru/from-rig/synthetic-lookalikes.jsonl \
  > bench/eg2-ru/expanded-rig.jsonl
```

Embed `embedding_text` with the unchanged document prefix and existing head patch.
Keep queries, display text, citations, IDs, chunk boundaries and exact search the
same. No learned parameters, generation service or runtime LLM is involved.
This is a limited 40-family dictionary: inflections, ambiguity and incomplete
coverage are intentional measured limitations. It must not be silently extended
on the held-out rig cases. The report distinguishes the vector candidate from
lexical stemming and glossary-indexing experiments; their combination is not a
validated production fix.

## Inputs and reproducibility

Existing assets: `bench/embeddinggemma-2-Q8_0.gguf`,
`bench/Qwen3-Embedding-0.6B-Q8_0.gguf`, `bench/mlx/.venv/bin/python`,
`bench/mlx/qwen3-emb-0.6b`, `bench/eg2-patch/hf`,
`bench/granite/llama.cpp-b11600/gguf-py`, `bench/granite/bin`,
`bench/llamacpp-b11600`, and the public sets/results under `bench/eg2-patch`.

Run from repo root. Generated files and environment/cache/temp overrides stay
under `bench/eg2-ru`. Dependencies (`zstandard`, `snowballstemmer`, `uniseg`) are
installed into `bench/eg2-ru/deps`; do not modify the existing MLX environment.
`util.py` sets cache/temp paths before importing the old loaders, and never calls
their logging or output helpers. Set `PYTHONDONTWRITEBYTECODE=1` at launch too.

The experiment is already scored. Evaluation scripts deliberately refuse to
overwrite results. Reproduction requires a fresh generated-output tree with
approved `from-rig` inputs preserved; do not delete the delivered experiment to
run extra held-out passes. The fixed source sample, authored TSVs, frozen
manifest, directions, glossary and result arrays are the reproducibility record.
The first 6,000 public StackOverflow threads were streamed, not a private chat
collection; 824 passages were selected by ID hash. `sources/provenance.json` and
per-record URL/author/timestamp/license retain attribution. The dataset card's
blanket 2.5 license is qualified by Stack Overflow's per-date license policy;
post revisions may have newer licenses than their creation timestamps.

Sequence used:

```sh
export PYTHONDONTWRITEBYTECODE=1
export TMPDIR="$PWD/bench/eg2-ru/tmp"
export PIP_CACHE_DIR="$PWD/bench/eg2-ru/cache/pip"
py=bench/mlx/.venv/bin/python
code=scripts/embedbench/research/eg2_ru
"$py" -m pip install --target bench/eg2-ru/deps zstandard snowballstemmer uniseg
"$py" "$code/download.py"
"$py" "$code/prepare.py"
"$py" "$code/encode.py" dev eg2
"$py" "$code/encode.py" dev qwen
"$py" "$code/evaluate.py" dev
"$py" "$code/diagnose.py" dev
"$py" "$code/nohubs.py" dev
"$py" "$code/freeze.py"
"$py" "$code/rig_prepare.py"
zsh "$code/confirm.sh"
"$py" "$code/lexical_regression.py"
"$py" "$code/tables.py"
"$py" "$code/audit.py"
"$py" "$code/write_report.py"
```

The dev mixed-script tail probe was added before freeze after the first lexical
analysis. Its held-out diagnostic is complete; the saved dev diagnostic lacks
those extra fields. Encoding can resume checkpoints or append records; it rejects
changed ordering. Never use append/resume to change a frozen experiment.

## Statistical design

20 dev and 20 held-out invented scenarios use disjoint term families; 11/10 public
Q&A/comment targets form the other split. Three queries per target: keyword,
Russian paraphrase and English paraphrase. English translations allow reverse
RU→EN diagnostics. The glossary covers both authored splits before scoring: this
measures a **fixed dictionary on new contexts**, not zero-shot unseen-word learning.
Eight externally authored rig cases were opened only after `FROZEN.json`.

Primary MRR@10; also nDCG@10 and Recall@10. Differences have 4,000 paired bootstrap
resamples, seed 391, clustered by target when queries share a passage. All slices
are pointwise and small. Exact vector search; hybrid is a single-field BM25
estimate, k1=1.2, b=.75, equal-weight RRF k=60, 30 hits per leg. No title boosts,
app stopword stripping, adaptive weighting, query operators, filters or ANN.

50 main dev passes: 42 main variants and eight no-artificial-hub sensitivity
variants. Lexical similarity, reverse-direction and numerical parity diagnostics
are separately recorded. Each held-out variant/set is scored once. Later tables,
hub analysis and direct hybrid-to-hybrid CIs reuse saved per-query arrays.

For a rank-two edit, `project.py` applies:

```
h = (I - u uᵀ) v
v_new = normalize((I - alpha l lᵀ) h)
```

`u` is the existing head vector. `l` is the normalized dev RU−EN or slang−plain
mean difference, orthogonalized to `u`. Doses are language .25 / slang .5.
`bench/eg2-ru/dev/directions.npz` and `FROZEN.json` define exact values. Random
controls are orthogonal and match the embedding-operator Frobenius edit norm.
The corresponding raw output-matrix edit is `(I-alpha*l*lᵀ)(I-u*uᵀ)W`; subsequent
quantization is not identical to the exact-space experiment. These additional
projections are controls, not recommended production patches.

`regression.py` reconstructs the deterministic **public prefix** of the prior
embedding bank from public set files and its run metadata. It never opens the
mixed `records.jsonl` or reads private vector rows. The final audit re-encodes
public samples to validate the prefix mapping. Regression candidates are checked
on full FiQA, wiki stress, SciFact, MIRACL RU/DE pools and Russian wiki. Lexical
no-op proofs avoid needless computation on purely Latin queries when glossary
expansion fires nowhere.

Two post-freeze correctness repairs are documented in `CORRECTIONS.json`: the
JSONL reader now iterates actual file lines. `str.splitlines()` incorrectly split
valid public FiQA records on Unicode separators inside JSON strings. The repair
occurred before regression scoring; no candidate or data changed. An import-path correction also keeps the local evaluator ahead of the identically
named shared-loader evaluator. Native validation was rerun after that repair.
The integrity audit verifies the original freeze and both explicit corrections.
