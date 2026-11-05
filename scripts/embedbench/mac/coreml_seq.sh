#!/bin/zsh
# Core ML runs back to back with cooldowns; footprint of the python process sampled.
S=${0:A:h}
W=$(git -C "${0:A:h}" rev-parse --show-toplevel)
B=${EMBEDBENCH_DIR:-$W/bench}
cd $B
PY=$B/coreml/.venv/bin/python
FB=coreml/filbona/EmbeddingGemma2.mlmodelc
NM=coreml/nemosine/EmbeddingGemma2TextANE.mlpackage
TOK=coreml/filbona/tokenizer.json
log=$B/coreml-seq.log
: > $log
crun() {
  name=$1; shift
  echo "=== $name start $(date +%T) batt: $(pmset -g batt | tail -1 | cut -f2)" >> $log
  $PY -I $S/coreml_bench.py "$@" fiqa-1500.jsonl $name > $name.log 2>&1 &
  p=$!
  : > $name.footprint
  for s in 20 25; do
    sleep $s; kill -0 $p 2>/dev/null || break
    footprint $p 2>&1 | grep -E "Footprint:|phys_footprint" >> $name.footprint
  done
  wait $p; echo "exit=$?" >> $name.log
  echo "=== $name end $(date +%T)" >> $log
  pmset -g therm | grep -v "^Note" >> $log
  sleep 60
}
# FilBona: fixed 512, int8 weights.
crun filbona-ane    $FB $TOK --units CPU_AND_NE --buckets 512 --batch 8
crun filbona-cpu    $FB $TOK --units CPU_ONLY   --buckets 512 --batch 1 --max-docs 200 --skip-queries
crun filbona-gpu    $FB $TOK --units CPU_AND_GPU --buckets 512 --batch 1 --max-docs 200 --skip-queries
# Nemosine: fp16, multifunction s128..s2048, attention_mask float32.
crun nemosine-ane   $NM $TOK --units CPU_AND_NE --buckets 128,256,512,1024,2048 --function-fmt s%d --mask-input attention_mask --mask-dtype float32 --batch 8
crun nemosine-ane512 $NM $TOK --units CPU_AND_NE --buckets 512 --function-fmt s%d --mask-input attention_mask --mask-dtype float32 --batch 1 --skip-queries
crun nemosine-cpu   $NM $TOK --units CPU_ONLY   --buckets 128,256,512,1024,2048 --function-fmt s%d --mask-input attention_mask --mask-dtype float32 --batch 1 --max-docs 200 --skip-queries
echo "=== ALL DONE $(date +%T)" >> $log
