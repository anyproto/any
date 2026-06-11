package config

import (
	"strings"
	"testing"
)

// The packaged fallback must work from ANY working directory — the old
// CWD-relative ../test-etc/staging.yml read broke packaged binaries
// (desktop-shell sidecar, installed CLIs). See
// docs/plans/20260611-desktop-shell-server-contract.md.
func TestLoadNodeconfEmbeddedFallback(t *testing.T) {
	raw, source, err := LoadNodeconf(Network{})
	if err != nil {
		t.Fatalf("embedded fallback errored: %v", err)
	}
	if source != NodeconfSourceEmbedded {
		t.Fatalf("source = %q, want %q", source, NodeconfSourceEmbedded)
	}
	if len(raw) == 0 {
		t.Fatal("embedded fallback is empty")
	}
	if !strings.Contains(string(raw), "networkId:") {
		t.Fatalf("embedded fallback doesn't look like a nodeconf: %q", string(raw[:min(80, len(raw))]))
	}
}

// Explicit overrides keep precedence over the embedded fallback.
func TestLoadNodeconfInlineWinsOverEmbedded(t *testing.T) {
	raw, source, err := LoadNodeconf(Network{Nodeconf: "networkId: inline-test"})
	if err != nil {
		t.Fatalf("inline nodeconf errored: %v", err)
	}
	if source != NodeconfSourceInline {
		t.Fatalf("source = %q, want %q", source, NodeconfSourceInline)
	}
	if string(raw) != "networkId: inline-test" {
		t.Fatalf("inline nodeconf not returned verbatim: %q", string(raw))
	}
}

// A configured-but-unreadable path is still an error (not silently
// swallowed by the fallback).
func TestLoadNodeconfBadPathStillErrors(t *testing.T) {
	_, _, err := LoadNodeconf(Network{NodeconfPath: "/nonexistent/nodeconf.yml"})
	if err == nil {
		t.Fatal("expected error for unreadable nodeconfPath")
	}
}
