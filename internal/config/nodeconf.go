package config

import (
	_ "embed"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// nodeconfStaging is the packaged fallback nodeconf, vendored from the
// sibling test-etc/staging.yml (re-copy if that file changes — drift is
// accepted; the staging conf rarely moves). It replaces the old
// CWD-relative `../test-etc/staging.yml` read so packaged binaries (the
// any-ui desktop-shell sidecar, installed CLIs) boot from any working
// directory. The default network stays STAGING — flipping the packaged
// default to a production network is a separate release decision.
//
//go:embed nodeconf-staging.yml
var nodeconfStaging []byte

// Source tags returned by LoadNodeconf and logged at SDK boot. The
// embedded fallback succeeds from anywhere, so a silently dropped
// network section (typo'd key, wrong nesting — the config parse is
// non-strict) lands the account on staging with no error; the boot log
// of the winning source is what makes that diagnosable.
const (
	NodeconfSourceInline   = "inline"
	NodeconfSourcePath     = "path"
	NodeconfSourceEmbedded = "embedded-staging-fallback"
)

// LoadNodeconf returns the YAML bytes any-sync needs to bootstrap, plus
// the NodeconfSource* tag they came from.
// Precedence: inline network.nodeconf → network.nodeconfPath → the
// embedded staging fallback. Errors only when an explicitly configured
// path is unreadable.
func LoadNodeconf(cfg Network) ([]byte, string, error) {
	if cfg.Nodeconf != "" {
		return []byte(cfg.Nodeconf), NodeconfSourceInline, nil
	}
	if cfg.NodeconfPath != "" {
		expanded := ExpandTilde(cfg.NodeconfPath)
		raw, err := os.ReadFile(expanded)
		if err != nil {
			return nil, "", fmt.Errorf("read nodeconf %s: %w", expanded, err)
		}
		return raw, NodeconfSourcePath, nil
	}
	return nodeconfStaging, NodeconfSourceEmbedded, nil
}

// NodeconfNetworkID best-effort extracts networkId for the boot log.
// Returns "" on parse failure — the SDK's own parse surfaces the error.
func NodeconfNetworkID(raw []byte) string {
	var doc struct {
		NetworkID string `yaml:"networkId"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	return doc.NetworkID
}
