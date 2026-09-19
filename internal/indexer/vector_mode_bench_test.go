package indexer

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/index"
)

// randUnitVecs returns n random L2-normalized dim-vectors.
func randUnitVecs(rng *rand.Rand, n, dim int) [][]float32 {
	out := make([][]float32, n)
	for i := range out {
		v := make([]float32, dim)
		var s float64
		for d := range v {
			v[d] = rng.Float32()*2 - 1
			s += float64(v[d]) * float64(v[d])
		}
		inv := float32(1 / math.Sqrt(s))
		for d := range v {
			v[d] *= inv
		}
		out[i] = v
	}
	return out
}

func applyVecs(t *testing.T, s *Store, vecs [][]float32, start, end int) {
	t.Helper()
	const batch = 256
	for off := start; off < end; off += batch {
		hi := min(off+batch, end)
		ups := make([]DocUpsert, hi-off)
		for j := off; j < hi; j++ {
			ups[j-off] = DocUpsert{
				Entry:  index.IndexEntry{Scope: "basic", ObjectId: "o", Dataset: "d", RecordId: fmt.Sprintf("r%d", j), Data: "x"},
				Vector: vecs[j],
			}
		}
		if err := s.Apply(context.Background(), "sp", ups, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// TestVectorModeIngestProfile compares the two ingest orderings per ANN
// mode — the difference cheggaaa flagged on the PR:
//   - BULK: insert all vectors, then EnsureVectorIndex once (a single
//     parallel build). What the old profile measured.
//   - INCREMENTAL: build the index on a 64-doc seed (as the embed loop
//     does after the first batch), then upsert the rest into the existing
//     index — serial per-doc graph maintenance. The PRODUCTION path.
//
// Gated: ANY_VEC_BENCH=1.
func TestVectorModeIngestProfile(t *testing.T) {
	if os.Getenv("ANY_VEC_BENCH") == "" {
		t.Skip("set ANY_VEC_BENCH=1")
	}
	const dim, seed = 1024, 64
	sizes := []int{5000, 20000, 50000}
	if v := os.Getenv("ANY_VEC_BENCH_SIZES"); v != "" {
		sizes = nil
		for _, s := range strings.Split(v, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
				sizes = append(sizes, n)
			}
		}
	}
	ctx := context.Background()
	rng := rand.New(rand.NewSource(1))

	t.Logf("%-7s %-8s  %12s  %18s", "N", "mode", "bulk", "incremental(prod)")
	for _, n := range sizes {
		vecs := randUnitVecs(rng, n, dim)
		for _, mode := range []string{"ivfsq", "btree"} {
			// BULK: insert all, build once.
			sb, err := OpenStore(ctx, t.TempDir()+"/i.db", dim, true)
			if err != nil {
				t.Fatal(err)
			}
			sb.SetVectorMode(mode)
			t0 := time.Now()
			applyVecs(t, sb, vecs, 0, n)
			if ok, err := sb.EnsureVectorIndex(ctx, "sp"); err != nil || !ok {
				t.Fatalf("bulk ensure %s: %v", mode, err)
			}
			bulk := time.Since(t0)
			_ = sb.Close()

			// INCREMENTAL: seed + build, then upsert the rest into the index.
			si, err := OpenStore(ctx, t.TempDir()+"/i.db", dim, true)
			if err != nil {
				t.Fatal(err)
			}
			si.SetVectorMode(mode)
			t1 := time.Now()
			applyVecs(t, si, vecs, 0, min(seed, n))
			if ok, err := si.EnsureVectorIndex(ctx, "sp"); err != nil || !ok {
				t.Fatalf("seed ensure %s: %v", mode, err)
			}
			applyVecs(t, si, vecs, min(seed, n), n) // into the existing index
			inc := time.Since(t1)
			_ = si.Close()

			t.Logf("%-7d %-8s  %12s  %18s", n, mode, bulk.Round(time.Millisecond), inc.Round(time.Millisecond))
		}
	}
}

// exactTopKIdx returns the recordIds ("r<i>") of the k nearest vectors to
// q by cosine (brute force) — the ground truth for recall@k.
func exactTopKIdx(q []float32, vecs [][]float32, k int) map[string]bool {
	type sc struct {
		i int
		s float32
	}
	scored := make([]sc, len(vecs))
	for i, v := range vecs {
		var dot float32
		for d := range q {
			dot += q[d] * v[d]
		}
		scored[i] = sc{i, dot}
	}
	sort.Slice(scored, func(a, b int) bool { return scored[a].s > scored[b].s })
	out := make(map[string]bool, k)
	for i := 0; i < k && i < len(scored); i++ {
		out[fmt.Sprintf("r%d", scored[i].i)] = true
	}
	return out
}

// dirSize sums the byte size of files in dir (index DB + WAL).
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// TestVectorModeProfile compares the build/insert/search cost of the ANN
// index modes at a few corpus sizes (random dim-1024 vectors). It answers
// "is HNSW's recall worth giving up IVF's cheaper build?" with numbers.
// Gated: ANY_VEC_BENCH=1.
//
//	ANY_VEC_BENCH=1 go test -run TestVectorModeProfile -v -timeout 30m ./internal/indexer
func TestVectorModeProfile(t *testing.T) {
	if os.Getenv("ANY_VEC_BENCH") == "" {
		t.Skip("set ANY_VEC_BENCH=1 (builds vector indexes at several sizes)")
	}
	const dim = 1024
	sizes := []int{1000, 10000, 50000}
	if v := os.Getenv("ANY_VEC_BENCH_SIZES"); v != "" {
		sizes = nil
		for _, s := range strings.Split(v, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
				sizes = append(sizes, n)
			}
		}
	}
	modes := []string{"ivfsq", "btree", "bruteforce"}
	ctx := context.Background()
	rng := rand.New(rand.NewSource(1))

	randVec := func() []float32 {
		v := make([]float32, dim)
		var s float64
		for i := range v {
			v[i] = rng.Float32()*2 - 1
			s += float64(v[i]) * float64(v[i])
		}
		inv := float32(1 / math.Sqrt(s))
		for i := range v {
			v[i] *= inv
		}
		return v
	}

	t.Logf("%-8s %-11s  %9s  %9s  %11s  %8s  %9s", "N", "mode", "insert", "build", "search/q", "recall", "diskMB")
	for _, n := range sizes {
		// Pre-generate vectors once per size (shared across modes).
		vecs := make([][]float32, n)
		for i := range vecs {
			vecs[i] = randVec()
		}
		queries := make([][]float32, 50)
		for i := range queries {
			queries[i] = randVec()
		}
		// Exact top-10 per query (ground truth for recall@10).
		const k = 10
		exact := make([]map[string]bool, len(queries))
		for qi, q := range queries {
			exact[qi] = exactTopKIdx(q, vecs, k)
		}

		for _, mode := range modes {
			dir := t.TempDir()
			s, err := OpenStore(ctx, dir+"/index.db", dim, true)
			if err != nil {
				t.Fatal(err)
			}
			s.SetVectorMode(mode)

			// Insert (docs + vectors) in batches — the cold-sync write path
			// after embeddings have landed.
			const batch = 256
			tIns := time.Now()
			for off := 0; off < n; off += batch {
				end := min(off+batch, n)
				ups := make([]DocUpsert, end-off)
				for j := off; j < end; j++ {
					ups[j-off] = DocUpsert{
						Entry:  index.IndexEntry{Scope: "basic", ObjectId: "o", Dataset: "d", RecordId: fmt.Sprintf("r%d", j), Data: "x"},
						Vector: vecs[j],
					}
				}
				if err := s.Apply(ctx, "sp", ups, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			insertDur := time.Since(tIns)

			// Build the index (one-shot over the inserted vectors).
			tB := time.Now()
			if ok, err := s.EnsureVectorIndex(ctx, "sp"); err != nil || !ok {
				t.Fatalf("ensure %s: %v (ok=%v)", mode, err, ok)
			}
			buildDur := time.Since(tB)

			// Search latency + recall@10 vs exact (NOTE: random vectors are
			// a worst case for recall — nearly equidistant in high dim; real
			// embeddings recall much higher. Use this for build/latency/disk
			// and the BEIR harness for real recall).
			tS := time.Now()
			var hits float64
			for qi, q := range queries {
				res, err := s.SearchVector(ctx, "sp", q, nil, k, 0)
				if err != nil {
					t.Fatal(err)
				}
				for _, h := range res {
					if exact[qi][h.RecordId] {
						hits++
					}
				}
			}
			searchPer := time.Since(tS) / time.Duration(len(queries))
			recall := hits / float64(len(queries)*k)

			diskMB := float64(dirSize(dir)) / (1 << 20)
			t.Logf("%-8d %-11s  %9s  %9s  %11s  %8.3f  %9.1f", n, mode,
				insertDur.Round(time.Millisecond), buildDur.Round(time.Millisecond),
				searchPer.Round(time.Microsecond), recall, diskMB)
			_ = s.Close()
		}
	}
}
