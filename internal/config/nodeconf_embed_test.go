package config

import (
	"strings"
	"testing"
)

// prodNetworkId pins the embedded default to the PRODUCTION any-sync
// network. A binary with no network config joins production, so a
// silent swap of the vendored conf (or a re-copy from the wrong source
// file) has to fail here.
const prodNetworkId = "N83gJpVd9MuNRZAuJLZ7LiMntTThhPc6DtzWWVjb1M3PouVU"

// The packaged default must work from ANY working directory — the old
// CWD-relative ../test-etc/staging.yml read broke packaged binaries
// (desktop-shell sidecar, installed CLIs). See
// docs/plans/20260611-desktop-shell-server-contract.md.
func TestLoadNodeconfEmbeddedDefaultIsProd(t *testing.T) {
	raw, err := LoadNodeconf(Network{})
	if err != nil {
		t.Fatalf("embedded default errored: %v", err)
	}
	if !strings.Contains(string(raw), "networkId: "+prodNetworkId) {
		t.Fatalf("embedded default is not the production nodeconf: %q", string(raw[:min(200, len(raw))]))
	}
	// Durable file backup needs the fileV2 broker fleet in the conf.
	if !strings.Contains(string(raw), "fileV2") {
		t.Error("embedded default carries no fileV2 nodes")
	}
}

// The placeholder is the tests' bootable-but-offline conf. It must stay
// distinct from the production default, or a test that thinks it is
// offline joins production.
func TestNodeconfPlaceholderIsNotProd(t *testing.T) {
	raw := NodeconfPlaceholder()
	if len(raw) == 0 {
		t.Fatal("placeholder is empty")
	}
	if !strings.Contains(string(raw), "networkId:") {
		t.Fatalf("placeholder doesn't look like a nodeconf: %q", string(raw[:min(80, len(raw))]))
	}
	if strings.Contains(string(raw), prodNetworkId) {
		t.Fatal("placeholder carries the production networkId")
	}
}

// Explicit overrides keep precedence over the embedded default.
func TestLoadNodeconfInlineWinsOverEmbedded(t *testing.T) {
	raw, err := LoadNodeconf(Network{Nodeconf: "networkId: inline-test"})
	if err != nil {
		t.Fatalf("inline nodeconf errored: %v", err)
	}
	if string(raw) != "networkId: inline-test" {
		t.Fatalf("inline nodeconf not returned verbatim: %q", string(raw))
	}
}

// A configured-but-unreadable path is still an error (not silently
// swallowed by the default).
func TestLoadNodeconfBadPathStillErrors(t *testing.T) {
	_, err := LoadNodeconf(Network{NodeconfPath: "/nonexistent/nodeconf.yml"})
	if err == nil {
		t.Fatal("expected error for unreadable nodeconfPath")
	}
}
