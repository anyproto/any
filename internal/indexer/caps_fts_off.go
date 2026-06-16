//go:build !fts

package indexer

// capFTS is false unless the `fts` build tag is set — the full-text
// search leg is compiled out (mobile/default builds). See caps_fts_on.go.
const capFTS = false
