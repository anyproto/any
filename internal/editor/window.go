package editor

import "strings"

// windowBudgetBytes bounds a coalesced window's text. ~1.5 KB ≈ 256–512
// tokens — large enough to embed meaningfully (well under the local
// embedder's 2048-token truncation), small enough for a precise hit.
// chunker-hybrid-search-report § 4.1.
const windowBudgetBytes = 1500

// windowRecordPrefix prefixes a window's synthetic record id, anchored on
// its first member block: win_<firstBlockId>. The anchor keeps the id
// stable across edits/appends within the window; a full reconcile
// (prefix-delete + re-upsert) handles the cases where it shifts. Uses '_'
// (not ':') so the id stays colon-free, matching the docId convention.
const windowRecordPrefix = "win_"

// Window is one coalesced index unit: the concatenated text of a run of
// consecutive document-ordered blocks.
type Window struct {
	AnchorId string   // first member block id — the record-id anchor
	BlockIds []string // member block ids, in document order
	Text     string   // member texts joined by "\n", heading-led
}

// Windows groups document-ordered blocks (as returned by List — a
// depth-first tree walk, siblings by nav.pos) into coalesced windows. A
// new window starts before every heading and whenever appending the next
// block's text would exceed windowBudgetBytes. The heading (or the run's
// first block) leads its window — cheap context that also lifts BM25 term
// frequency for heading words.
//
// Blocks with empty text (dividers, images) still count as members for
// ordering but add no text; a window whose total text is empty is omitted
// (nothing to index).
func Windows(ordered []Block) []Window {
	var out []Window
	var cur Window
	started := false

	flush := func() {
		if started {
			if strings.TrimSpace(cur.Text) != "" {
				out = append(out, cur)
			}
			cur = Window{}
			started = false
		}
	}

	for _, b := range ordered {
		isHeading := b.Type == TypeHeading
		// Break before a heading, or when this block would overflow the
		// budget — but never on an empty window (a single over-budget
		// block becomes its own window).
		if started && (isHeading || len(cur.Text)+len(b.Text)+1 > windowBudgetBytes) {
			flush()
		}
		if !started {
			cur = Window{AnchorId: b.Id}
			started = true
		}
		cur.BlockIds = append(cur.BlockIds, b.Id)
		if b.Text != "" {
			if cur.Text != "" {
				cur.Text += "\n"
			}
			cur.Text += b.Text
		}
	}
	flush()
	return out
}
