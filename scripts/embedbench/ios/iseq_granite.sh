#!/bin/zsh
# iseq_granite.sh: Granite Embedding 311M multilingual R2 on the iPhone (Core ML on the ANE from
# research/granite_ane, and llama.cpp from an app built on b11600 or later), with same-session
# EG2 controls. Needs Documents/payload-granite, models/GraniteANE.mlmodelc and
# gguf/granite-311m-mlr2-Q8_0.gguf (see pp_runs_granite.zsh).
cd "${0:A:h}"
GR="--model models/GraniteANE.mlmodelc --function-fmt s%d --mask-input attention_mask --payload payload-granite"
NM="--model models/Nemosine.mlmodelc --function-fmt s%d --mask-input attention_mask"
G4=128,256,512,1024
cool() { ./irun.sh cool-$1 --mode idle --idle-sec ${2:-60} >/dev/null; }
r() { echo "$(date +%T) $1"; ./irun.sh "$@"; cool $1; }
r granite-ane       --mode coreml ${=GR} --units ane --buckets $G4 --batch 8
r granite-ane-3b    --mode coreml ${=GR} --units ane --buckets 128,256,512 --batch 1 --skip-queries
r plan-granite      --mode plan ${=GR} --units ane --buckets $G4
r granite-q8-metal  --mode llama --model gguf/granite-311m-mlr2-Q8_0.gguf --kind granite
r granite-q8-cpu    --mode llama --model gguf/granite-311m-mlr2-Q8_0.gguf --kind granite --cpu --max-docs 300 --skip-queries
r nemosine-ane4-ctl --mode coreml ${=NM} --units ane --buckets $G4 --batch 1 --skip-queries
r eg2-q8-metal-ctl  --mode llama --model gguf/embeddinggemma-2-Q8_0.gguf --kind eg2 --skip-queries
echo "$(date +%T) SEQ DONE"
