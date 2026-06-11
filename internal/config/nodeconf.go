package config

import (
	_ "embed"
	"fmt"
	"os"
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

// LoadNodeconf returns the YAML bytes any-sync needs to bootstrap.
// Precedence: inline network.nodeconf → network.nodeconfPath → the
// embedded staging fallback. Errors only when an explicitly configured
// path is unreadable. The SDK logs the applied networkId at boot
// (common.nodeconf "net configuration applied"), so the chosen source
// is diagnosable from the log stream without extra plumbing here.
func LoadNodeconf(cfg Network) ([]byte, error) {
	if cfg.Nodeconf != "" {
		return []byte(cfg.Nodeconf), nil
	}
	if cfg.NodeconfPath != "" {
		expanded := ExpandTilde(cfg.NodeconfPath)
		raw, err := os.ReadFile(expanded)
		if err != nil {
			return nil, fmt.Errorf("read nodeconf %s: %w", expanded, err)
		}
		return raw, nil
	}
	return nodeconfStaging, nil
}
