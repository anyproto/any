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

### Energy (`ppseq.sh`, `ppanalyze.py`)

`ppseq.sh` records each run with Instruments' Power Profiler and Neural Engine
instruments: an idle baseline, then the three workloads, with idle gaps
between them. `ppanalyze.py` prints energy per doc above idle, for the whole
doc loop and for its last 100 s, once the phone has settled to its sustained
power level.

- Unplug the phone. Power Profiler reports 0 %/hr while it charges. The
  phone is then reached over Wi-Fi.
- `xctrace` launches the app itself. A system-wide (`--all-processes`)
  recording drops off Wi-Fi after about 1.6 s.
- Before each recording the script wakes the Wi-Fi tunnel with a `devicectl`
  call. Otherwise `xctrace` lists the phone as offline and times out
  "waiting for device to boot".
- System power is battery %/hr, sampled about once a second. Joules assume
  `WH` (default 16; the iPhone 17 Pro is 15.5 Wh with a SIM tray, 16.6 Wh
  eSIM-only).
- The per-process CPU and GPU "power impact" is unitless, and its GPU column
  stays at 0 for llama.cpp's Metal compute. The Neural Engine has no power
  column, only busy intervals.

## Research (`research/`)

The follow-up on why EG2 trails Qwen3: short-text bias and query-side
centering, heading-only windows, long chunks under a 512-token cap, and
token-splitter costs. See `research/README.md`.
