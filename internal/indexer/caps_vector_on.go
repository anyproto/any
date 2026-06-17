//go:build vector && !gomobile && !mobile

package indexer

// capVector reports whether the vector (embedding + IVF-SQ ANN) leg is
// compiled in. It requires the `vector` build tag AND a non-gomobile,
// non-mobile build: vector/embedding is **always** off on mobile, no
// matter the tags or runtime config (docs/13-index.md § build tags). The
// llama.cpp purego/ffi bindings panic Android at package load, can't be
// cross-compiled into the iOS c-archive, and embedding has no place in
// the mobile runtime — so both gomobile (Android) and mobile (iOS)
// force-disable the whole leg. When false NewEmbedder always returns nil
// (FTS-only), the store never marks docs pending or creates a vector
// index, and the embed loop never runs.
const capVector = true
