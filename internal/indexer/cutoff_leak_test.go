//go:build fts && vector && !gomobile

package indexer

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/query"
)

type memSnap struct {
	goroutines int
	heapInuse  uint64
	totalAlloc uint64
}

func snapshotMem() memSnap {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return memSnap{goroutines: runtime.NumGoroutine(), heapInuse: ms.HeapInuse, totalAlloc: ms.TotalAlloc}
}

func mib(b uint64) string { return fmt.Sprintf("%.2f MiB", float64(b)/(1<<20)) }

// withDeadline fails the test if fn does not return within d — the
// guard for anything a leaked reader slot would block forever (a new
// read tx past MaxReaders, DB close). On timeout fn is abandoned, still
// running; the test is failing at that point anyway.
func withDeadline(t *testing.T, d time.Duration, what string, fn func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(d):
		t.Fatalf("%s: blocked for %s", what, d)
	}
}

// TestIteratorEarlyCloseNoLeak: closing an any-store iterator before it
// is exhausted — the lazy FTS leg's normal exit — must leave no
// goroutine, no retained heap, and no held reader slot behind, for the
// $text query with and without Limit and for $knn. It also logs what an
// open iterator pins (heap in use while open vs baseline) and what one
// query allocates, the memory side of the with/without-Limit question.
func TestIteratorEarlyCloseNoLeak(t *testing.T) {
	st, rng := cutoffStore(t, 5_000)
	ctx := context.Background()
	coll, err := st.spaceColl(ctx, cutoffSpace)
	if err != nil {
		t.Fatal(err)
	}
	qv := benchVec(rng, cutoffDim)
	text := query.Text{Search: "glacier"}
	knn := query.Key{Path: []string{"vector"}, Filter: query.NewKnn(qv, 1000)}
	openers := []struct {
		name string
		open func() (anystore.Iterator, error)
	}{
		{"fts/nolimit", func() (anystore.Iterator, error) { return coll.Find(text).Iter(ctx) }},
		{"fts/limit=30", func() (anystore.Iterator, error) { return coll.Find(text).Limit(30).Iter(ctx) }},
		{"knn/k=1000", func() (anystore.Iterator, error) { return coll.Find(knn).Iter(ctx) }},
	}
	const churn = 500
	for _, o := range openers {
		t.Run(o.name, func(t *testing.T) {
			base := snapshotMem()
			it, err := o.open()
			if err != nil {
				t.Fatal(err)
			}
			pullHits(t, it, 5) // closes
			it, err = o.open()
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5 && it.Next(); i++ {
				if _, err := it.Doc(); err != nil {
					t.Fatal(err)
				}
			}
			pinned := snapshotMem() // iterator still open
			if err := it.Close(); err != nil {
				t.Fatal(err)
			}
			before := snapshotMem()
			// A leaked reader slot blocks the open that exceeds
			// MaxReaders forever (any-store's slot wait has no ctx), so
			// the churn runs under a deadline to fail loudly instead.
			withDeadline(t, 60*time.Second, fmt.Sprintf("%d early-closed %s queries", churn, o.name), func() error {
				for i := 0; i < churn; i++ {
					it, err := o.open()
					if err != nil {
						return err
					}
					pullHits(t, it, 5)
				}
				return nil
			})
			after := snapshotMem()
			t.Logf("%s: goroutines %d → %d; heapInuse base %s, +%s while open, +%s after %d early-closed queries; alloc/query %s",
				o.name, base.goroutines, after.goroutines, mib(base.heapInuse),
				mib(pinned.heapInuse-min(pinned.heapInuse, base.heapInuse)),
				mib(after.heapInuse-min(after.heapInuse, base.heapInuse)), churn,
				mib((after.totalAlloc-before.totalAlloc)/churn))
			if after.goroutines != base.goroutines {
				t.Fatalf("goroutines %d → %d", base.goroutines, after.goroutines)
			}
			if after.heapInuse > base.heapInuse+(4<<20) {
				t.Fatalf("heap in use grew %s → %s after %d early-closed queries", mib(base.heapInuse), mib(after.heapInuse), churn)
			}
		})
	}

	// Reader slots: MaxReaders = max(NumCPU-1, 4) read txs may be open
	// at once; a leaked slot per early close would block the first open
	// past that count forever.
	opens := 2*runtime.NumCPU() + 16
	withDeadline(t, 20*time.Second, fmt.Sprintf("%d sequential early-closed opens", opens), func() error {
		for i := 0; i < opens; i++ {
			it, err := coll.Find(text).Iter(ctx)
			if err != nil {
				return err
			}
			if it.Next() {
				if _, err := it.Doc(); err != nil {
					return err
				}
			}
			if err := it.Close(); err != nil {
				return err
			}
		}
		return nil
	})
	// A writer and the DB close both wait on readers.
	withDeadline(t, 20*time.Second, "write after early closes", func() error {
		return st.Apply(ctx, cutoffSpace, benchUpserts(rng, 1<<20, 1), nil, nil)
	})
	withDeadline(t, 20*time.Second, "store close after early closes", st.Close)
}
