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
| `auto` (default) | an online OpenAI-compatible API as primary, the local model as fallback — same model both ways; indexed text and search queries go to the online API | nothing; the local model auto-downloads regardless. A build without the local embedder runs the online API alone |
| `local` | llama.cpp in a child process of the server, no external service | a build with the local embedder (`make build`, a release tarball, or `go install -tags llamacpp`); the llama.cpp shared libraries next to the binary; a system `libffi` on Linux |
| `ollama` | a local Ollama server's `/api/embed` | Ollama running (default `http://localhost:11434`, model `embeddinggemma`) |
| `openai` | any OpenAI-compatible `/embeddings` endpoint | `index.openai.{baseUrl, model, apiKey}` |
| `none` | no embedder — the index is full-text only | — |

> **Note.** An embedder reads the text it embeds. `auto` and `openai` send the text of every indexed document and every search query to `index.openai.baseUrl`; `ollama` sends them to the Ollama server. `local` keeps them on the device.

```yaml
index:
  embedder: local
```

```bash
ANY_INDEX_EMBEDDER=none any run      # FTS-only server
```

## `local` — llama.cpp in a child process

The default model is **Qwen3-Embedding-0.6B (Q8_0)**: 1024-dimensional, last-token pooling, L2-normalized vectors; queries carry the Qwen retrieval instruction, documents embed bare. The GGUF (639 MB, sha256-pinned) downloads into `<data-dir>/models/` on first boot — resumable, with progress in the server log and as the `index.model_download` [process](../notifications/processes.html) — and never blocks boot. Until it lands the embedder reports unavailable and full-text keeps answering.

Decoding never runs inside the server. On the first embed call the server re-executes its own binary as a hidden `any run embedder` child and talks to it over stdin/stdout; one child serves every space. The bindings are pure Go (no CGO); the child loads the prebuilt llama.cpp shared libraries at runtime from `index.local.libDir`, default `llamacpp/` next to the binary. `make build` fetches them; the release tarballs ship them. Nothing is loaded into memory until that first call (~640 MB mmap + ~200 MB context once it is).

The child exists so a llama.cpp fault costs a round of embedding, not the server: a GPU device loss or an internal assertion kills only the child, the batch's documents stay `pending`, and the next round respawns it (with backoff). A frame that outlives `requestTimeout` counts as the same fault — a wedged GPU stops answering rather than failing. A search query takes the child ahead of queued document batches, so a busy re-index delays it by at most the decode in flight, and the child runs at lower scheduling priority (`niceness`) so a full re-index yields to interactive work.

| Key | Default | Purpose |
|---|---|---|
| `local.modelPath` | "" | an existing GGUF; set it and no download is attempted (air-gapped) |
| `local.modelUrl` / `modelSha256` | "" | override the download source / checksum |
| `local.libDir` | `<exe-dir>/llamacpp` | where the shared libraries live |
| `local.contextSize` | 2048 | truncation bound in tokens, shared by the texts of one decode |
| `local.queryPrefix` | "" | "" = the Qwen retrieval instruction |
| `local.dim` | 0 | Matryoshka truncation; 0 = the model's 1024 |
| `local.threads` | 0 | the child's CPU budget; 0 = CPU count − 1 |
| `local.gpuLayers` | absent | absent = offload all when a GPU backend is usable; 0 = force CPU |
| `local.batchDocs` | 1 | documents packed per decode; each then gets `contextSize / batchDocs` tokens, so a wider batch embeds less of each text |
| `local.requestTimeout` | `3m` | bound on one request to the child before it is killed and respawned |
| `local.niceness` | 10 | the child's scheduling priority below the server; 0 = same as the server |

**GPU offload** is automatic: the bundles carry Metal (macOS arm64) and Vulkan (Linux, Windows — NVIDIA/AMD/Intel) backends alongside the CPU variants, and a backend whose driver is missing simply does not register. Full offload of the default model takes ~2 GB of VRAM; set `gpuLayers: 0` if the embedder should not have it. CUDA/ROCm builds are not bundled — point `libDir` at your own llama.cpp build to use them.

Platform notes: only builds with `-tags llamacpp` carry the local embedder; without it `local` fails at boot ([Builds and CI](../operations/builds-and-ci.html)). Linux needs a loadable system `libffi.so.8` (on NixOS use the repo's `nix develop` shell); macOS bundles it, and the `-sandbox` release variants load the system one instead so they work inside an App-Sandboxed host (see [Builds and CI](../operations/builds-and-ci.html)). Mobile builds never construct an embedder.

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

"Model still downloading" and "GPU died mid-run" both ride the same path: affected documents stay `pending`. A child that crashes with GPU offload active also moves the server to CPU decoding for the rest of the run; the next start tries the GPU again, so a driver fix recovers on its own.

> **Why it matters.** The embedder is the only component of search that might live outside the process. Making it optional, swappable and outage-tolerant keeps the encrypted data searchable on every device — including one that never installs a model or never goes online.
