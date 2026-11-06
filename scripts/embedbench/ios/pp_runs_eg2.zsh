# EG2 on the ANE (Nemosine) and Metal, and Qwen3 on Metal, for ppseq.sh.
NM=(--model models/Nemosine.mlmodelc --function-fmt s%d --mask-input attention_mask)
idle pp-idle-0 90
rec pp-nemosine4 --mode coreml $NM --units ane --buckets 128,256,512,1024 --batch 1 --skip-queries --repeat 10
idle pp-idle-1 120
rec pp-eg2-q8-metal --mode llama --model gguf/embeddinggemma-2-Q8_0.gguf --kind eg2 --skip-queries --repeat 4
idle pp-idle-2 120
rec pp-qwen3-q8-metal --mode llama --model gguf/Qwen3-Embedding-0.6B-Q8_0.gguf --kind qwen3 --skip-queries --repeat 2
idle pp-idle-3 90
