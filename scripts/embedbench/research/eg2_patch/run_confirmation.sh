#!/bin/bash
# Run from the repository root. One substantial compute process at a time.
set -euo pipefail
export PYTHONDONTWRITEBYTECODE=1
export TMPDIR="$PWD/bench/eg2-patch/tmp"
export HF_HOME="$PWD/bench/eg2-patch/cache/hf"
export OMP_NUM_THREADS=2 OPENBLAS_NUM_THREADS=2 TOKENIZERS_PARALLELISM=false
py=bench/mlx/.venv/bin/python
code=scripts/embedbench/research/eg2_patch
data=bench/eg2-patch
sets=(wiki wiki-mix wiki-fold ru fiqa10k fiqa-full scifact nfcorpus miracl-ru-pool miracl-de-pool rig-known rig-topical)
test -f "$data/FROZEN.json"
test -f "$data/validation/qwen-f16-parity.json"
"$py" -u "$code/prompt_check.py" > "$data/dev/prompt-check.log" 2>&1
"$py" -u "$code/encode_joint.py" --sets "${sets[@]}" > "$data/vectors/joint.log" 2>&1
"$py" -u "$code/encode_eval.py" qwen --qwen --sets "${sets[@]}" > "$data/vectors/qwen.log" 2>&1
"$py" -u "$code/evaluate.py" --sets "${sets[@]}" > "$data/results/evaluate.log" 2>&1
