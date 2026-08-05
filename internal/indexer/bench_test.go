//go:build fts && vector && !gomobile && !mobile

package indexer

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/anyproto/any/internal/index"
)

// Benchmarks that informed the Options defaults (BatchLimit, EmbedBatch)
// — see docs/13-index.md § tuning. File-backed stores: tx-size tuning on
// an in-memory DB would be meaningless.

func benchStore(b *testing.B, dim int) *Store {
	b.Helper()
	s, err := OpenStore(context.Background(), filepath.Join(b.TempDir(), "index.db"), dim, dim > 0)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s
}

var benchWords = []string{
	"glacier", "tidal", "sourdough", "orbital", "quarterly", "budget",
	"zeppelin", "harvest", "primer", "notes", "review", "pipeline",
	"asynchronous", "worker", "cursor", "tombstone", "lexical", "vector",
}

func benchText(rng *rand.Rand, words int) string {
	out := ""
	for i := 0; i < words; i++ {
		if i > 0 {
			out += " "
		}
		out += benchWords[rng.Intn(len(benchWords))]
	}
	return out
}

func benchUpserts(rng *rand.Rand, start, n int) []DocUpsert {
	ups := make([]DocUpsert, n)
	for i := range ups {
		ups[i] = DocUpsert{Entry: index.IndexEntry{
			Scope:    "chat",
			ObjectId: fmt.Sprintf("obj%d", (start+i)%64),
			Dataset:  "chat_messages",
			RecordId: fmt.Sprintf("r%08d", start+i),
			Data:     benchText(rng, 12),
			ApplySeq:   uint64(start + i + 1),
		}}
	}
	return ups
}

// BenchmarkApplyTxSize: one Apply (one write tx) per op, varying docs
// per tx. Per-doc cost is timePerOp / batch — FTS postings flush once
// per tx, so bigger batches amortize it.
func BenchmarkApplyTxSize(b *testing.B) {
	for _, batch := range []int{16, 64, 256, 1024} {
		b.Run(fmt.Sprintf("batch=%d", batch), func(b *testing.B) {
			s := benchStore(b, 0)
			rng := rand.New(rand.NewSource(1))
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.Apply(ctx, "sp", benchUpserts(rng, i*batch, batch), nil, nil); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.N*batch)/b.Elapsed().Seconds(), "docs/s")
		})
	}
}

func benchVec(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = rng.Float32()*2 - 1
	}
	return v
}

// BenchmarkSetVectorsBatch: one SetVectors tx per op at dim 768 over an
// existing IVF-SQ index — the steady-state vector-insert path.
func BenchmarkSetVectorsBatch(b *testing.B) {
	const dim = 768
	for _, batch := range []int{16, 64, 256} {
		b.Run(fmt.Sprintf("batch=%d", batch), func(b *testing.B) {
			s := benchStore(b, dim)
			rng := rand.New(rand.NewSource(1))
			ctx := context.Background()
			// Seed pending docs for every iteration up front, then
			// create the index from a first small embedded set.
			total := (b.N + 1) * batch
			const seedChunk = 1024
			for off := 0; off < total; off += seedChunk {
				n := seedChunk
				if total-off < n {
					n = total - off
				}
				if err := s.Apply(ctx, "sp", benchUpserts(rng, off, n), nil, nil); err != nil {
					b.Fatal(err)
				}
			}
			ids, _, err := s.Pending(ctx, "sp", total)
			if err != nil || len(ids) < total {
				b.Fatalf("pending seed: %v (%d/%d)", err, len(ids), total)
			}
			// First batch trains the IVF index (excluded from timing).
			vecs := make([][]float32, batch)
			for i := range vecs {
				vecs[i] = benchVec(rng, dim)
			}
			if err := s.SetVectors(ctx, "sp", ids[:batch], vecs); err != nil {
				b.Fatal(err)
			}
			if ok, err := s.EnsureVectorIndex(ctx, "sp"); err != nil || !ok {
				b.Fatalf("ensure vector index: %v", err)
			}
			ids = ids[batch:]
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				chunk := ids[i*batch : (i+1)*batch]
				for j := range vecs {
					vecs[j] = benchVec(rng, dim)
				}
				if err := s.SetVectors(ctx, "sp", chunk, vecs); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.N*batch)/b.Elapsed().Seconds(), "vecs/s")
		})
	}
}

// BenchmarkSearch: FTS and vector lookups against a 10k-doc space
// (vector at dim 768, IVF-SQ).
func BenchmarkSearch(b *testing.B) {
	const dim = 768
	const docs = 10_000
	s := benchStore(b, dim)
	rng := rand.New(rand.NewSource(1))
	ctx := context.Background()
	for off := 0; off < docs; off += 1024 {
		n := 1024
		if docs-off < n {
			n = docs - off
		}
		if err := s.Apply(ctx, "sp", benchUpserts(rng, off, n), nil, nil); err != nil {
			b.Fatal(err)
		}
	}
	ids, _, err := s.Pending(ctx, "sp", docs)
	if err != nil {
		b.Fatal(err)
	}
	for off := 0; off < len(ids); off += 256 {
		end := off + 256
		if end > len(ids) {
			end = len(ids)
		}
		vecs := make([][]float32, end-off)
		for i := range vecs {
			vecs[i] = benchVec(rng, dim)
		}
		if err := s.SetVectors(ctx, "sp", ids[off:end], vecs); err != nil {
			b.Fatal(err)
		}
		if off == 0 {
			if ok, err := s.EnsureVectorIndex(ctx, "sp"); err != nil || !ok {
				b.Fatalf("ensure vector index: %v", err)
			}
		}
	}

	b.Run("fts", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := s.SearchFTS(ctx, "sp", "glacier tidal", nil, 30); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("vector", func(b *testing.B) {
		qv := benchVec(rng, dim)
		for i := 0; i < b.N; i++ {
			if _, err := s.SearchVector(ctx, "sp", qv, nil, 30, 0); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkOllamaEmbedBatch measures real embedding throughput per batch
// size against a local Ollama (the EmbedBatch dial). Gated:
// ANY_BENCH_OLLAMA=1.
func BenchmarkOllamaEmbedBatch(b *testing.B) {
	if os.Getenv("ANY_BENCH_OLLAMA") == "" {
		b.Skip("set ANY_BENCH_OLLAMA=1 (needs local ollama + embeddinggemma)")
	}
	c := NewOllama("", "")
	rng := rand.New(rand.NewSource(1))
	ctx := context.Background()
	for _, batch := range []int{8, 32, 64, 128} {
		b.Run(fmt.Sprintf("batch=%d", batch), func(b *testing.B) {
			texts := make([]string, batch)
			for i := range texts {
				texts[i] = benchText(rng, 24)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.EmbedDocs(ctx, texts); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.N*batch)/b.Elapsed().Seconds(), "texts/s")
		})
	}
}
