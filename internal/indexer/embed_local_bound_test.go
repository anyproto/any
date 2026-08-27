//go:build vector && !gomobile

package indexer

import "testing"

// maxDocTokens is the real per-text bound: the KV cache partitions nCtx
// across batchDocs sequences, floored to a whole block so it never
// promises more than the slot llama actually reserves.
func TestLocalMaxDocTokens(t *testing.T) {
	for _, c := range []struct{ nCtx, batchDocs, want int }{
		{2048, 1, 2048},
		{2048, 2, 1024},
		{2048, 4, 512},
		{2048, 8, 256},
		{2048, 16, 256}, // 128 floors to 0, then to one 256-token block
		{2048, 64, 256},
		{2048, 5, 256}, // 409 floors to 256 — never promises the unrounded 409
		{2048, 3, 512}, // 682 floors to 512
		{512, 1, 512},
		{300, 1, 300}, // never exceeds nCtx
	} {
		l := &Local{nCtx: c.nCtx, batchDocs: c.batchDocs}
		if got := l.maxDocTokens(); got != c.want {
			t.Errorf("nCtx=%d batchDocs=%d: maxDocTokens=%d, want %d", c.nCtx, c.batchDocs, got, c.want)
		}
	}
}
