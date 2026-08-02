package markdown

import (
	"errors"
	"testing"
)

func TestApplyEdits_SingleExact(t *testing.T) {
	content := "# Title\n\n- [ ] buy milk\n\n- [ ] walk dog"
	got, err := applyEdits(content, []Edit{
		{OldText: "- [ ] buy milk", NewText: "- [x] buy milk"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "# Title\n\n- [x] buy milk\n\n- [ ] walk dog"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyEdits_MultipleEditsMatchOriginal(t *testing.T) {
	// Both edits resolve against the ORIGINAL content — the first
	// replacement must not shift the second's match.
	content := "alpha\n\nbeta\n\ngamma"
	got, err := applyEdits(content, []Edit{
		{OldText: "gamma", NewText: "GAMMA"},
		{OldText: "alpha", NewText: "a much longer replacement"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "a much longer replacement\n\nbeta\n\nGAMMA"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyEdits_EmptyNewTextDeletes(t *testing.T) {
	content := "keep\n\ndrop me\n\nkeep too"
	got, err := applyEdits(content, []Edit{
		{OldText: "\n\ndrop me", NewText: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "keep\n\nkeep too"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyEdits_NoMatch(t *testing.T) {
	_, err := applyEdits("some content", []Edit{
		{OldText: "not there", NewText: "x"},
	})
	var nm NoMatchError
	if !errors.As(err, &nm) || nm.Index != 0 {
		t.Fatalf("want NoMatchError{0}, got %v", err)
	}
}

func TestApplyEdits_Ambiguous(t *testing.T) {
	_, err := applyEdits("dup\n\ndup", []Edit{
		{OldText: "dup", NewText: "x"},
	})
	var am AmbiguousMatchError
	if !errors.As(err, &am) || am.Index != 0 || am.Occurrences != 2 {
		t.Fatalf("want AmbiguousMatchError{0, 2}, got %v", err)
	}
}

func TestApplyEdits_ReplaceAll(t *testing.T) {
	got, err := applyEdits("dup one\n\ndup two\n\nother", []Edit{
		{OldText: "dup", NewText: "DUP", ReplaceAll: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "DUP one\n\nDUP two\n\nother"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyEdits_Overlap(t *testing.T) {
	_, err := applyEdits("one two three", []Edit{
		{OldText: "one two", NewText: "x"},
		{OldText: "two three", NewText: "y"},
	})
	var ov OverlapError
	if !errors.As(err, &ov) {
		t.Fatalf("want OverlapError, got %v", err)
	}
	if ov.IndexA != 0 || ov.IndexB != 1 {
		t.Errorf("want indices {0,1}, got {%d,%d}", ov.IndexA, ov.IndexB)
	}
}

func TestApplyEdits_EmptyOldText(t *testing.T) {
	_, err := applyEdits("content", []Edit{{OldText: "", NewText: "x"}})
	if err == nil {
		t.Fatal("want error for empty oldText")
	}
}

func TestApplyEdits_FuzzyUnicodePunctuation(t *testing.T) {
	// Content has ASCII quotes/dashes; the caller quotes with curly
	// quotes and an em dash. Exact match misses, fuzzy whole-line
	// rescues.
	content := "He said \"hello\" - loudly\n\nother line"
	got, err := applyEdits(content, []Edit{
		{OldText: "He said “hello” — loudly", NewText: "replaced"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "replaced\n\nother line"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyEdits_FuzzyTrailingWhitespace(t *testing.T) {
	content := "line one\n\nline two"
	got, err := applyEdits(content, []Edit{
		{OldText: "line one   ", NewText: "line 1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "line 1\n\nline two"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyEdits_FuzzyMultiline(t *testing.T) {
	content := "aaa\n\nbbb\n\nccc"
	got, err := applyEdits(content, []Edit{
		{OldText: "aaa\n\nbbb  ", NewText: "merged"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "merged\n\nccc"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyEdits_FuzzyPartialLineDoesNotMatch(t *testing.T) {
	// Fuzzy is whole-line only: a mid-line fragment that misses the
	// exact scan must not silently replace the whole line.
	_, err := applyEdits("full line with “curly” inside", []Edit{
		{OldText: "with \"curly\"", NewText: "x"},
	})
	var nm NoMatchError
	if !errors.As(err, &nm) {
		t.Fatalf("want NoMatchError, got %v", err)
	}
}

func TestApplyEdits_FuzzyBlankPatternRejected(t *testing.T) {
	_, err := applyEdits("a\n\nb", []Edit{{OldText: "   ", NewText: "x"}})
	var nm NoMatchError
	if !errors.As(err, &nm) {
		t.Fatalf("want NoMatchError for blank pattern, got %v", err)
	}
}

func TestApplyEdits_FuzzyAmbiguityCounts(t *testing.T) {
	// Two fuzzy hits without ReplaceAll → ambiguous, same as exact.
	_, err := applyEdits("same  \n\nsame\t", []Edit{
		{OldText: "same", NewText: "x"},
	})
	var am AmbiguousMatchError
	if !errors.As(err, &am) || am.Occurrences != 2 {
		t.Fatalf("want AmbiguousMatchError{_, 2}, got %v", err)
	}
}

func TestApplyEdits_CRLFOldTextNormalized(t *testing.T) {
	content := "one\n\ntwo"
	got, err := applyEdits(content, []Edit{
		{OldText: "one\r\n\r\ntwo", NewText: "joined"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "joined" {
		t.Errorf("got %q, want %q", got, "joined")
	}
}

func TestApplyEdits_NoopIsIdentical(t *testing.T) {
	content := "- [x] already ticked"
	got, err := applyEdits(content, []Edit{
		{OldText: "- [x] already ticked", NewText: "- [x] already ticked"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != content {
		t.Errorf("got %q, want unchanged content", got)
	}
}

func TestExactMatches_NonOverlapping(t *testing.T) {
	// "aaaa" contains "aa" at 0 and 2 with a non-overlapping scan.
	got := exactMatches("aaaa", "aa")
	if len(got) != 2 || got[0] != [2]int{0, 2} || got[1] != [2]int{2, 4} {
		t.Errorf("got %v", got)
	}
}
