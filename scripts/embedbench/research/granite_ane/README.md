# granite_ane

Granite Embedding 311M multilingual R2 (ModernBERT, CLS pooling, no prompts)
converted for the Neural Engine. It is a multifunction mlprogram with
fixed-length functions `s128`, `s256`, `s512` and `s1024`.

The interface is the iPhone app's and `mac/coreml_bench.py`'s:

- inputs: `input_ids` int32 `[1, L]` (pad id 0) and `attention_mask` float32 `[1, L]`;
- output: `embedding`, float32 `[1, 768]`, CLS-pooled and L2-normalised.

| script | does |
|---|---|
| `granite_ane.py` | Re-implements the model in torch for fixed lengths (see its docstring for the fp16 choices). |
| `check_torch.py <hf dir> <corpus.jsonl> [n]` | Compares the re-implementation with HF's `ModernBertModel` in fp32, and reports activation ranges. |
| `convert.py <hf dir> <out dir>` | Builds one fp16 function per bucket, then the multifunction package and its compiled `.mlmodelc`. |
| `validate.py <model.mlmodelc> <hf dir> <corpus.jsonl> [--plan]` | Core ML vectors against the HF fp32 reference, per bucket. `--plan` adds `MLComputePlan` placement. |
| `hf_ref.py <corpus.jsonl> <n> <out.docs.f32> [hf dir]` | HF fp32 reference vectors (sentence-transformers, CLS, 2048 tokens) in the `TestEmbedBench` dump format, for `mac/cos.py`. |
| `plan_fn.py <pkg> <fn,…>` | Placement for each function. `MLComputePlan` only reads the default function, so it re-saves the package with each one as default. |

Environment: coremltools 9.0, torch 2.7.0, transformers 4.57.3, tokenizers and
numpy. Run from this directory.

Why the upstream `rankun203/granite-embedding-311m-multilingual-r2-coreml`
wasn't used:

- its base model is fp32 with a fixed 512-token input;
- its `mixed-fp16` variant keeps add, softmax and layer_norm in fp32, which
  moves every residual add and LayerNorm off the ANE;
- it has only two lengths.

The fp16 changes:

- the residual stream peaks around 6.8k, so LayerNorm inputs are pre-scaled
  by 1/32 (`--ln-scale`), which keeps the squares inside fp16;
- masks use −1e4, not the dtype's minimum;
- the last layer computes only the CLS row;
- the pooled tail (final LayerNorm on the CLS row and the L2 norm) stays fp32
  on the CPU (`--precision tail32`). In fp16 the output norm drifted by ±0.001.
