//go:build gomobile && fts

package indexer

import "testing"

// Android twin of caps_mobile_test.go (neither tag implies the other):
// the `gomobile fts` bind must have BM25 on and vector force-off — the
// latter is what keeps the llama.cpp ffi bindings (load-time ffi_prep_cif
// panic on Android) out of the build. Run:
// `go test -tags 'gomobile fts' ./internal/indexer`.
func TestCompiledCaps_GomobileFTS(t *testing.T) {
	fts, vector := CompiledCaps()
	if !fts {
		t.Error("capFTS must be true under -tags 'gomobile fts'")
	}
	if vector {
		t.Error("capVector must be false under -tags 'gomobile fts' (vector force-off on mobile)")
	}
}
