# Qwen3-Embedding-0.6B on the ANE (erjigit17 8-bit Core ML, one package per sequence bucket) with one bucket
# (512) and two (128 + 512), plus same-session controls: EG2 on the ANE (Nemosine, buckets 128-1024) and
# Qwen3 Q8 on Metal (llama.cpp b11600), for power_seq.sh.
# Metal needs EMBEDBENCH_BIN and EMBEDBENCH_LIBDIR (see run.sh). Warm each Core ML model once before this
# sequence: the first load compiles it for the ANE (30-120 s), which would land in the power window.
: ${EMBEDBENCH_BIN:?} ${EMBEDBENCH_LIBDIR:?}
q_run() { # name corpus buckets
  local name=$1 corpus=$2 b=$3
  local st=$(date +%s)
  $PY -I $S/qwen3_coreml_bench.py single "$B/qwen3ane/erjigit/qwen3-b1_s%d-8bit.mlpackage" $B/qwen3ane/erjigit/tokenizer.json \
    $P/$corpus $P/$name --units CPU_AND_NE --buckets $b --skip-queries > $P/$name.log 2>&1
  mark $name $st
  sleep 60
}
q_run  qwen3-ane8-512      fiqa-x2.jsonl 512
q_run  qwen3-ane8-128-512  fiqa-x3.jsonl 128,512
ml_run nemosine-ane4-ctl   fiqa-x10.jsonl coreml/nemosine/EmbeddingGemma2TextANE.mlpackage $TOK --units CPU_AND_NE --buckets 128,256,512,1024 --function-fmt s%d --mask-input attention_mask --mask-dtype float32
go_run qwen3-q8-metal-b11600 Qwen3-Embedding-0.6B-Q8_0.gguf qwen3 metal fiqa-x2.jsonl
