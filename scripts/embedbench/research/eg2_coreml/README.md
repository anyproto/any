# Reproducible EG2 Core ML conversion

This directory converts the original pinned Google BF16 safetensors into a
text-only Core ML model with an FP16 encoder and FP32 final L2 normalization.
The normalized raw model output is 768-dimensional;
application-specific head removal and Matryoshka truncation remain downstream.

The owned pipeline adapts the stock Transformers text forward and the
overflow-safe RMS/L2 formulas from [QVAC's published EG2 export recipe](https://github.com/tetherto/qvac-fabric-llm.cpp/blob/2ed826df0c0eefc7135e9c3acc45aaa277f9b6ff/examples/embedding-coreml/README.md).
See `NOTICE` and preserved upstream licenses. It does not repackage a community
Core ML model. QVAC's original fixed lengths stop at 512 and accept embeddings;
this implementation accepts token IDs and preserves the stock sliding window
for 1024 and 2048. It follows [Apple's multifunction model recipe](https://apple.github.io/coremltools/docs-guides/source/multifunction-models.html).

## Measured release

The [November 16 report](results/2026-11-16/REPORT.txt) and
[summary](results/2026-11-16/evidence/summary.json) validate the exact five-function
artifact on an M2 Air with 16 GiB RAM and macOS 27.2.1. All 16 fresh stock-HF
fixtures and 1,499 fitting corpus documents passed; all five transformer cores
prefer ANE. The [model repository](https://huggingface.co/anylab/embeddinggemma-2-coreml)
is the distribution destination; use its immutable revision and per-file hashes.

The matched direct-runtime benchmark averaged 300 ms on ANE versus 435 ms on
Metal Q8 over 11 inputs and five measured rounds. Exact 2048-token medians were
300 versus 727 ms; exact 1025 tokens was essentially tied. This does not establish
an ANE speed advantage at every length or measure the production Go wrappers.

s2048 alone used 649 MiB wired neural memory; s128+s2048 used 932 MiB. Explicit
release removed the released function's wired allocation, while reclaimable
cache remained. These are separate from ordinary process footprint. Full-app
8 GiB safety and runtime idle-eviction behavior are not established by this run.

The [November 12 four-function baseline](results/2026-11-12/REPORT.txt) is retained
separately. This tooling does not enable a production application backend.

## Reproduce

Use Apple Silicon macOS, Python 3.12.9, and the exact dependency lock:

```sh
gzip -dc fixtures/tokens.json.gz > tokens.json
shasum -a 256 -c fixtures/SHA256SUMS
mkdir -p evidence
python3.12 -m venv .venv
.venv/bin/python -m pip install -r requirements.lock
.venv/bin/python fetch_stock.py
.venv/bin/python parity.py > evidence-torch-parity.log 2>&1
.venv/bin/python convert.py --lengths 128 256 512 1024 2048 --merge > evidence/convert-all.log 2>&1
```

`source-manifest.json` pins the source revision and SHA-256 of weights, config
and tokenizer. `parity.py` compares the export forward to the pinned stock HF
FP32 eager forward with strict tensor loading. It includes padded short text,
multilingual inputs, long inputs beyond the sliding radius, bucket boundaries,
the same short text padded to every bucket, and retained FiQA document 52
(413 tokens), which exposed final FP16 normalization error. Conversion refuses to start
unless this gate passes. The exact source files are verified again before
conversion. `tokens.json` is the existing FiQA/edge-case token fixture.

Validate every function of the merged package on the target Air:

```sh
xcrun swiftc -parse-as-library -O -target arm64-apple-macos27.0 validate.swift -o validate
./validate "$PWD" cold multi 128,256,512,1024,2048 > evidence/air-cold.log 2>&1
./validate "$PWD" warm multi 128,256,512,1024,2048 > evidence/air-warm.log 2>&1
```

Conversion can run on a separate Mac; copy `models/`, `evidence/hf-reference.json`,
`tokens.json`, and the native validator to the target. Keep one validation
process at a time. The compiled `.mlmodelc` is created beside the package.
The harness reuses that compiled directory by existence. When replacing a
package, use a fresh deployment directory (as this validation did), or stop
the runner and remove only that package's adjacent `.mlmodelc` before testing.
Do not reuse an old compiled artifact with changed model bytes.
The runner captures compile/load/inference timings, FP32-reference parity,
MLComputePlan device preferences, FiQA vectors and `footprint --wired` ledgers.
No cache flush, forced memory pressure or service restart is needed.

For the bounded clean-build reproduction check (fresh destination, no reused
model), run:

```sh
.venv/bin/python convert.py --output reproducibility-rebuild --lengths 2048 > evidence/rebuild-s2048.log 2>&1
.venv/bin/python compare_rebuild.py --length 2048
.venv/bin/python measure_assets.py
```

The specific final-normalization regression can be checked before the full
corpus without collecting compute plans:

```sh
.venv/bin/python convert.py --lengths 512
./validate "$PWD" regression separate 512
```

The retained document is fixture case 8, 413 tokens (FiQA document 52). The
native regression report includes its norm, cosine and maximum absolute error.

The parity gate and existing intermediate packages are bound to hashes of the
implementation/source manifest and exact relevant dependency versions. A changed
implementation requires a new parity run and fresh output directory. The clean
rebuild check distinguishes weight/model protobuf equality from random package
manifest identifiers. `evidence/asset-manifest.json` records output checksums,
input/output contracts, and physical shared weight offsets.

## Contract

- Functions: `s128`, `s256`, `s512`, `s1024`, `s2048`; choose the smallest fitting bucket.
- Inputs: int32 `input_ids[1,L]`, float32 `attention_mask[1,L]`.
- Prepend BOS 2, append EOS 1, right-pad with token 0; real-token mask 1, pad mask 0.
- Original prompts: `title: none | text: ` (or real title), `task: search result | query: `.
- Output: float32 `embedding[1,768]`, normalized; encoder weights/compute FP16,
  final normalization over 768 values FP32.
- Include prompt and special tokens in the masked mean.
- Reject/route inputs longer than 2048 elsewhere. This validator excludes and
  reports them; it never silently truncates.

`cpuAndNeuralEngine` allows CPU execution. MLComputePlan reports preferred
placement and estimated cost, not a runtime trace or ANE-only guarantee.
Disk weight sharing and runtime per-bucket neural memory are separate results.
The target test is M2 / 16 GiB; it does not establish an 8 GiB production memory policy.

The FP32 final L2 fix is retained from the validated four-bucket converter.
This extension uses fresh artifacts under `/tmp/eg2-owned-2048-20261116` locally
and on the Air; it preserves the earlier four-bucket evidence. The Air now runs
macOS 27.2.1 (26B131), and all five functions passed the fresh numerical and placement gates there;
consult REPORT.txt and the dated evidence for measured results. The earlier measurements were on macOS 27.0.1.

The stock FP32 gate additionally requires exact 2048-token text (including BOS
and EOS), actual text over 1024 tokens, long multilingual text, and short text
padded to 2048. The exact boundary fixture is generated by adding text tokens
until the full tokenization has exactly 2048 tokens, without truncating a real
document. All 1499 FiQA documents that fit passed the corpus validator; document 178 has 2212
tokens and is explicitly excluded. The prior retained BF16 corpus file is no longer available locally; fresh stock
HF fixtures cover the long bucket, and no new BF16 corpus comparison is claimed. The overlap comparisons use two explicitly named retained native references:
our November12 owned converter output (1491 documents) and Nemosine output
(1491 documents). Neither supplies reference outputs for the eight new documents.

## Delivered package provenance and timing

The November 16 validation uses the exact package assembled from the four
November 12 exports plus the new November 16 s2048 export. The source weights,
architecture implementation (`eg2.py`) and dependency versions are unchanged;
the parity fixtures and export fingerprints differ. `evidence/asset-manifest.json`
records each function's source package and provenance separately. A fresh run of
the commands above rebuilds all five with the current source fingerprint. Do not
reuse older intermediate packages under that fingerprint.

The matched benchmark uses 11 full prompted inputs of 1025–2048 tokens: eight
real FiQA documents, long multilingual text, and exact 1025/2048 text fixtures.
It compares Core ML FP16 encoder/FP32 L2 with stock GGUF Q8_0 on llama.cpp b11600
Metal; it is a deployed-path comparison with different precision and host APIs.
The Metal harness calls the encoder-only API directly (`llama_encode`) and
uses metadata-only memory clearing when a memory object exists. It records
whether such memory exists. This is a direct-runtime comparison, not the exact
production wrapper call path. Both timings start with token IDs and end with a copied normalized raw vector.
Model load/tokenization are excluded; input allocation/fill and synchronous
inference/output handling are included. No downstream head projection is used.
Run sequentially, after the main validator exits:

```sh
.venv/bin/python make_benchmark_inputs.py
xcrun swiftc -parse-as-library -O -target arm64-apple-macos27.0 runtime_bench.swift -o runtime-bench
./runtime-bench "$PWD" ane-warm > evidence/air-ane-warm.log 2>&1
GOWORK=off go -C metalbench build -o ../metalbench-run .
# Place pinned b11600 macOS-arm64 dylibs in benchassets/llamacpp-b11600.
# Place pinned Q8_0 GGUF in benchassets/embeddinggemma-2-Q8_0.gguf.
./metalbench-run "$PWD" > evidence/air-metal-warm.log 2>&1
.venv/bin/python analyze_bench.py
./runtime-bench "$PWD" memory-alone > evidence/air-memory-alone.log 2>&1
./runtime-bench "$PWD" memory-pair > evidence/air-memory-pair.log 2>&1
```

`evidence/benchmark-assets.json` pins the GGUF and every runtime library.
The official library release is https://github.com/ggml-org/llama.cpp/releases/tag/b11600;
extract its macOS-arm64 release archive into `benchassets/llamacpp-b11600`.
Download the exact GGUF without changing machine authentication:

```sh
mkdir -p benchassets
curl -fL https://huggingface.co/ggml-org/embeddinggemma-2-GGUF/resolve/bfcd298762cc34d0357ece5ebdd31791a3a374d8/embeddinggemma-2-Q8_0.gguf -o benchassets/embeddinggemma-2-Q8_0.gguf
shasum -a 256 benchassets/embeddinggemma-2-Q8_0.gguf
```

The stock GGUF revision is `bfcd298762cc34d0357ece5ebdd31791a3a374d8` at
`ggml-org/embeddinggemma-2-GGUF`; its Q8_0 file SHA-256 is
`2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135`.
The Go module pins yzma v1.30.0 and records transitive modules in go.sum.
The upstream ABI compatibility table requires v1.30.0+ for llama.cpp b11480+;
v1.29.1 is not used for the b11600 benchmark.
Memory runs snapshot `footprint --wired` with s2048 alone or s128+s2048,
explicitly release s2048, wait 30 seconds, and finally release all models.
They do not force memory pressure. Disk deduplication is measured separately.

`report.py` renders the dated November 16 experiment from its complete evidence set;
its hardware/date fields describe that recorded run. For a new machine, use the
validator JSON and record the actual machine and environment separately.

## Licences

The model and original converter code are Apache-2.0. Transformers and QVAC
attribution and upstream licences are preserved in [NOTICE](NOTICE) and
`upstream/`. The FiQA test fixture and covered reference texts have separate
CC-BY-SA-4.0 terms: retain the [dataset attribution](licenses/FIQA-NOTICE.txt)
and [licence](licenses/CC-BY-SA-4.0.txt) when redistributing them. The
[fixture source map](fixtures/source-map.json) identifies the source documents
and reference records. These notice additions do not alter converter code,
fixture bytes, historical fingerprints or validation results.
