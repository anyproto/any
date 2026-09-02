package server

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anyproto/any-sync-sdk/auth"
)

// deviceKeyFile is the per-account device-key cache a managed server
// keeps at <root>/<accountId>/device.key. The account key never
// touches disk in managed mode — the host supplies it on every boot —
// but the device key must: it is what the peerId derives from, and a
// login that re-minted it would leave a permanent row in the synced
// devices registry on every app launch. Minted once, reused forever,
// never portable (docs/02-server.md § Data dir layout).
const deviceKeyFile = "device.key"

// deviceKeyVersion is the envelope version written to device.key.
const deviceKeyVersion = 1

type deviceKeyEnvelope struct {
	Version   int    `json:"version"`
	DeviceKey string `json:"deviceKey"` // base64, raw Ed25519 private key
}

// errDeviceKeyCorrupt: the cache exists but cannot be used. Deliberately
// not re-minted — that would fork this install's peerId — so the
// operator decides (POST /v1/auth answers auth.device_key_corrupt).
var errDeviceKeyCorrupt = errors.New("device key cache unreadable")

// deviceKeyPath is the cache location for an account dir.
func deviceKeyPath(dir string) string { return filepath.Join(dir, deviceKeyFile) }

// managedCredential is the host-owned custody: the account key comes
// from the mnemonic the host supplied for this boot and never touches
// disk; the device key is the account dir's cached one, minted on the
// first boot and reused after, so the peerId stays stable across
// logins. Index is applied explicitly — the SDK's own default is 0,
// any's is 1.
func managedCredential(dir, mnemonic string, index uint32) credential {
	return func(ctx context.Context) (auth.Provider, bool, error) {
		deviceKey, created, err := loadOrCreateDeviceKey(ctx, dir)
		if err != nil {
			return nil, false, err
		}
		p, err := auth.NewMnemonicProvider(auth.MnemonicConfig{Mnemonic: mnemonic, Index: index, DeviceKey: deviceKey})
		if err != nil {
			return nil, created, err
		}
		return p, created, nil
	}
}

// loadOrCreateDeviceKey returns the account dir's cached device key,
// minting and persisting one (mode 0600) when the cache is absent.
// created reports a mint.
func loadOrCreateDeviceKey(ctx context.Context, dir string) (key []byte, created bool, err error) {
	path := deviceKeyPath(dir)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		var env deviceKeyEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, false, fmt.Errorf("%w: parse %s: %v", errDeviceKeyCorrupt, path, err)
		}
		if env.Version != deviceKeyVersion {
			return nil, false, fmt.Errorf("%w: %s: unsupported version %d", errDeviceKeyCorrupt, path, env.Version)
		}
		key, err := base64.StdEncoding.DecodeString(env.DeviceKey)
		if err != nil || len(key) != ed25519.PrivateKeySize {
			return nil, false, fmt.Errorf("%w: %s: malformed device key", errDeviceKeyCorrupt, path)
		}
		return key, false, nil
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}

	key, err = auth.GenerateDeviceKey(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("generate device key: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, fmt.Errorf("create account dir: %w", err)
	}
	body, err := json.Marshal(deviceKeyEnvelope{Version: deviceKeyVersion, DeviceKey: base64.StdEncoding.EncodeToString(key)})
	if err != nil {
		return nil, false, err
	}
	// Written whole or not at all: a crash mid-write must not leave a
	// truncated cache that locks the account out of this install.
	if err := writeFileAtomic(path, body, 0o600); err != nil {
		return nil, false, fmt.Errorf("write %s: %w", path, err)
	}
	return key, true, nil
}

// writeFileAtomic writes body to a temp file beside path, syncs it and
// renames it into place.
func writeFileAtomic(path string, body []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
