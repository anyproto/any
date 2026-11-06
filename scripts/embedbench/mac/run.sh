#!/bin/zsh
# run.sh <out-name> <model.gguf> <eg2|qwen3|granite> <metal|cpu> [threads]
# EMBEDBENCH_LIBDIR overrides the llama.cpp libs (default <repo>/bin/llamacpp), EMBEDBENCH_BIN the dir
# holding `any` and `indexer.test` (default <repo>/bin); a newer llama.cpp needs both built against a matching yzma.
# Runs TestEmbedBench once from the data dir ($EMBEDBENCH_DIR, default <repo>/bench), samples `footprint` of the
# `any run embedder` child mid-run, logs to <out-name>.log.
set -u
W=$(git -C "${0:A:h}" rev-parse --show-toplevel)
BIN=${EMBEDBENCH_BIN:-$W/bin}
B=${EMBEDBENCH_DIR:-$W/bench}
cd $B
name=$1 model=$2 kind=$3 be=$4 threads=${5:-}
unset ANY_EVAL_LOCAL_DOC_PREFIX ANY_EVAL_LOCAL_QUERY_PREFIX ANY_EVAL_LOCAL_POOLING ANY_EVAL_LOCAL_GPU_LAYERS ANY_EVAL_LOCAL_THREADS
export ANY_BENCH_ANY_BIN=$BIN/any ANY_EVAL_LOCAL_LIBDIR=${EMBEDBENCH_LIBDIR:-$W/bin/llamacpp} ANY_BENCH_CORPUS=${ANY_BENCH_CORPUS_OVERRIDE:-$B/fiqa-1500.jsonl}
export ANY_EVAL_LOCAL_MODEL=$B/$model ANY_BENCH_OUT=$name
if [[ $kind == eg2 ]]; then
  export ANY_EVAL_LOCAL_POOLING=mean
  export ANY_EVAL_LOCAL_DOC_PREFIX='title: none | text: '
  export ANY_EVAL_LOCAL_QUERY_PREFIX='task: search result | query: '
elif [[ $kind == granite ]]; then
  export ANY_EVAL_LOCAL_POOLING=cls  # no prompts on either side
else
  export ANY_EVAL_LOCAL_QUERY_PREFIX=$'Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:'
fi
[[ $be == cpu ]] && export ANY_EVAL_LOCAL_GPU_LAYERS=0
[[ -n $threads ]] && export ANY_EVAL_LOCAL_THREADS=$threads

$BIN/indexer.test -test.run 'TestEmbedBench$' -test.v -test.timeout 60m > $name.log 2>&1 &
tpid=$!
: > $name.footprint
for s in 20 25; do
  sleep $s
  kill -0 $tpid 2>/dev/null || break
  cpid=$(pgrep -f "$BIN/any run embedder" | head -1)
  [[ -n $cpid ]] && { echo "### t+sample pid=$cpid"; footprint $cpid 2>&1 | head -40; } >> $name.footprint
done
wait $tpid
echo "exit=$?" >> $name.log
grep -E "RESULT|child up|FAIL|panic" $name.log | head -3
grep -E "phys_footprint|Footprint|TOTAL" $name.footprint | head -6
