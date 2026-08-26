package indexer

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSnippetTerms(t *testing.T) {
	got := snippetTerms(`"Zeppelin Disaster" hind*`, []string{"1937"})
	want := []string{"zeppelin", "disaster", "hind", "1937"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("terms = %v, want %v", got, want)
	}
}

func TestSnippet(t *testing.T) {
	// Fits → untouched.
	if d, off, tot := snippet("hello world", []string{"world"}, 512); d != "hello world" || off != 0 || tot != 11 {
		t.Fatalf("fits = %q %d %d", d, off, tot)
	}
	// -1 → whole text however long.
	long := strings.Repeat("filler ", 200) + "needle here " + strings.Repeat("tail ", 200)
	if d, _, _ := snippet(long, []string{"needle"}, -1); d != long {
		t.Fatal("-1 must keep the whole text")
	}
	// Window lands on the match, bounded, on word boundaries, offset set.
	d, off, tot := snippet(long, []string{"needle"}, 100)
	if !strings.Contains(d, "needle here") {
		t.Fatalf("window misses the match: %q", d)
	}
	if n := utf8.RuneCountInString(d); n > 100 {
		t.Fatalf("window = %d runes > 100", n)
	}
	if off == 0 || tot != utf8.RuneCountInString(long) {
		t.Fatalf("offset %d total %d", off, tot)
	}
	if strings.HasPrefix(d, "iller") || strings.HasSuffix(d, "tai") {
		t.Fatalf("window cut mid-word: %q", d)
	}
	// No match → head.
	d, off, _ = snippet(long, []string{"absent"}, 50)
	if off != 0 || !strings.HasPrefix(long, d) {
		t.Fatalf("no-match head = %q off %d", d, off)
	}
	// Case-insensitive, rune offsets on multibyte text.
	cjk := strings.Repeat("日本語 ", 50) + "Needle" + strings.Repeat(" 日本語", 50)
	d, off, _ = snippet(cjk, []string{"needle"}, 40)
	at := utf8.RuneCountInString(strings.Repeat("日本語 ", 50))
	if !strings.Contains(d, "Needle") || off > at || off < at-40 || utf8.RuneCountInString(d) > 40 {
		t.Fatalf("cjk window = %q off %d (match at %d)", d, off, at)
	}
}
