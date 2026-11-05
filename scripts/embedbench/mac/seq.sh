#!/bin/zsh
# Runs the step-1 matrix back to back with cooldowns; thermal state logged.
S=${0:A:h}
W=$(git -C "${0:A:h}" rev-parse --show-toplevel)
B=${EMBEDBENCH_DIR:-$W/bench}
cd $B
log=$B/seq.log
: > $log
run() {
  echo "=== $1 start $(date +%T) batt: $(pmset -g batt | tail -1 | cut -f2)" >> $log
  pmset -g therm | grep -v "^Note" >> $log
  $S/run.sh "$@" >> $log 2>&1
  echo "=== $1 end $(date +%T)" >> $log
  pmset -g therm | grep -v "^Note" >> $log
  sleep 90
}
run eg2-q8-metal   embeddinggemma-2-Q8_0.gguf     eg2   metal
run eg2-q8-cpu     embeddinggemma-2-Q8_0.gguf     eg2   cpu
run eg2-bf16-cpu   embeddinggemma-2-BF16.gguf     eg2   cpu
run eg2-bf16-metal embeddinggemma-2-BF16.gguf     eg2   metal
run qwen3-q8-metal Qwen3-Embedding-0.6B-Q8_0.gguf qwen3 metal
run qwen3-q8-cpu   Qwen3-Embedding-0.6B-Q8_0.gguf qwen3 cpu
run eg2-q8-cpu-t4  embeddinggemma-2-Q8_0.gguf     eg2   cpu 4
echo "=== ALL DONE $(date +%T)" >> $log
