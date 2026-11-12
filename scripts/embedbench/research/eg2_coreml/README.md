# EmbeddingGemma 2 conversion for Core ML

Convert the pinned Google checkpoint into a shared-weight Core ML package with
four fixed functions: `s128`, `s256`, `s512`, and `s1024`. The encoder uses FP16;
the final L2 normalization uses FP32. The output is the original normalized
768-dimensional embedding. Application head projection and Matryoshka truncation
stay outside this model.

This is standalone research tooling. It does not add a production embedder,
fallback, cache policy, or bucket-eviction policy to the application.

The [measured report](results/2026-11-12/REPORT.txt) and
[machine-readable summary](results/2026-11-12/summary.json) cover an M2 MacBook Air
with 16 GiB RAM and macOS 27.0.1:

| Check | Result |
|---|---|
| Fresh stock-HF FP32 parity | 12 fixtures passed; minimum cosine 0.99995254 |
| FiQA corpus | 1,491 documents passed; nine longer than 1024 excluded |
| Retained BF16 reference | Mean cosine 0.99997563; minimum 0.99993424 |
| Native inference, excluding tokenization | 58.94 documents/s; p50 7.80 ms; p95 35.01 ms |
| Four-function load | 120.37 s cold; 2.96 s cached; package compilation adds 2.30 s |
| Four recently active functions | 1,382 MiB wired neural memory, separate from ordinary footprint |
| Shared package size | 532.1 MiB; four standalone packages total 2.03 GiB |

All four Core ML compute plans prefer ANE for the transformer core. CPU work
includes token lookup and final normalization. A compute plan describes preferred
placement and estimated cost, not an Instruments runtime trace or ANE-only
execution. Shared weights on disk do not eliminate per-function neural memory.

## Reproduce

Run from this directory on Apple Silicon macOS. The conversion environment was
Python 3.12.9, Torch 2.7.0, Transformers 5.19.0, Core ML Tools 9.0, NumPy 2.2.6,
and safetensors 0.8.0. The complete dependency set is in `requirements.lock`.

```sh
cd scripts/embedbench/research/eg2_coreml
gzip -dc fixtures/tokens.json.gz > tokens.json
shasum -a 256 -c fixtures/SHA256SUMS
python3.12 -m venv .venv
.venv/bin/python -m pip install -r requirements.lock
.venv/bin/python fetch_stock.py
.venv/bin/python parity.py
.venv/bin/python convert.py --lengths 128
```

`fetch_stock.py` downloads the original safetensors, configuration and tokenizer
at revision `914f7f89142e33e77833254d9c9b90c3cef7303b`, then checks their SHA-256
hashes. The checkpoint download is about 1.49 GB. Allow space for the environment,
four intermediate packages, merged output and an optional clean rebuild.

`parity.py` compares the fixed-shape forward against the stock Transformers FP32
forward with strict weight loading. Its 12 fixtures cover padding, multilingual
text, long inputs that exercise the sliding window, and the retained normalization
regression. Conversion refuses a failed or stale parity gate; source and toolchain
fingerprints also prevent reuse of stale intermediate exports.

On the target Mac, build the native validator. The command below reproduces the
tested macOS 27 target with Swift 6.4.2; model packages target macOS 15 or later.

```sh
xcrun swiftc -parse-as-library -O -target arm64-apple-macos27.0 validate.swift -o validate
./validate "$PWD" s128 separate 128
.venv/bin/python convert.py --lengths 128 256 512 1024 --merge
./validate "$PWD" cold multi 128,256,512,1024
./validate "$PWD" warm multi 128,256,512,1024
```

Conversion and validation can run on different Macs. Copy `models/`,
`evidence/hf-reference.json`, `tokens.json`, and `validate.swift` to the target,
preserving the directory layout, and build the validator there. Run only one
validation process at a time. Native inference needs Swift/Core ML; it does not
need Python.

The validator writes into the ignored `evidence/` directory. It rejects nonfinite
or non-unit vectors, failed HF parity, missing function plans, and CPU placement
of convolution/matmul/linear/softmax core operations. The full run checks all
1,491 fitting FiQA documents, saves vectors, and records timing and
`footprint --wired` ledgers. Longer inputs are explicitly excluded, not truncated.

Use a fresh deployment directory when replacing a package. The harness reuses
an adjacent `.mlmodelc` by existence; stop its runner and remove only that model's
adjacent compiled directory before testing changed model bytes. This does not
define Core ML's internal specialization-cache invalidation rules.

For a clean rebuild into a new destination:

```sh
.venv/bin/python convert.py --output reproducibility-rebuild --lengths 128
.venv/bin/python compare_rebuild.py
.venv/bin/python measure_assets.py
```

The measured clean rebuild reproduced identical weights and an identical parsed
model graph. Protobuf map serialization order and package UUIDs varied, so
whole-package byte identity is not claimed.

## Input and output contract

- Choose the smallest fitting bucket after adding prompts and special tokens.
- `input_ids`: int32 `[1,L]`; BOS 2, EOS 1, right-padding token 0.
- `attention_mask`: float32 `[1,L]`; real tokens 1, padding 0.
- Document prompt: `title: none | text: `, or the supplied real title.
- Query prompt: `task: search result | query: `.
- Include prompt and special tokens in masked mean pooling.
- `embedding`: float32 `[1,768]`, L2-normalized.
- Route inputs longer than 1024 elsewhere; this package has no fallback runtime.

The first all-FP16 output experiment failed the unit-norm gate on FiQA document
52 (413 tokens): norm 0.99876078. Keeping final normalization in FP32 changed that
to 1.00000004, with cosine 0.99997802 against stock HF. This is fixture case 8 and
can be checked independently after exporting s512:

```sh
.venv/bin/python convert.py --lengths 512
./validate "$PWD" regression separate 512
```

## Evidence and references

The committed `results/2026-11-12/` contains measured summaries, fixture vectors,
all four compute plans, model hashes, provenance, and representative memory
ledgers. Private host names and local paths are replaced with portable labels.
The original converter and validator sources are byte-identical to the tested
handoff; see `publication-manifest.json`. Large model files, environments and the
full corpus output dump are excluded from Git.

`analyze.py` can compare a new `evidence/cold-vectors.json` with separately supplied
historical BF16, Nemosine and head-vector references. Those historical reference
files are not included here; their hashes and measured comparison are retained
in `results/2026-11-12/corpus-parity.json`. Fresh stock-HF fixture comparison does
not depend on them.

```sh
.venv/bin/python analyze.py --bf16 /path/to/eg2-bf16-cpu.docs.f32 \
  --nemosine /path/to/coreml-cold-vectors.json --head /path/to/eg2_head.f32
```

The fixed-shape forward adapts Transformers v5.19.0. Overflow-safe RMS/L2 formulas
come from [QVAC's published recipe](https://github.com/tetherto/qvac-fabric-llm.cpp/blob/2ed826df0c0eefc7135e9c3acc45aaa277f9b6ff/examples/embedding-coreml/README.md).
That exporter accepts external embeddings and stops at length 512. This version
accepts token IDs and preserves the original sliding-window mask at 1024.
Upstream source and licenses are retained under `upstream/`; see `NOTICE`.
[FilBona also publishes conversion scripts](https://huggingface.co/FilBona/embeddinggemma-2-coreml-ane/tree/cecc10efab079b842899591aeb73f44e0f895efc/conversion),
which were inspected but not reused.

Full-app 8 GiB safety with eviction/fallback and exact specialization-cache
invalidation remain unvalidated. No runtime integration is enabled by this tooling.
