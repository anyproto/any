//go:build vector

package indexer

// capVector reports whether the vector (embedding + IVF-SQ ANN) leg is
// compiled in. Built when the `vector` build tag is set (see
// docs/13-index.md § build tags). When false NewEmbedder always returns
// nil (FTS-only), the store never marks docs pending or creates a vector
// index, and the embed loop never runs.
const capVector = true
