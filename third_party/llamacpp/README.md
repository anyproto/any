# llama.cpp prebuilt libraries

`make llamacpp` (also run as a failure-tolerant step of `make build`)
downloads the official prebuilt
[llama.cpp](https://github.com/ggml-org/llama.cpp) release tarball for
the host platform into `cache/` here and extracts the shared libraries
into `bin/llamacpp/`, where the built-in local embedder
(`index.embedder: local`, see `docs/13-index.md`) dlopens them at
runtime via [yzma](https://github.com/hybridgroup/yzma).

The release tag is pinned by `LLAMACPP_VERSION` in the `Makefile`; bump
it together with the yzma dependency, which tracks llama.cpp releases.

Supported platforms: macOS arm64 (Metal) and Linux amd64 (CPU — the
tarball ships per-CPU-variant backends; the loader picks the best one
for the host).

## Licenses

- **llama.cpp** — MIT. The upstream `LICENSE` file is copied next to
  the extracted libraries (`bin/llamacpp/LICENSE`).
- **yzma** (Go bindings) — Apache-2.0, vendored as a Go module.
- **Qwen3-Embedding-0.6B** (default model, downloaded at runtime into
  `<data-dir>/index/models/`) — Apache-2.0.
