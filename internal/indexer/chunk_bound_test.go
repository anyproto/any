package indexer

import (
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/index"
)

// A non-positive bound must not spin: breakPoint returns a zero-length
// cut and the split loop would never advance.
func TestSplitTextTinyBoundTerminates(t *testing.T) {
	done := make(chan []string, 1)
	go func() { done <- splitText("hello world of text", 0) }()
	select {
	case got := <-done:
		t.Logf("splitText(.., 0) = %q", got)
	case <-time.After(3 * time.Second):
		t.Fatal("splitText(.., 0) HANGS")
	}
}

// Same, reached through the title budget.
func TestExpandEntryTinyBoundTerminates(t *testing.T) {
	done := make(chan int, 1)
	go func() {
		done <- len(expandEntry(index.IndexEntry{
			ObjectId: "o", Dataset: "d", RecordId: "r",
			Title: "T", Data: strings.Repeat("word ", 50),
		}, 2))
	}()
	select {
	case n := <-done:
		t.Logf("expandEntry(.., 2) -> %d chunks", n)
	case <-time.After(3 * time.Second):
		t.Fatal("expandEntry(.., maxRunes=2) HANGS")
	}
}
