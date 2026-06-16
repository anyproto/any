//go:build fts

package indexer

// capFTS reports whether the BM25 full-text search leg is compiled in.
// Built when the `fts` build tag is set (see docs/13-index.md § build
// tags). When false the store never creates a fulltext index and
// SearchFTS short-circuits to no hits.
const capFTS = true
