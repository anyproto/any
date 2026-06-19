package indexer

import "testing"

func TestStripStopWords(t *testing.T) {
	cases := []struct{ in, want string }{
		{"how did we set up the CRM sync", "set up CRM sync"},
		{"what does RRF mean", "RRF mean"},
		{"why is my sourdough starter not rising", "sourdough starter not rising"},
		// Punctuation on a stop word still matches (normalized core).
		{"How? what's the plan", "what's plan"},
		// All-stop-words query is returned unchanged (better noisy than empty).
		{"what is the", "what is the"},
		{"", ""},
		// Non-stop content is preserved verbatim, including case.
		{"GPU Budget Review", "GPU Budget Review"},
	}
	for _, c := range cases {
		if got := stripStopWords(c.in); got != c.want {
			t.Errorf("stripStopWords(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
