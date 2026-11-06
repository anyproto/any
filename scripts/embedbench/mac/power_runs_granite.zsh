# Granite Embedding 311M multilingual R2 (CLS, no prompts) with EG2 controls (ANE with the same
# buckets, and Metal), for power_seq.sh.
# Everything runs on llama.cpp b11600 or later: older builds allocate a logits buffer of
# n_vocab x n_ctx floats (262k x 2048 = 2.1 GB) for these 262k-vocab models. That build needs
# `any` and `indexer.test` built against yzma v1.30.0 or later, which mirrors the new
# llama_context_params.moe_cache_size. Set EMBEDBENCH_BIN and EMBEDBENCH_LIBDIR (see run.sh).
# The Core ML build (fp16, buckets 128-1024, CLS) is made by research/granite_ane/ from the HF weights.
: ${EMBEDBENCH_BIN:?} ${EMBEDBENCH_LIBDIR:?}
go_run granite-q8-metal    granite/granite-311m-mlr2-Q8_0.gguf granite metal fiqa-x5.jsonl
go_run granite-q8-cpu      granite/granite-311m-mlr2-Q8_0.gguf granite cpu   fiqa-x2.jsonl
ml_run granite-ane fiqa-x10.jsonl granite/ane/model/GraniteANE.mlmodelc granite/hf/tokenizer.json --units CPU_AND_NE --buckets 128,256,512,1024 --function-fmt s%d --mask-input attention_mask --mask-dtype float32 --doc-prefix '' --query-prefix ''
ml_run nemosine-ane4-ctl fiqa-x10.jsonl coreml/nemosine/EmbeddingGemma2TextANE.mlpackage $TOK --units CPU_AND_NE --buckets 128,256,512,1024 --function-fmt s%d --mask-input attention_mask --mask-dtype float32
go_run eg2-q8-metal-b11600 embeddinggemma-2-Q8_0.gguf          eg2     metal fiqa-x3.jsonl
