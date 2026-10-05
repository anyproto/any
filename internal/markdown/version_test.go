package markdown

import "testing"

// TestFormatVersionCoversContent pins that two bodies never share a
// version, even at the same generation and sequence — the case a store
// restored from an older backup produces.
func TestFormatVersionCoversContent(t *testing.T) {
	if formatVersion("g", 7, "alpha") == formatVersion("g", 7, "beta") {
		t.Fatal("same generation and sequence with different bodies gave one version")
	}
	if formatVersion("g", 7, "alpha") != formatVersion("g", 7, "alpha") {
		t.Fatal("formatVersion is not deterministic")
	}
}
