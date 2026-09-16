---
title: Embedders
description: The five index.embedder settings — auto, local, ollama, openai, none — their prerequisites, and what happens when one is unreachable.
order: 50
---
# Embedders

An embedder converts text into numeric vectors used by [semantic search](vector.html). The server embeds selected index documents in the background and embeds a query when it runs vector search.

Choose where that computation should happen. `local` runs a model on the device; `none` keeps search full-text only. Hosted or separately running model servers are optional choices. An unavailable embedder pauses vector indexing while full-text keeps working.

## The options

| `index.embedder` | What runs | Needs |
|---|---|---|
| `auto` | the configured OpenAI-compatible endpoint first, local model on failure; both must serve the same model | primary endpoint configuration and local runtime; the local model downloads unless supplied |
| `local` | llama.cpp in a child process on this device | model and llama.cpp libraries; Linux also needs system `libffi` |
| `ollama` | the configured Ollama server's `/api/embed` | running server; URL defaults to `http://localhost:11434`, model to `embeddinggemma` |
| `openai` | the configured OpenAI-compatible `/embeddings` endpoint | `index.openai.{baseUrl, model, apiKey}` |
| `none` | no embedder — the index is full-text only | — |

The standalone server's configuration default is `auto`. A host application or an explicit configuration can select another mode; the list of supported providers does not establish which one an application uses.

The selected embedder receives document text chosen for embedding and queries that need semantic search. `local` processes that text on the server's device. `openai` sends it to `index.openai.baseUrl`; `auto` tries that endpoint before local fallback; `ollama` sends it to its configured URL. Whether an endpoint is on-device or remote depends on that URL. Full-text-only properties and `mode: "fts"` queries do not need embedding.

```yaml
index:
  embedder: local
```

```bash
ANY_INDEX_EMBEDDER=none any run      # FTS-only server
```

## `local` — llama.cpp in a child process

Set `index.embedder: local` to keep embedding computation on the device. Install a desktop/server release with the matching `llamacpp/` library directory, or use `make build`. The server needs access to the model file; an absent model can download in the background as described below.


The default model is **Qwen3-Embedding-0.6B (Q8_0)**: 1024-dimensional, last-token pooling, L2-normalized vectors; queries carry the Qwen retrieval instruction, documents embed bare. The GGUF model file (639 MB, checked against a pinned SHA-256) downloads into `<data-dir>/models/` on first boot — resumable, with progress in the server log and as the `index.model_download` [process](../notifications/processes.html) — and never blocks boot. Until it lands the embedder reports unavailable and full-text keeps answering.

On the first embed call, the server starts its own binary as a hidden `any run embedder` child process. It sends text and receives vectors over stdin/stdout. One child serves every space, keeping model computation separate from the database server. The bindings are pure Go (no CGO); the child loads the prebuilt llama.cpp shared libraries at runtime from `index.local.libDir`, default `llamacpp/` next to the binary. `make build` fetches them; the release tarballs ship them. Model memory is allocated on the first call: approximately 640 MB mapped from the model file plus 200 MB of working context.

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

**Platform support.** Linux needs a loadable system `libffi.so.8` (on NixOS use the repo's `nix develop` shell); macOS bundles it, and the `-sandbox` release variants load the system one instead so they work inside an App-Sandboxed host (see [Builds and CI](../operations/builds-and-ci.html)). Mobile builds have full-text search and force vector search off; they do not construct an embedder, even when configuration names one.

## `auto` — online primary, local fallback

`auto` tries the configured primary endpoint and falls back to the local model on failure. After repeated failures, a circuit breaker temporarily skips the primary before trying it again. Local fallback still needs its model and runtime to be ready. A semantic query gives the primary half the remaining timeout budget so the local fallback has time to answer.

Both sides must be the **same embedding model** — the index holds one vector space and one dimension, and mixing models yields incoherent similarity. The supported pairing is one model served two ways: `index.openai.model: Qwen/Qwen3-Embedding-0.6B` on a host that serves it, with the default local Qwen3-Embedding-0.6B. fp16-versus-Q8 drift is negligible. The packaged `openai` defaults are shared development credentials, marked temporary.

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

`index.embedConcurrency` embeds several batches in parallel (default 4 for `openai`/`auto`, 1 for `local`, which serializes internally anyway). `index.embedBatch` (default 64) is the documents per request.

## Outage semantics

There is no boot-time probe. Whenever an embedder is *configured*, text-bearing documents are marked `pending` regardless of reachability, so:

- an outage — at boot or mid-run — freezes only the vector side; full-text indexes and answers normally;
- when the embedder returns, the next embed round (nudged by a page, or the 1-minute retry tick) drains the queue automatically;
- `hybrid` queries degrade to full-text with `vectorStatus: "unavailable"`; `vector` queries answer `503 index.embedder_unavailable`;
- the vector dimension is learned from the first successful batch (or `index.vector.dim`) and pinned in the index database — a later dimension change against a populated index is an error requiring a rebuild of the account's derived index. A model change also requires a rebuild; do not mix vectors from different models.

"Model still downloading" and "GPU died mid-run" both ride the same path: affected documents stay `pending`. A child that crashes with GPU offload active also moves the server to CPU decoding for the rest of the run; the next start tries the GPU again, so a driver fix recovers on its own.

To avoid an initial model download, set `index.local.modelPath` to an existing compatible GGUF and provide the runtime libraries. To run without any model, choose `index.embedder: none`; full-text search continues to work.
