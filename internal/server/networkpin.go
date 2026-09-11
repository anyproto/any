package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// networkPinFile records the any-sync network an account's data belongs
// to, at <account-dir>/network.json. The network is a per-process
// setting, but spaces, ACLs and the SDK's replay cache only make sense on
// the network that wrote them, so the first boot pins the id and a boot
// under another network is refused (docs/02-server.md § Startup).
const networkPinFile = "network.json"

// networkPinVersion is the envelope version written to network.json.
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
	if env.Version != networkPinVersion || env.NetworkId == "" {
		return false, fmt.Errorf("%w: %s: unsupported version %d or empty networkId", errNetworkPinCorrupt, path, env.Version)
	}
	if env.NetworkId != networkId {
		return true, &ErrNetworkMismatch{Pinned: env.NetworkId, Configured: networkId}
	}
	return true, nil
}

// writeNetworkPin pins the dir to networkId.
func writeNetworkPin(dir, networkId string) error {
	body, err := json.Marshal(networkPinEnvelope{Version: networkPinVersion, NetworkId: networkId})
	if err != nil {
		return err
	}
	return writeFileAtomic(networkPinPath(dir), body, 0o600)
}
