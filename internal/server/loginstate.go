package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/auth"

	"github.com/anyproto/any/internal/config"
)

// loginStateFile records, at <account-dir>/login.json, whether a login
// has seen the account's first sync: "pending" keeps the engine closed,
// "complete" or no file opens it (docs/02-server.md § Startup). Local
// tech-space storage cannot tell: the SDK creates it on every first open.
const loginStateFile = "login.json"

// loginStateVersion is the envelope version written to login.json.
const loginStateVersion = 1

const (
	loginStatePending  = "pending"
	loginStateComplete = "complete"
)

type loginStateEnvelope struct {
	Version int    `json:"version"`
	State   string `json:"state"`
}

func loginStatePath(dir string) string { return filepath.Join(dir, loginStateFile) }

// sdkDataDir is where OpenSDK keeps the account's SDK data.
func sdkDataDir(dir string) string { return filepath.Join(dir, "sdk") }

// loginPending reports whether dir holds a login that has not seen its
// first sync. A file that cannot be read counts as pending: waiting for
// one more sync is the safe reading, and the sync rewrites the file.
func loginPending(dir string) bool {
	raw, err := os.ReadFile(loginStatePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	var env loginStateEnvelope
	if err == nil {
		err = json.Unmarshal(raw, &env)
	}
	switch {
	case err == nil && env.State == loginStateComplete:
		return false
	case err == nil && env.State == loginStatePending:
		return true
	}
	engineLog.Warn("login state unreadable — waiting for the account's first sync again",
		zap.String("path", loginStatePath(dir)), zap.Error(err))
	return true
}

// LoginPending is loginPending for the CLI.
func LoginPending(dir string) bool { return loginPending(dir) }

func writeLoginState(dir, state string) error {
	body, err := json.Marshal(loginStateEnvelope{Version: loginStateVersion, State: state})
	if err != nil {
		return err
	}
	return writeFileAtomic(loginStatePath(dir), body, 0o600)
}

// MarkLoginPending records a login in dir before its keys are created.
// keyPath is the key file the login is about to create. A no-op when
// the dir already holds that key, the account dir's key of the other
// custody, SDK data, or a login state: only a phrase on a device with
// nothing of the account is a login. The dir is created when missing.
func MarkLoginPending(dir, keyPath string) error {
	held := []string{keyPath, sdkDataDir(dir), loginStatePath(dir)}
	switch wallet := config.WalletPath(config.Config{}, dir); keyPath {
	case wallet:
		held = append(held, deviceKeyPath(dir))
	case deviceKeyPath(dir):
		held = append(held, wallet)
	}
	for _, path := range held {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeLoginState(dir, loginStatePending)
}

// loginCredential opens a phrase's keys as a login: the pending mark is
// written first, under the instance lock bootEngine holds, so keys never
// exist without it.
func loginCredential(dir, keyPath string, open credential) credential {
	return func(ctx context.Context) (auth.Provider, bool, error) {
		if err := MarkLoginPending(dir, keyPath); err != nil {
			return nil, false, err
		}
		return open(ctx)
	}
}
