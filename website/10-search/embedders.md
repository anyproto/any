---
title: Embedders
description: The five index.embedder settings — auto, local, ollama, openai, none — their prerequisites, and what happens when one is unreachable.
order: 50
---
# Embedders

An embedder turns index documents and queries into vectors for the [semantic leg](vector.html). `index.embedder` picks one; the default needs no configuration at all, and no embedder can ever break indexing or full-text search.

## The options

| `index.embedder` | What runs | Needs |
|---|---|---|
| `auto` (default) | an online OpenAI-compatible API as primary, the in-process local model as fallback — same model both ways | nothing; the local model auto-downloads regardless |
| `local` | llama.cpp in-process, no external service | the llama.cpp shared libraries next to the binary; a system `libffi` on Linux |
| `ollama` | a local Ollama server's `/api/embed` | Ollama running (default `http://localhost:11434`, model `embeddinggemma`) |
| `openai` | any OpenAI-compatible `/embeddings` endpoint | `index.openai.{baseUrl, model, apiKey}` |
| `none` | no embedder — the index is full-text only | — |

```yaml
index:
  embedder: local
```

```bash
ANY_INDEX_EMBEDDER=none any run      # FTS-only server
```

## `local` — in-process llama.cpp

The default model is **Qwen3-Embedding-0.6B (Q8_0)**: 1024-dimensional, last-token pooling, L2-normalized vectors; queries carry the Qwen retrieval instruction, documents embed bare. The GGUF (639 MB, sha256-pinned) downloads into `<data-dir>/models/` on first boot — resumable, with progress in the server log and as the `index.model_download` [process](../notifications/processes.html) — and never blocks boot. Until it lands the embedder reports unavailable and full-text keeps answering.

The bindings are pure Go (no CGO); they load the prebuilt llama.cpp shared libraries at runtime from `index.local.libDir`, default `llamacpp/` next to the binary. `make build` fetches them; the release tarballs ship them. Nothing is loaded into memory until the first embed call (~640 MB mmap + ~200 MB context once it is).

| Key | Default | Purpose |
|---|---|---|
| `local.modelPath` | "" | an existing GGUF; set it and no download is attempted (air-gapped) |
| `local.modelUrl` / `modelSha256` | "" | override the download source / checksum |
| `local.libDir` | `<exe-dir>/llamacpp` | where the shared libraries live |
| `local.contextSize` | 2048 | truncation bound in tokens |
| `local.queryPrefix` | "" | "" = the Qwen retrieval instruction |
| `local.dim` | 0 | Matryoshka truncation; 0 = the model's 1024 |
| `local.threads` | 0 | 0 = CPU count − 1 |
| `local.gpuLayers` | absent | absent = offload all when a GPU backend is usable; 0 = force CPU |
| `local.batchDocs` | 16 | documents packed per decode |

**GPU offload** is automatic: the bundles carry Metal (macOS arm64) and Vulkan (Linux, Windows — NVIDIA/AMD/Intel) backends alongside the CPU variants, and a backend whose driver is missing simply does not register. Full offload of the default model takes ~2 GB of VRAM; set `gpuLayers: 0` if the embedder should not have it. CUDA/ROCm builds are not bundled — point `libDir` at your own llama.cpp build to use them.

Platform notes: Linux needs a loadable system `libffi.so.8` (on NixOS use the repo's `nix develop` shell); macOS bundles it, and the `-sandbox` release variants load the system one instead so they work inside an App-Sandboxed host (see [Builds and CI](../operations/builds-and-ci.html)). Mobile builds never construct an embedder.

## `auto` — online primary, local fallback

`auto` prefers the online API for speed and falls back to the local model during an outage through a circuit breaker (repeated failures skip the primary for a cooldown, then re-probe), so vector search stays fresh instead of pausing.

> **Note.** Both sides must be the **same embedding model** — the index holds one vector space and one dimension, and mixing models yields incoherent similarity. The supported pairing is one model served two ways: `index.openai.model: Qwen/Qwen3-Embedding-0.6B` on a host that serves it, with the default local Qwen3-Embedding-0.6B. fp16-versus-Q8 drift is negligible. The packaged `openai` defaults are shared development credentials, marked temporary.

## `ollama` and `openai`

```yaml
index:
  embedder: ollama
  ollama:
    url: http://localhost:11434
    model: embeddinggemma
```

```yaml
index:
  embedder: openai
  openai:
    baseUrl: https://api.openai.com/v1
    model: text-embedding-3-small
    apiKey: sk-…            # sent as Bearer, never logged
```

`index.embedConcurrency` embeds several batches in parallel — the throughput win for online APIs (default 4 for `openai`/`auto`, 1 for `local`, which serializes internally anyway). `index.embedBatch` (default 64) is the documents per request.

## Outage semantics

There is no boot-time probe. Whenever an embedder is *configured*, text-bearing documents are marked `pending` regardless of reachability, so:

- an outage — at boot or mid-run — freezes only the vector side; full-text indexes and answers normally;
- when the embedder returns, the next embed round (nudged by a page, or the 1-minute retry tick) drains the queue automatically;
- `hybrid` queries degrade to full-text with `vectorStatus: "unavailable"`; `vector` queries answer `503 index.embedder_unavailable`;
- the vector dimension is learned from the first successful batch (or `index.vector.dim`) and pinned in the index database — a later dimension change against a populated index is a loud boot error advising `rm <data-dir>/index`, never silent corruption.

"Model still downloading" and "GPU died mid-run" both ride the same path: affected documents stay `pending`, and a restart re-selects backends cleanly.

> **Why it matters.** The embedder is the only component of search that might live outside the process. Making it optional, swappable and outage-tolerant keeps the encrypted data searchable on every device — including one that never installs a model or never goes online.
