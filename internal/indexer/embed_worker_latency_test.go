//go:build vector && !gomobile

package indexer

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Query latency through a real child while several "space workers"
// keep the doc queue saturated with chunk-sized frames. Gated like the
// other real-model tests; ANY_EVAL_LOCAL_GPU_LAYERS / _THREADS pick the
// child's mode and ANY_EVAL_LOAD_WORKERS (3) how many workers compete:
//
//	ANY_EVAL_LOCAL_MODEL=~/.any/models/<model>.gguf \
//	ANY_EVAL_LOCAL_LIBDIR=./bin/llamacpp ANY_EVAL_LOCAL_THREADS=16 \
//	go test -tags 'fts vector' -run TestWorkerEmbedder_RealChild_QueryLatency -v ./internal/indexer
func TestWorkerEmbedder_RealChild_QueryLatency(t *testing.T) {
	modelPath := os.Getenv("ANY_EVAL_LOCAL_MODEL")
	libDir := os.Getenv("ANY_EVAL_LOCAL_LIBDIR")
	if modelPath == "" || libDir == "" {
		t.Skip("set ANY_EVAL_LOCAL_MODEL + ANY_EVAL_LOCAL_LIBDIR")
	}
	workers, _ := strconv.Atoi(orEnv("ANY_EVAL_LOAD_WORKERS", "3"))
	mode := fmt.Sprintf("gpuLayers=%s threads=%s workers=%d",
		orEnv("ANY_EVAL_LOCAL_GPU_LAYERS", "0"), orEnv("ANY_EVAL_LOCAL_THREADS", "default"), workers)
	h := &helperSpawner{modes: []string{"real"}}
	w := newTestWorker(t, h, 0)
	w.spec.modelPath = modelPath
	w.spec.libDir = libDir
	w.startTimeout = 5 * time.Minute
	w.reqTimeout = 5 * time.Minute
	ctx := context.Background()

	// A chunk-sized doc (DefaultChunkRunes) of prose — what the embed
	// loop actually sends per frame.
	sentence := "The quick brown fox jumps over the lazy dog while the committee reviews the quarterly figures and the engineers argue about consensus protocols. "
	doc := []string{strings.Repeat(sentence, DefaultChunkRunes/len(sentence)+1)[:DefaultChunkRunes]}
	query := "how do distributed consensus protocols handle a partitioned quorum"

	// Warm up: load the model, measure one doc frame and an idle query.
	if _, err := w.EmbedDocs(ctx, doc); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := w.EmbedDocs(ctx, doc); err != nil {
		t.Fatal(err)
	}
	frame := time.Since(start)
	start = time.Now()
	if _, err := w.EmbedQuery(ctx, query); err != nil {
		t.Fatal(err)
	}
	idleQuery := time.Since(start)
	t.Logf("[%s] idle: one doc frame (%d runes) %s, one query %s", mode, DefaultChunkRunes, frame.Round(time.Millisecond), idleQuery.Round(time.Millisecond))

	// Saturate: workers looping single doc frames (what EmbedDocs does
	// per group), like several spaces backfilling at once.
	stop := make(chan struct{})
	var frames atomic.Int64
	var frameNanos atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				start := time.Now()
				if _, err := w.embed(ctx, workerRoleDoc, doc); err != nil {
					t.Error(err)
					return
				}
				frames.Add(1)
				frameNanos.Add(int64(time.Since(start)))
			}
		}()
	}
	time.Sleep(2 * time.Second) // let the queue fill
	const n = 20
	lat := make([]time.Duration, 0, n)
	var waitSum, decodeSum time.Duration
	loadStart := time.Now()
	framesAt, nanosAt := frames.Load(), frameNanos.Load()
	for i := 0; i < n; i++ {
		// The EmbedQuery path, split into its slot wait and its decode.
		start := time.Now()
		if err := w.slot.acquire(ctx, true); err != nil {
			t.Fatal(err)
		}
		waited := time.Since(start)
		r := w.serve(ctx, workerReq{Op: workerOpEmbed, Role: workerRoleQuery, Texts: []string{query}})
		w.slot.release(true)
		total := time.Since(start)
		if r.err != nil {
			t.Fatal(r.err)
		}
		lat = append(lat, total)
		waitSum += waited
		decodeSum += total - waited
		// Jittered, so the samples do not phase-lock to the frame rate.
		time.Sleep(300*time.Millisecond + time.Duration(rand.Int64N(int64(400*time.Millisecond))))
	}
	t.Logf("[%s] query split: slot wait mean %s, decode mean %s", mode,
		(waitSum / n).Round(time.Millisecond), (decodeSum / n).Round(time.Millisecond))
	loadDur := time.Since(loadStart)
	done := frames.Load() - framesAt
	// A worker's frame call spans its wait behind the other workers, so
	// the per-decode time is the call time divided by the competitors.
	frameUnderLoad := time.Duration(frameNanos.Load()-nanosAt) / time.Duration(max(done, 1)) / time.Duration(workers)
	docRate := float64(done) / loadDur.Seconds()
	close(stop)
	wg.Wait()

	slices.Sort(lat)
	pct := func(p float64) time.Duration { return lat[min(int(float64(len(lat))*p), len(lat)-1)] }
	var sum time.Duration
	for _, d := range lat {
		sum += d
	}
	t.Logf("[%s] under load (%.1f doc frames/s, %s per decode): query p50 %s p90 %s max %s mean %s over %d queries",
		mode, docRate, frameUnderLoad.Round(time.Millisecond), pct(0.5).Round(time.Millisecond), pct(0.9).Round(time.Millisecond),
		lat[len(lat)-1].Round(time.Millisecond), (sum / time.Duration(len(lat))).Round(time.Millisecond), n)
	fmt.Fprintf(os.Stderr, "LATENCY mode=[%s] idle_frame=%s idle_query=%s load_frame=%s docs_per_s=%.1f load_p50=%s load_p90=%s load_max=%s\n",
		mode, frame.Round(time.Millisecond), idleQuery.Round(time.Millisecond), frameUnderLoad.Round(time.Millisecond), docRate,
		pct(0.5).Round(time.Millisecond), pct(0.9).Round(time.Millisecond), lat[len(lat)-1].Round(time.Millisecond))
}

func orEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
