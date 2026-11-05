#!/bin/zsh
# The iPhone sequence. Cooldowns run the app in idle mode so the screen
# stays on (auto-lock would block the next launch).
cd "${0:A:h}"
NM="--model models/Nemosine.mlmodelc --function-fmt s%d --mask-input attention_mask"
FB="--model models/FilBona.mlmodelc --buckets 512"
ALL=128,256,512,1024,2048
cool() { ./irun.sh cool-$1 --mode idle --idle-sec ${2:-60} >/dev/null; }
r() { echo "$(date +%T) $1"; ./irun.sh "$@"; cool $1; }
r nemosine-ane    --mode coreml ${=NM} --units ane --buckets $ALL --batch 8
r nemosine-ane512 --mode coreml ${=NM} --units ane --buckets 512 --batch 1 --skip-queries
r filbona-ane     --mode coreml ${=FB} --units ane --batch 8
r filbona-cpu     --mode coreml ${=FB} --units cpu --batch 1 --max-docs 200 --skip-queries
r filbona-gpu     --mode coreml ${=FB} --units gpu --batch 1 --max-docs 200 --skip-queries
r nemosine-cpu    --mode coreml ${=NM} --units cpu --buckets $ALL --batch 1 --max-docs 200 --skip-queries
r nemosine-cpu3   --mode coreml ${=NM} --units cpu --buckets 128,256,512 --batch 1 --max-docs 200 --skip-queries
r nemosine-gpu    --mode coreml ${=NM} --units gpu --buckets $ALL --batch 1 --max-docs 200 --skip-queries
r plan-nemosine   --mode plan ${=NM} --units ane --buckets $ALL
r plan-filbona    --mode plan --model models/FilBona.mlmodelc --units ane
r eg2-q8-metal    --mode llama --model gguf/embeddinggemma-2-Q8_0.gguf --kind eg2
r eg2-bf16-metal  --mode llama --model gguf/embeddinggemma-2-BF16.gguf --kind eg2 --skip-queries
r qwen3-q8-metal  --mode llama --model gguf/Qwen3-Embedding-0.6B-Q8_0.gguf --kind qwen3
r eg2-q8-cpu      --mode llama --model gguf/embeddinggemma-2-Q8_0.gguf --kind eg2 --cpu --max-docs 300 --skip-queries
r eg2-q8-cpu-t2   --mode llama --model gguf/embeddinggemma-2-Q8_0.gguf --kind eg2 --cpu --threads 2 --max-docs 300 --skip-queries
r qwen3-q8-cpu    --mode llama --model gguf/Qwen3-Embedding-0.6B-Q8_0.gguf --kind qwen3 --cpu --max-docs 200 --skip-queries
echo "$(date +%T) SEQ DONE"
