#!/bin/zsh
# iseq_qwen3.sh: Qwen3-Embedding-0.6B on the iPhone's ANE through a published Core ML build
# (erjigit17/qwen3-embedding-0.6b-ane-coreml-8bit: 8-bit weights, one package per bucket,
# 128 and 512 only), with a same-session llama.cpp Q8 Metal control. Needs
# Documents/payload-qwen3 (prep.py --qwen3 with Qwen3's tokenizer.json),
# models-qwen3/ej/s{128,512}.mlmodelc (coremlcompiler --platform iOS) and the Qwen3 GGUF.
# The fp16 builds are left out: about 1 GB of ANE memory per bucket.
cd "${0:A:h}"
EJ="--model models-qwen3/ej/s%d.mlmodelc --mask-input attention_mask --mask-int32 --pad-left --pad-id 151643 --eos 151643 --payload payload-qwen3"
cool() { ./irun.sh cool-$1 --mode idle --idle-sec ${2:-60} >/dev/null; }
r() { echo "$(date +%T) $1"; ./irun.sh "$@"; cool $1; }
r qwen3-ane-8bit    --mode coreml ${=EJ} --units ane --buckets 128,512 --batch 1
r plan-qwen3-8bit   --mode plan ${=EJ} --units ane --buckets 128,512
r qwen3-q8-metal-ctl --mode llama --model gguf/Qwen3-Embedding-0.6B-Q8_0.gguf --kind qwen3
echo "$(date +%T) SEQ DONE"
