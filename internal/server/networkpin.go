package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anyproto/any/internal/config"
)

// networkPinFile records the any-sync network an account's data belongs
// to, at <account-dir>/network.json. The network is a per-process
// setting, but spaces, ACLs and the SDK's replay cache only make sense on
// the network that wrote them, so the first boot pins the id and a boot
// under another network is refused (docs/02-server.md § Startup).
const networkPinFile = "network.json"

// networkPinVersion is the envelope version written to network.json.
// Every version keeps networkId, so any version ≥ 1 is readable.
const networkPinVersion = 1

type networkPinEnvelope struct {
	Version   int    `json:"version"`
	NetworkId string `json:"networkId"`
}

// ErrNetworkMismatch refuses a boot whose configured network is not the
// one the account dir is pinned to.
type ErrNetworkMismatch struct {
	Pinned     string
	Configured string
}

func (e *ErrNetworkMismatch) Error() string {
	return fmt.Sprintf("account data belongs to network %s, but this server is configured for network %s — "+
		"start it with that network's nodeconf, or use a separate data dir for the other network", e.Pinned, e.Configured)
}

// errNetworkPinCorrupt: the pin exists but cannot be read. Never
// rewritten from the current config — that would defeat the guard.
var errNetworkPinCorrupt = errors.New("network pin unreadable")

func networkPinPath(dir string) string { return filepath.Join(dir, networkPinFile) }

// configuredNetwork loads the server's nodeconf and the id of the
// network it names.
func configuredNetwork(n config.Network) (nodeconf []byte, networkId string, err error) {
	if nodeconf, err = config.LoadNodeconf(n); err != nil {
		return nil, "", err
	}
	networkId, err = config.NodeconfNetworkId(nodeconf)
	return nodeconf, networkId, err
}

// checkNetworkPin refuses networkId when the dir is pinned to another
// network. pinned reports whether the dir carries a pin at all; an
// unpinned dir passes and is pinned by writeNetworkPin once it boots.
func checkNetworkPin(dir, networkId string) (pinned bool, err error) {
	path := networkPinPath(dir)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var env networkPinEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return false, fmt.Errorf("%w: parse %s: %v", errNetworkPinCorrupt, path, err)
	}
	if env.Version < 1 || env.NetworkId == "" {
		return false, fmt.Errorf("%w: %s: version %d, networkId %q", errNetworkPinCorrupt, path, env.Version, env.NetworkId)
	}
	if env.NetworkId != networkId {
		return true, &ErrNetworkMismatch{Pinned: env.NetworkId, Configured: networkId}
	}
	return true, nil
}

// checkConfiguredNetwork is checkNetworkPin against the configured
// network.
func checkConfiguredNetwork(n config.Network, dir string) error {
	_, networkId, err := configuredNetwork(n)
	if err != nil {
		return err
	}
	_, err = checkNetworkPin(dir, networkId)
	return err
}

// writeNetworkPin pins the dir to networkId.
func writeNetworkPin(dir, networkId string) error {
	body, err := json.Marshal(networkPinEnvelope{Version: networkPinVersion, NetworkId: networkId})
	if err != nil {
		return err
	}
	return writeFileAtomic(networkPinPath(dir), body, 0o600)
}
