# The EG2 / Qwen3 / Core ML matrix for power_seq.sh (pinned llama.cpp libs).
go_run eg2-q8-metal   embeddinggemma-2-Q8_0.gguf     eg2   metal fiqa-x3.jsonl
go_run eg2-q8-cpu     embeddinggemma-2-Q8_0.gguf     eg2   cpu   fiqa-1500.jsonl
go_run qwen3-q8-metal Qwen3-Embedding-0.6B-Q8_0.gguf qwen3 metal fiqa-x2.jsonl
ml_run nemosine-ane fiqa-x10.jsonl coreml/nemosine/EmbeddingGemma2TextANE.mlpackage $TOK --units CPU_AND_NE --buckets 128,256,512,1024,2048 --function-fmt s%d --mask-input attention_mask --mask-dtype float32
ml_run filbona-ane  fiqa-x5.jsonl  coreml/filbona/EmbeddingGemma2.mlmodelc $TOK --units CPU_AND_NE --buckets 512
go_run eg2-q8-cpu-t4  embeddinggemma-2-Q8_0.gguf     eg2   cpu   fiqa-1500.jsonl 4
