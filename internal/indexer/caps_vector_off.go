//go:build !vector

package indexer

// capVector is false unless the `vector` build tag is set — the
// embedding/ANN leg is compiled out (mobile/default builds). See
// caps_vector_on.go.
const capVector = false
