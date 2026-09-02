package config

import (
	"errors"
	"fmt"
)

// Server ownership modes (docs/02-server.md § Modes). Mode is fixed at
// launch and unreachable over HTTP — that is what makes the standalone
// refusals enforceable rather than advisory.
const (
	// ModeStandalone: the user owns the server. Keys live in wallet.key,
	// the account resolves from disk, logout and HTTP shutdown are
	// refused.
	ModeStandalone = "standalone"
	// ModeManaged: a spawning host owns the server. The account key is
	// supplied per boot over POST /v1/auth and never written to disk;
	// logout, account switch and HTTP shutdown are allowed behind the
	// control token only the host holds.
	ModeManaged = "managed"
)

// ParseMode normalizes a mode string; "" reads as standalone.
func ParseMode(s string) (string, error) {
	switch s {
	case "", ModeStandalone:
		return ModeStandalone, nil
	case ModeManaged:
		return ModeManaged, nil
	}
	return "", fmt.Errorf("unknown mode %q (standalone | managed)", s)
}

// Managed reports whether the server runs under host ownership.
func (c Config) Managed() bool { return c.Mode == ModeManaged }

// validateMode resolves Mode and rejects the standalone-only inputs
// under managed: the client states the account on every boot, so a
// selector or an explicit wallet path has nothing to select.
func validateMode(cfg *Config) error {
	mode, err := ParseMode(cfg.Mode)
	if err != nil {
		return err
	}
	cfg.Mode = mode
	if mode != ModeManaged {
		return nil
	}
	if cfg.Account != "" {
		return errors.New("mode managed: account selector is not applicable (the client states the account on POST /v1/auth)")
	}
	if cfg.Auth.WalletPath != "" {
		return errors.New("mode managed: auth.walletPath is not applicable (managed servers keep no wallet file)")
	}
	return nil
}
