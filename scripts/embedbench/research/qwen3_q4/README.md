# Published low-bit Qwen3-Embedding-0.6B builds vs production Q8_0

SYN-391. Do the published 4- and 5-bit builds of Qwen3-Embedding-0.6B keep Q8's retrieval quality? The aim is to cut
on-device model RAM and disk. Weight quantization doesn't change the any-store index size: IVF-SQ stores int8 per
dimension whatever the model's bit width.

## Arms

| arm | source | size | runs on |
|---|---|---|---|
| `q8` | `Qwen/Qwen3-Embedding-0.6B-GGUF` Q8_0 (production) | 639 MB | llama.cpp |
| `q4km` | `enacimie/Qwen3-Embedding-0.6B-Q4_K_M-GGUF` | 397 MB | llama.cpp |
| `q4km-imat` | `PeterAM4/Qwen3-Embedding-0.6B-GGUF` Q4_K_M-imat (imatrix calibrated on English finance text) | 396 MB | llama.cpp |
| `q5km` | `PeterAM4/Qwen3-Embedding-0.6B-GGUF` Q5_K_M (sha256 `57719bdd…59e1b2`) | 444 MB | llama.cpp |
| `dwq` | `mlx-community/Qwen3-Embedding-0.6B-4bit-DWQ` (group 64) | 335 MB | MLX only |

**How the arms run.** Every arm uses the same MLX Qwen3 graph as `eg2_patch/qwen.py`:
- Instruct query prefix, bare docs, last-token pooling, L2 norm, 2048 context.
- GGUF arms load their tensors dequantized with gguf-py, so the weights are exactly the quantized ones.
- **Parity on 200 FiQA docs, against llama.cpp b11600 Metal with the same file:** cos 0.99996 for Q4_K_M and 0.999998 for Q8.

**Scoring:** exact vector search, vector leg only, with a paired cluster bootstrap from `eg2_ru2/util2.ci`.

**Sets:**
- Russian:
  - `sealed-ru`: the round-2 dev-slang chat set;
  - `new-rig`: the 20 independent rig look-alikes;
  - `ru`: RU wiki paraphrase and EN→RU queries;
  - `miracl-ru-pool`.
- `miracl-de-pool`, `scifact`, `nfcorpus` and `wiki-mix`.
- `rig-known` and `rig-topical`: the cleared two-space export.

## Results (nDCG@10, Δ against Q8, * = 95% CI excludes 0)

| set (queries) | Q8 | Q4_K_M | Q4_K_M imat | Q5_K_M |
|---|---|---|---|---|
| sealed-ru (204) | 0.749 | −0.007 | −0.003 | −0.001 |
| new-rig (64) | 0.946 | −0.007 | −0.008 | −0.005 |
| ru (48) | 0.868 | **−0.058*** | **−0.043*** | −0.017 [−0.046, +0.003] |
| miracl-ru-pool (200) | 0.836 | **−0.010*** | 0.000 | +0.001 |
| miracl-de-pool (200) | 0.776 | −0.001 | −0.002 | −0.004 |
| scifact (300) | 0.701 | +0.004 | 0.000 | −0.002 |
| wiki-mix (65) | 0.838 | **−0.010*** | −0.013 | −0.011 |
| rig-known (105) | 0.963 | −0.002 | −0.012 | −0.001 |
| rig-topical (12) | 0.691 | −0.017 | −0.006 | +0.006 |
| mean doc cosine to Q8 | 1 | 0.975 | 0.983 | 0.992 |

**NFCorpus ties for all arms.** Its CIs are unreliable: 323 queries collapse into 6 relevance clusters.

The full tables, with MRR, are in `bench/qwen3-q4/RESULTS.md` and `results.json` (gitignored).

**Q4_K_M by query kind on `ru`, MRR:**
- RU paraphrase −0.069*;
- EN→RU −0.084*.

**Verdict:**
- **Q4_K_M costs Russian encyclopedic text.** The dev-slang chat cases hold.
- **imatrix calibration helps only partly.**
- **Q5_K_M has no significant loss on any set,** for 31% less model RAM and disk than Q8. It's the low-bit candidate for the rig.

## Core ML (ANE) builds on HF (2026-11-09)

There's no correct 4-bit build:
- **ewchampion's builds:** the `-coreml-{4,6,8,16}bit` repos all ship the same 1.98 GB `weight.bin` (same LFS oid).
- **Wballard's build:** a real 4-bit LUT palettization at seq 256, 298 MB, but it mean-pools. Qwen3-Embedding is a last-token model.
- **The rest:** tooktang, neuradex, NeoRoth and zacknisbet are fp16 at about 1.2 GB per bucket. erjigit17 8-bit (128/512) is the smallest correct build.

**ANE RAM is mostly one weight copy per sequence bucket.** On the M2, the 8-bit erjigit build maps 468 MB with one bucket and 892 MB with two. sidekick fp16 uses about 0.88 GB per bucket even though it hardlinks the weight files.

## Run

```sh
export PYTHONDONTWRITEBYTECODE=1
py=bench/mlx/.venv/bin/python   # mlx 0.32.4, mlx_lm, gguf, tokenizers
$py scripts/embedbench/research/qwen3_q4/q4_eval.py parity
for a in q8 q4km q4km-imat q5km dwq; do $py scripts/embedbench/research/qwen3_q4/q4_eval.py encode $a; done
$py scripts/embedbench/research/qwen3_q4/q4_eval.py score
```

Model files go under `bench/qwen3-q4/<hf repo>/`. The `new-rig` set needs `bench/eg2-ru2/FROZEN.json`, which round 2 wrote.
