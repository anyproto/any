# embedbench

Harnesses that measure the local embedder on Apple Silicon: llama.cpp
through `TestEmbedBench` (Metal and CPU), Core ML builds of EmbeddingGemma 2
on the Neural Engine, whole-system power without sudo, and the same runs on an
iPhone. They write vectors in the `TestEmbedBench` dump format (`uint32`
count, `uint32` dim, then little-endian `float32` rows), so `cos.py` compares
any two runs.

## Data dir

Everything reads and writes `$EMBEDBENCH_DIR`, default `<repo>/bench`
(gitignored):

- the GGUF models: `embeddinggemma-2-Q8_0.gguf`, `embeddinggemma-2-BF16.gguf`,
  `Qwen3-Embedding-0.6B-Q8_0.gguf`
- `fiqa-1500.jsonl`: one `{"text": …}` per line
- `coreml/.venv`: Python with `coremltools`, `numpy` and `tokenizers`
- `coreml/filbona/`: `EmbeddingGemma2.mlmodelc` and the EmbeddingGemma 2
  `tokenizer.json`
- `coreml/nemosine/EmbeddingGemma2TextANE.mlpackage`

Build `bin/indexer.test` (`go test -c -tags llamacpp -o bin/indexer.test
./internal/indexer`), `bin/any`, and the llama.cpp libs in `bin/llamacpp`
(`make llamacpp`).

## Mac (`mac/`)

- `run.sh <name> <model.gguf> <eg2|qwen3> <metal|cpu> [threads]`: one
  `TestEmbedBench` run. It samples the embedder child's `footprint`.
  `seq.sh` runs the whole matrix with cooldowns.
- `coreml_bench.py <model> <tokenizer.json> <corpus> <out> [flags]`: one
  doc per `predict` with the same prompts, plus a batched pass. With several
  `--buckets` each doc goes to the smallest fixed-length function that holds
  it. `coreml_seq.sh` holds the flags for each build.
- `cos.py <ref.docs.f32> <run.docs.f32> [run.truncated]`: row-wise cosine,
  split by truncated docs.
- `power_seq.sh`, then `power_analyze.py <data>/power`: whole-system power
  from the `AppleSmartBattery` accumulators. Run on AC with the battery at
  its charge limit. The gauge publishes about once a minute, so runs repeat
  the corpus to last at least 4 minutes.
- `memprobe.py <model.mlmodelc> <fn,fn,…>`: the process's Neural Engine
  memory after each load. On macOS that memory is excluded from
  `phys_footprint`; `footprint` lists it under "(neural) (nofootprint)".
- `chunktok.py`: token counts of the indexer's chunks. It ports the 2000-rune
  splitter from `internal/indexer/chunk.go`.

## iPhone (`ios/`)

One app runs Core ML (any compute unit, multifunction length buckets),
`MLComputePlan`, and llama.cpp from its release xcframework with the local
embedder's settings. Each run prints a `RESULT` JSON with a per-stage memory
trace (`phys_footprint`, the neural and graphics ledgers, the jetsam
headroom). Its vectors go to the app's `Documents/out/`.

1. Unzip the llama.cpp release's `llama-<build>-xcframework.zip` into
   `ios/llama/`.
2. Run `DEVELOPMENT_TEAM=<team> EMBEDBENCH_BUNDLE=<bundle id> xcodegen
   generate`, then `xcodebuild -project EmbedBench.xcodeproj -scheme
   EmbedBench -configuration Release -destination 'id=<udid>'
   -derivedDataPath build -allowProvisioningUpdates build`.
3. Install with `xcrun devicectl device install app`. A personal team must be
   trusted once on the phone: Settings → General → VPN & Device Management.
4. Run `prep.py <tokenizer.json> <corpus.jsonl> payload`. Compile Nemosine
   with `xcrun coremlcompiler compile <pkg> models --platform iOS
   --deployment-target 18.0`. Copy `payload/`, `models/*.mlmodelc` and
   `gguf/*.gguf` into the app container with `xcrun devicectl device copy to
   --domain-type appDataContainer`.
5. Set `IPHONE_UDID` and `EMBEDBENCH_BUNDLE`, then run `irun.sh <name>
   <args>` for one run or `iseq.sh` for the whole matrix. Keep the phone
   unlocked and plugged in. `analyze.py` prints the table and the cosines
   against the Mac's reference dumps.

Reinstalling the app drops its Neural Engine compile cache, so the next load
of a Core ML model compiles again.
