//go:build llamacpp && !android && !ios

package indexer

// llamaCppRelease pins the llama.cpp release whose shared libs the local
// embedder loads (docs/13-index.md § GPU offload). The Makefile, the
// build scripts and CI read it from this line. Bump together with the
// yzma dependency — yzma tracks llama.cpp releases, and only a matching
// pair works: too old a build fails to load ("undefined symbol"), too new
// a one loads but reads shifted struct fields (llama_model_n_embd comes
// back 0 and context creation aborts). yzma's README carries the
// compatibility table; verify a bump by embedding with the real model.
const llamaCppRelease = "b10620"
