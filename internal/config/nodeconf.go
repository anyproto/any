package config

import (
	"fmt"
	"os"
)

// DefaultNodeconfPath is the staging-network nodeconf used when neither
// network.nodeconfPath nor an inline network.nodeconf is configured. It
// points at the sibling test-etc checkout that ships with the SDK; will
// be replaced by a packaged default before v1 ships.
const DefaultNodeconfPath = "../test-etc/staging.yml"

// LoadNodeconf returns the YAML bytes any-sync needs to bootstrap.
// Precedence: inline network.nodeconf → network.nodeconfPath → fallback
// at DefaultNodeconfPath. Errors only when the configured/fallback path
// is unreadable.
func LoadNodeconf(cfg Network) ([]byte, error) {
	if cfg.Nodeconf != "" {
		return []byte(cfg.Nodeconf), nil
	}
	path := cfg.NodeconfPath
	if path == "" {
		path = DefaultNodeconfPath
	}
	expanded := ExpandTilde(path)
	raw, err := os.ReadFile(expanded)
	if err != nil {
		return nil, fmt.Errorf("read nodeconf %s: %w", expanded, err)
	}
	return raw, nil
}
