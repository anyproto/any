package editor

import (
	"reflect"
	"strings"
	"testing"
)

func blk(id, typ, text string) Block {
	return Block{Id: id, Type: typ, Text: text}
}

func TestWindows_HeadingBoundaries(t *testing.T) {
	blocks := []Block{
		blk("h1", TypeHeading, "CRM sync setup"),
		blk("p1", TypeParagraph, "nightly job pulls contacts"),
		blk("p2", TypeParagraph, "runs at 02:00 UTC"),
		blk("h2", TypeHeading, "Deployment"),
		blk("p3", TypeParagraph, "canary first"),
	}
	wins := Windows(blocks)
	if len(wins) != 2 {
		t.Fatalf("got %d windows, want 2: %+v", len(wins), wins)
	}
	// First window: heading leads, paragraphs follow, anchored on heading.
	if wins[0].AnchorId != "h1" {
		t.Errorf("window 0 anchor = %q, want h1", wins[0].AnchorId)
	}
	if !strings.HasPrefix(wins[0].Text, "CRM sync setup\n") || !strings.Contains(wins[0].Text, "02:00 UTC") {
		t.Errorf("window 0 text not heading-led / missing members: %q", wins[0].Text)
	}
	if len(wins[0].BlockIds) != 3 {
		t.Errorf("window 0 members = %v, want 3", wins[0].BlockIds)
	}
	if wins[1].AnchorId != "h2" || !strings.HasPrefix(wins[1].Text, "Deployment\n") {
		t.Errorf("window 1 wrong: %+v", wins[1])
	}
}

func TestWindows_BudgetSplit(t *testing.T) {
	big := strings.Repeat("x", 900)
	// Two ~900-byte paragraphs under no heading exceed the 1500 budget
	// together, so they split into two windows.
	wins := Windows([]Block{
		blk("p1", TypeParagraph, big),
		blk("p2", TypeParagraph, big),
	})
	if len(wins) != 2 {
		t.Fatalf("budget split: got %d windows, want 2", len(wins))
	}
	if wins[0].AnchorId != "p1" || wins[1].AnchorId != "p2" {
		t.Errorf("budget-split anchors = %q,%q", wins[0].AnchorId, wins[1].AnchorId)
	}
}

func TestWindows_EmptyAndOverBudgetSingle(t *testing.T) {
	// Empty-text blocks (divider) produce no window.
	if wins := Windows([]Block{blk("d1", TypeDivider, "")}); len(wins) != 0 {
		t.Fatalf("empty block should yield no window, got %+v", wins)
	}
	// A single over-budget block is its own window (never dropped).
	huge := strings.Repeat("y", windowBudgetBytes+500)
	wins := Windows([]Block{blk("p1", TypeParagraph, huge)})
	if len(wins) != 1 || wins[0].AnchorId != "p1" {
		t.Fatalf("over-budget single block: %+v", wins)
	}
}

func TestWindows_NoLeadingHeading(t *testing.T) {
	// Body that starts without a heading still coalesces into one window
	// anchored on the first block.
	wins := Windows([]Block{
		blk("p1", TypeParagraph, "intro line"),
		blk("p2", TypeParagraph, "second line"),
	})
	if len(wins) != 1 || wins[0].AnchorId != "p1" {
		t.Fatalf("no-heading window: %+v", wins)
	}
	if wins[0].Text != "intro line\nsecond line" {
		t.Errorf("joined text = %q", wins[0].Text)
	}
}

// A window never holds headings alone when anything follows them.
func TestWindows_HeadingOnlyFolds(t *testing.T) {
	big := strings.Repeat("x", windowBudgetBytes+10)
	for _, c := range []struct {
		name   string
		blocks []Block
		want   [][]string // member block ids per window
	}{
		{"a heading run leads the text below it",
			[]Block{blk("h1", TypeHeading, "Games"), blk("h2", TypeHeading, "Handhelds"), blk("p1", TypeParagraph, "the list")},
			[][]string{{"h1", "h2", "p1"}}},
		{"a heading takes an over-budget block",
			[]Block{blk("h1", TypeHeading, "Log"), blk("p1", TypeParagraph, big), blk("p2", TypeParagraph, "tail")},
			[][]string{{"h1", "p1"}, {"p2"}}},
		{"an empty block between does not end the run",
			[]Block{blk("h1", TypeHeading, "A"), blk("d1", TypeDivider, ""), blk("h2", TypeHeading, "B"), blk("p1", TypeParagraph, "text")},
			[][]string{{"h1", "d1", "h2", "p1"}}},
		{"a heading after text still starts a window",
			[]Block{blk("h1", TypeHeading, "A"), blk("p1", TypeParagraph, "text"), blk("h2", TypeHeading, "B"), blk("p2", TypeParagraph, "more")},
			[][]string{{"h1", "p1"}, {"h2", "p2"}}},
		{"trailing headings stand alone",
			[]Block{blk("p1", TypeParagraph, "text"), blk("h1", TypeHeading, "A"), blk("h2", TypeHeading, "B")},
			[][]string{{"p1"}, {"h1", "h2"}}},
	} {
		wins := Windows(c.blocks)
		var got [][]string
		for _, w := range wins {
			got = append(got, w.BlockIds)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: windows %v, want %v", c.name, got, c.want)
		}
		if len(wins) > 0 && wins[0].AnchorId != c.blocks[0].Id {
			t.Errorf("%s: first anchor %q", c.name, wins[0].AnchorId)
		}
	}
	// The leading heading stays the window's title.
	if w := Windows([]Block{blk("h1", TypeHeading, "Games"), blk("h2", TypeHeading, "Handhelds"), blk("p1", TypeParagraph, "the list")}); w[0].Title != "Games" || w[0].Text != "Games\nHandhelds\nthe list" {
		t.Errorf("folded window: %+v", w[0])
	}
}
