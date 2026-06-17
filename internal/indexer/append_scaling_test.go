package indexer

import "testing"

// Append-fast-path verification for the coalescing-chunker design
// (chunker-hybrid-search-report § 4.1 strategy A, § 8 open question 1).
//
// The report claims strategy A ("RechunkObject returns the full current
// window set; the indexer re-emits only windows whose maxSeq > cursor")
// preserves the markdown/append O(N) fast-path that debug-log growth
// depends on. This test isolates the load-bearing cost — how many block
// records the chunker must READ per dirty event — and shows the claim is
// only half right.
//
// Today's per-block chunker reads via index.RecordsSince, which filters
// `_applySeq > cursor`: on an append it sees ONLY the new block(s), so N
// appends cost O(N) reads total. A coalescing chunker can't use that
// filter to build a window, because a window's text needs the window's
// OLDER members (applySeq <= cursor), which the filter hides.
//
// So the chunker must read those older blocks some other way. This test
// models the two ways:
//   - naive (the report's literal RechunkObject): re-read ALL blocks to
//     re-form windows. maxSeq>cursor bounds what is EMITTED/embedded to
//     O(window), but READS are O(doc) per append → O(N^2) total.
//   - windowed (NOT spelled out in the report): read back only to the
//     touched window's start (last heading) and forward to the tail →
//     O(window) reads per append → O(N) total.
//
// Verdict, asserted below: strategy A as written regresses the append
// path to O(N^2) reads; preserving O(N) requires the windowed read, which
// the report under-specifies. The emit/embed side (maxSeq>cursor) is fine
// either way — it's the READ side that bites.

type blk struct {
	applySeq  uint64
	isHeading bool
}

// chunkResult reports the work one ChunksSince-equivalent call did.
type chunkResult struct {
	blocksRead     int
	windowsEmitted int
}

// fragmentedIncremental is today's behavior: RecordsSince streams only
// blocks past the cursor; each becomes its own doc.
func fragmentedIncremental(doc []blk, cursor uint64) chunkResult {
	r := chunkResult{}
	for _, b := range doc {
		if b.applySeq > cursor {
			r.blocksRead++
			r.windowsEmitted++
		}
	}
	return r
}

// coalescedNaive is the report's strategy A taken literally: read every
// block to re-form the window set, emit windows whose maxSeq > cursor.
func coalescedNaive(doc []blk, cursor uint64) chunkResult {
	r := chunkResult{blocksRead: len(doc)} // full re-read to form windows
	for _, w := range windowsOf(doc) {
		if w.maxSeq > cursor {
			r.windowsEmitted++
		}
	}
	return r
}

// coalescedWindowed reads only the touched windows: from the start of the
// earliest touched window (last heading at/before the first changed
// block) to the end of the latest (next heading after the last changed
// block). Models a bounded range scan around the change — for an append
// that's the tail window; for a mid-insert, the one window it lands in.
func coalescedWindowed(doc []blk, cursor uint64) chunkResult {
	first, last := -1, -1
	for i, b := range doc {
		if b.applySeq > cursor {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return chunkResult{}
	}
	start := first
	for start > 0 && !doc[start].isHeading {
		start-- // walk back to the window boundary
	}
	end := last + 1
	for end < len(doc) && !doc[end].isHeading {
		end++ // extend to the next window boundary
	}
	span := doc[start:end]
	r := chunkResult{blocksRead: len(span)}
	for _, w := range windowsOf(span) {
		if w.maxSeq > cursor {
			r.windowsEmitted++
		}
	}
	return r
}

type win struct{ maxSeq uint64 }

// windowsOf groups blocks into windows that break before each heading
// (the report's primary boundary; a char budget would only split further,
// making windows smaller and the windowed read cheaper — so heading-only
// is the conservative worst case for the windowed variant).
func windowsOf(doc []blk) []win {
	var out []win
	var cur win
	started := false
	flush := func() {
		if started {
			out = append(out, cur)
			started = false
		}
	}
	for _, b := range doc {
		if b.isHeading {
			flush()
		}
		if !started {
			cur = win{}
			started = true
		}
		if b.applySeq > cur.maxSeq {
			cur.maxSeq = b.applySeq
		}
	}
	flush()
	return out
}

// simulateAppends grows a doc one block at a time (a heading every
// headingEvery blocks — the debug-log grow-by-append shape) and sums the
// blocks read across all appends, the way the advance loop would call the
// chunker once per dirty event with cursor = last indexed applySeq.
func simulateAppends(n, headingEvery int, chunk func([]blk, uint64) chunkResult) (totalReads, totalEmits int) {
	var doc []blk
	var seq, cursor uint64
	for i := range n {
		seq++
		doc = append(doc, blk{applySeq: seq, isHeading: i%headingEvery == 0})
		r := chunk(doc, cursor)
		totalReads += r.blocksRead
		totalEmits += r.windowsEmitted
		cursor = seq
	}
	return totalReads, totalEmits
}

func TestAppendFastPathScaling(t *testing.T) {
	const headingEvery = 8
	sizes := []int{100, 200, 400, 800}

	t.Logf("blocks read across N sequential appends (heading every %d):", headingEvery)
	t.Logf("%6s  %12s  %12s  %12s", "N", "fragmented", "coal-naive", "coal-windowed")
	reads := map[string][]int{}
	for _, n := range sizes {
		fr, _ := simulateAppends(n, headingEvery, fragmentedIncremental)
		nv, _ := simulateAppends(n, headingEvery, coalescedNaive)
		wd, _ := simulateAppends(n, headingEvery, coalescedWindowed)
		reads["frag"] = append(reads["frag"], fr)
		reads["naive"] = append(reads["naive"], nv)
		reads["windowed"] = append(reads["windowed"], wd)
		t.Logf("%6d  %12d  %12d  %12d", n, fr, nv, wd)
	}

	// Fragmented (today): exactly one block read per append — the O(N)
	// baseline that the append fast-path must preserve.
	for i, n := range sizes {
		if reads["frag"][i] != n {
			t.Errorf("fragmented reads at N=%d = %d, want %d (one per append)", n, reads["frag"][i], n)
		}
	}

	// Naive strategy A: reads scale ~quadratically. Doubling N must ~4x
	// the reads (sum_{i=1..N} i ≈ N^2/2). Assert the ratio is clearly
	// super-linear (> 3, well above the linear ratio of 2).
	for i := 1; i < len(sizes); i++ {
		ratio := float64(reads["naive"][i]) / float64(reads["naive"][i-1])
		if ratio < 3.0 {
			t.Errorf("coal-naive reads N=%d/N=%d ratio = %.2f, want ~4 (quadratic)", sizes[i], sizes[i-1], ratio)
		}
	}
	// And in absolute terms it dwarfs linear: at the largest N, naive
	// reads must exceed the fragmented baseline by more than the window
	// size (i.e. it is not merely a constant factor).
	last := len(sizes) - 1
	if reads["naive"][last] <= reads["frag"][last]*headingEvery {
		t.Errorf("coal-naive (%d) should dwarf O(N) baseline at N=%d, got <= %d",
			reads["naive"][last], sizes[last], reads["frag"][last]*headingEvery)
	}

	// Windowed: stays linear. Each append reads at most one window
	// (~headingEvery blocks), so total <= headingEvery * N with slack.
	for i, n := range sizes {
		if bound := headingEvery * n; reads["windowed"][i] > bound {
			t.Errorf("coal-windowed reads at N=%d = %d, want <= %d (O(window) per append)", n, reads["windowed"][i], bound)
		}
		// Doubling N should ~double windowed reads, not quadruple.
		if i > 0 {
			ratio := float64(reads["windowed"][i]) / float64(reads["windowed"][i-1])
			if ratio > 2.5 {
				t.Errorf("coal-windowed N=%d ratio = %.2f, want ~2 (linear)", n, ratio)
			}
		}
	}

	// Emit count is O(window) for BOTH coalescing variants — confirming
	// the maxSeq>cursor refinement bounds embedding work regardless of
	// read strategy (so the read side is the sole regression risk).
	_, naiveEmits := simulateAppends(sizes[last], headingEvery, coalescedNaive)
	_, windowedEmits := simulateAppends(sizes[last], headingEvery, coalescedWindowed)
	if naiveEmits != windowedEmits {
		t.Errorf("emit counts differ (naive=%d windowed=%d) — maxSeq>cursor should bound both equally", naiveEmits, windowedEmits)
	}
	t.Logf("windows emitted over %d appends (both variants): %d — embed work is O(N), independent of read strategy", sizes[last], naiveEmits)
}

// TestAppendFastPathMidInsert confirms a mid-document insert (not just a
// tail append) also touches one window under the windowed read — the
// other case the report flags. A single insert dirties exactly the window
// it lands in.
func TestAppendFastPathMidInsert(t *testing.T) {
	const headingEvery = 8
	// Build a 200-block doc fully indexed (cursor at the max seq).
	var doc []blk
	var seq uint64
	for i := range 200 {
		seq++
		doc = append(doc, blk{applySeq: seq, isHeading: i%headingEvery == 0})
	}
	cursor := seq

	// Insert one block in the middle (bump its applySeq past the cursor).
	seq++
	mid := 100
	doc = append(doc[:mid], append([]blk{{applySeq: seq}}, doc[mid:]...)...)

	naive := coalescedNaive(doc, cursor)
	windowed := coalescedWindowed(doc, cursor)
	t.Logf("mid-insert reads: naive=%d windowed=%d (doc=%d)", naive.blocksRead, windowed.blocksRead, len(doc))
	// Bounded by one window (the original headingEvery blocks plus the
	// inserted one), not by doc size — give a little slack.
	if windowed.blocksRead > 2*headingEvery {
		t.Errorf("windowed mid-insert read %d blocks, want ~one window (<= %d), not the doc (%d)", windowed.blocksRead, 2*headingEvery, len(doc))
	}
	if naive.blocksRead != len(doc) {
		t.Errorf("naive mid-insert read %d, want full doc %d", naive.blocksRead, len(doc))
	}
	if naive.windowsEmitted != 1 || windowed.windowsEmitted != 1 {
		t.Errorf("mid-insert should emit exactly 1 window, got naive=%d windowed=%d", naive.windowsEmitted, windowed.windowsEmitted)
	}
}
