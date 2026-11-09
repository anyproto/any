#!/bin/zsh
set -eu
export PYTHONDONTWRITEBYTECODE=1
export TMPDIR="$PWD/bench/eg2-ru/tmp"
export HF_HOME="$PWD/bench/eg2-ru/cache/hf"
export OMP_NUM_THREADS=2 OPENBLAS_NUM_THREADS=2 TOKENIZERS_PARALLELISM=false
py=bench/mlx/.venv/bin/python
code=scripts/embedbench/research/eg2_ru
for split in test rig; do
 "$py" "$code/encode.py" "$split" eg2 > "bench/eg2-ru/results/encode-$split-eg2.log" 2>&1
 "$py" "$code/encode.py" "$split" qwen > "bench/eg2-ru/results/encode-$split-qwen.log" 2>&1
 "$py" "$code/evaluate.py" "$split" > "bench/eg2-ru/results/evaluate-$split.log" 2>&1
 if [[ $split == test ]]; then
  "$py" "$code/diagnose.py" test > bench/eg2-ru/results/diagnose-test.log 2>&1
  "$py" "$code/nohubs.py" test > bench/eg2-ru/results/nohubs-test.log 2>&1
 fi
done
"$py" "$code/regression.py" prepare > bench/eg2-ru/results/regression-prepare.log 2>&1
"$py" "$code/regression.py" run > bench/eg2-ru/results/regression-run.log 2>&1
"$py" "$code/native.py" > bench/eg2-ru/validation/native.log 2>&1
