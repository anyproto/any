// Device-token persistence: one JSON file (push-token.json) in the
// per-account data dir. Device-local by design — a push token names
// THIS device's APNs/FCM endpoint, so it must never ride a synced
// dataset (task-push-notifications.md § Settings storage).

package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// tokenFileName is the token file's name inside the per-account data
// dir (next to wallet.key / server.pid).
const tokenFileName = "push-token.json"

// deviceToken is the persisted shape: which transport the token
// belongs to and the opaque token string itself.
type deviceToken struct {
	Platform string `json:"platform"`
	Token    string `json:"token"`
}

// loadTokenFile reads the persisted token. A missing file is not an
// error — (nil, nil) means "no token registered".
func loadTokenFile(path string) (*deviceToken, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("push: read token file: %w", err)
	}
	var tok deviceToken
	if err := json.Unmarshal(raw, &tok); err != nil {
		return nil, fmt.Errorf("push: parse token file: %w", err)
	}
	if tok.Token == "" {
		return nil, nil
	}
	return &tok, nil
}

// saveTokenFile persists the token at 0600 (same posture as
// wallet.key — the token is a device credential).
func saveTokenFile(path string, tok deviceToken) error {
	raw, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("push: encode token file: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("push: write token file: %w", err)
	}
	return nil
}

// removeTokenFile deletes the persisted token; a missing file is fine.
func removeTokenFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("push: remove token file: %w", err)
	}
	return nil
}
