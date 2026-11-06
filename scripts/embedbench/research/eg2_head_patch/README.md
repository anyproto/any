# EG2 head patch (SYN-391)

This is a one-tensor raw-weight edit of EmbeddingGemma 2, with no training. It was found by the `eg2_patch` research agent, following the MIT "weight-space programming" method, and frozen on 2026-11-06 at 11:30, before any held-out scoring.

**The edit:** `output.weight` (the bias-free 512→768 head after mean pooling) becomes `W' = W − u(uᵀW)`.
- `u` is `head_mean.npy`: the unit mean direction of pooled dev doc outputs.
- So one shared direction is projected out of every query and doc vector. That removes about half of EG2's short-text bias without the code change that query centering needs.

**Rebuilding it** (any OS: numpy plus gguf-py from llama.cpp b11600 on `PYTHONPATH`):

    PYTHONPATH=<llama.cpp>/gguf-py python apply.py embeddinggemma-2-BF16.gguf embeddinggemma-2-Q8_0.gguf embeddinggemma-2-head-Q8_0.gguf

| file | sha256 |
|---|---|
| input BF16 (ggml-org) | 68bae29d62fb8c7d23e98d21fd4662753ddd636e6b62b8a70dfe059a9844f216 |
| input Q8_0 (ggml-org) | 2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135 |
| output Q8_0 | 17b7bf4dd431cb043b0cddd1c24c2c156de9b14e32f49c95f6c9f9fa87b27cb8 |

The output is byte-identical to the frozen artifact. Only `output.weight` differs from stock, and the header and metadata are unchanged.

**What doesn't change:** it loads in stock llama.cpp b11454 and b11600 (parity cosine ≥ 0.99998). Production settings stay as they are: doc prefix `title: none | text: `, query prefix `task: search result | query: `, mean pooling, 768 dims and context 2048.

**Re-embedding:** every document has to be re-embedded after the swap.
