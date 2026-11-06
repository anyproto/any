# Granite Embedding 311M multilingual R2 on the ANE (research/granite_ane) and on Metal,
# then EG2 controls on the same paths, for ppseq.sh. Needs Documents/payload-granite
# (prep.py --no-prompts with Granite's tokenizer.json), models/GraniteANE.mlmodelc
# and gguf/granite-311m-mlr2-Q8_0.gguf, and an app built on llama.cpp b11600 or later.
GR=(--model models/GraniteANE.mlmodelc --function-fmt s%d --mask-input attention_mask --payload payload-granite)
NM=(--model models/Nemosine.mlmodelc --function-fmt s%d --mask-input attention_mask)
idle pp-idle-0 90
rec pp-granite-ane --mode coreml $GR --units ane --buckets 128,256,512,1024 --batch 1 --skip-queries --repeat 10
idle pp-idle-1 120
rec pp-granite-q8-metal --mode llama --model gguf/granite-311m-mlr2-Q8_0.gguf --kind granite --skip-queries --repeat 6
idle pp-idle-2 120
rec pp-nemosine4 --mode coreml $NM --units ane --buckets 128,256,512,1024 --batch 1 --skip-queries --repeat 10
idle pp-idle-3 120
rec pp-eg2-q8-metal --mode llama --model gguf/embeddinggemma-2-Q8_0.gguf --kind eg2 --skip-queries --repeat 4
idle pp-idle-4 90
