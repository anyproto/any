//go:build mobile && fts

package indexer

import "testing"

// TestCompiledCaps_MobileFTS pins the iOS embedded-build cap contract
// (IOS-6169 tag reconciliation): under `-tags 'mobile fts'` the binary
// links the BM25 full-text leg (capFTS == true) and force-disables the
// vector/embedding leg (capVector == false, via caps_vector_off.go's
// `mobile` term). This is the assertion behind Phase B's "# UNVERIFIED"
// caps gate — it only compiles under `-tags 'mobile fts'`, so run it with
// `go test -tags 'mobile fts' ./internal/indexer`.
func TestCompiledCaps_MobileFTS(t *testing.T) {
	fts, vector := CompiledCaps()
	if !fts {
		t.Error("capFTS must be true under -tags 'mobile fts'")
	}
	if vector {
		t.Error("capVector must be false under -tags 'mobile fts' (vector force-off on mobile)")
	}
}
