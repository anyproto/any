package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

// nodeconfProd is the packaged default nodeconf — the production
// any-sync network, vendored from the sibling test-etc/prod.yml
// (re-copy if that file changes — drift is accepted). It is what a
// packaged binary joins with no configuration at all: the desktop-shell
// sidecar, an installed CLI, the mobile bindings.
//
//go:embed nodeconf-prod.yml
var nodeconfProd []byte

// nodeconfPlaceholder is the sanitized fixture — a real networkId with
// placeholder peer IDs and hosts, so a server boots and serves but joins
// no network. Tests that need a bootable conf without network traffic
// use it through NodeconfPlaceholder; nothing selects it at runtime.
//
//go:embed nodeconf-placeholder.yml
var nodeconfPlaceholder []byte

// NodeconfPlaceholder returns the sanitized fixture bytes. Callers that
// must boot the SDK without joining a network (tests, offline smoke
// runs) pass it as Network.Nodeconf instead of falling through to the
// production default.
func NodeconfPlaceholder() []byte { return slices.Clone(nodeconfPlaceholder) }

// LoadNodeconf returns the YAML bytes any-sync needs to bootstrap.
// Precedence: inline network.nodeconf → network.nodeconfPath → the
// embedded production default. Errors only when an explicitly configured
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
	return nodeconfProd, nil
}

// NetworkId returns the id of the any-sync network a nodeconf names.
func NetworkId(nodeconf []byte) (string, error) {
	var nc struct {
		NetworkId string `yaml:"networkId"`
	}
	if err := yaml.Unmarshal(nodeconf, &nc); err != nil {
		return "", fmt.Errorf("parse nodeconf: %w", err)
	}
	if nc.NetworkId == "" {
		return "", errors.New("nodeconf names no networkId")
	}
	return nc.NetworkId, nil
}
