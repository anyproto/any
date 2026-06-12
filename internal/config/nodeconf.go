package config

import (
	_ "embed"
	"fmt"
	"os"
)

// nodeconfStaging is the packaged fallback nodeconf, vendored from the
// sibling test-etc/staging.yml (re-copy if that file changes — drift is
// accepted). It replaces the old CWD-relative `../test-etc/staging.yml`
// read so packaged binaries (the any-ui desktop-shell sidecar, installed
// CLIs) boot from any working directory. NOTE: the fixture is sanitized
// — staging networkId but placeholder peer IDs/hosts — so the fallback
// boots and works locally yet joins no network. Shipping a functional
// default (a real staging or production conf) is a one-file swap here
// and a separate release decision.
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
