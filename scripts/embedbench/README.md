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

- `run.sh <name> <model.gguf> <eg2|qwen3|granite> <metal|cpu> [threads]`: one
  `TestEmbedBench` run. It samples the embedder child's `footprint`.
  `seq.sh` runs the whole matrix with cooldowns. `EMBEDBENCH_LIBDIR` and
  `EMBEDBENCH_BIN` point it at other llama.cpp libs and at `any` and
  `indexer.test` built against a matching yzma.
- `coreml_bench.py <model> <tokenizer.json> <corpus> <out> [flags]`: one
  doc per `predict` with the same prompts, plus a batched pass. With several
  `--buckets` each doc goes to the smallest fixed-length function that holds
  it. `coreml_seq.sh` holds the flags for each build.
- `qwen3_coreml_bench.py <sidekick|single> <model> <tokenizer.json> <corpus> <out>
  [--buckets …]`: the same loop for the published Qwen3-Embedding-0.6B Core ML
  builds. `sidekick` is a dir of `model_<L>.{0,1}.mlmodelc` (two chained
  programs per bucket, fp16, right-padded) from
  `sidekickd/qwen3-embedding-0.6b-coreml`. `single` is a printf format with one
  package per bucket, left-padded, as in
  `erjigit17/qwen3-embedding-0.6b-ane-coreml-8bit` (128 and 512 only). In fp16
  every bucket holds its own ~1 GB weight copy; on an M2 Air three buckets
  (128–512, 2.6 GB) load, and a fourth crashes Core ML in `predict`.
- `cos.py <ref.docs.f32> <run.docs.f32> [run.truncated]`: row-wise cosine,
  split by truncated docs.
- `power_seq.sh [runs.zsh]`, then `power_analyze.py <power dir>`: whole-system
  power from the `AppleSmartBattery` accumulators. Run on AC with the battery
  at its charge limit, and leave the Mac alone: the idle baseline at the start
  and end must agree. The gauge publishes about once a minute, so runs repeat
  the corpus to last at least 4 minutes. The run lists are
  `power_runs_eg2.zsh` (the default) and `power_runs_granite.zsh`.
  `POWER_DIR` sets the output dir.
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

For Granite, `iseq_granite.sh` runs Granite on the ANE and llama.cpp, with
same-session EG2 controls. It needs:

- `prep.py <granite tokenizer.json> <corpus> payload-granite --no-prompts`;
- the `research/granite_ane` build compiled for iOS, as `models/GraniteANE.mlmodelc`;
- the Granite GGUF;
- an app built on llama.cpp b11600 or later. Older builds allocate a
  vocab × context logits buffer, about 2.1 GB for a 262k vocab.

For Qwen3 on the ANE, `iseq_qwen3.sh` runs the erjigit17 8-bit build and a
same-session llama.cpp Q8 Metal control. It needs `prep.py <qwen3
tokenizer.json> <corpus> payload-qwen3 --qwen3` (bare docs, the Instruct
query prefix) and the two packages compiled for iOS as
`models-qwen3/ej/s{128,512}.mlmodelc`. The app's `--model` takes `%d` for one
file per bucket, `--stage2` chains a second program per bucket, and
`--pad-left`, `--mask-int32`, `--pad-id` and `--eos` match the build's inputs.

### Energy (`ppseq.sh`, `ppanalyze.py`)

`ppseq.sh [runs.zsh]` records each run with Instruments' Power Profiler and
Neural Engine instruments: an idle baseline, then the workloads in the run
list (`pp_runs_eg2.zsh` by default, or `pp_runs_granite.zsh`), with idle gaps
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
token-splitter costs. See `research/README.md`. `research/granite_ane/`
converts Granite Embedding 311M multilingual R2 for the Neural Engine.
