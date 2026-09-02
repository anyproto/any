package server

import (
	"fmt"
	"strings"

	"github.com/anyproto/any/internal/config"
)

// Identity names the wallet a server boots with and where that
// account's data lives. Dir is the per-account data dir (the root
// itself for the legacy flat layout and for an explicit walletPath
// override).
type Identity struct {
	// Account is the expected account id. Empty when it can only be
	// learned by opening the wallet (root wallet / explicit override);
	// when non-empty the caller verifies the derived id against it.
	Account string
	// Dir holds this account's server.pid, sdk/ and index/ — and, under
	// managed custody, the cached device.key.
	Dir string
	// WalletPath is the wallet file to open. Empty under managed
	// custody, where the account key never touches disk.
	WalletPath string
}

// ErrNoIdentity is the resolution outcome "nothing to boot": no wallet
// selected and none unambiguously present. `run` starts unauthorized
// and waits for POST /v1/auth; CLI paths print the hint instead.
type ErrNoIdentity struct {
	// Accounts lists the per-account dirs found (empty = fresh root).
	Accounts []string
}

func (e *ErrNoIdentity) Error() string {
	if len(e.Accounts) == 0 {
		return "no account found — create one with `any init` or POST /v1/auth"
	}
	return fmt.Sprintf("multiple accounts found (%s) — select one with --account / ANY_ACCOUNT",
		strings.Join(e.Accounts, ", "))
}

// ResolveIdentity decides which wallet to boot for the given root:
//
//  1. auth.walletPath / --wallet override → that wallet, data flat at
//     the root (manual mode, no per-account nesting).
//  2. --account / ANY_ACCOUNT / account: selector → <root>/<id>/ if it
//     exists, else the root wallet (verified against the selector
//     after opening), else an error.
//  3. No selector: root wallet.key → the default flat account; else a
//     sole per-account dir; else *ErrNoIdentity (none or several).
func ResolveIdentity(cfg config.Config, root string) (*Identity, error) {
	if cfg.Auth.WalletPath != "" {
		return &Identity{Dir: root, WalletPath: config.WalletPath(cfg, root)}, nil
	}

	accounts, err := config.ListAccounts(root)
	if err != nil {
		return nil, err
	}
	hasRoot := config.HasRootWallet(root)

	if sel := cfg.Account; sel != "" {
		for _, id := range accounts {
			if id == sel {
				dir := config.AccountDir(root, sel)
				return &Identity{Account: sel, Dir: dir, WalletPath: config.WalletPath(cfg, dir)}, nil
			}
		}
		if hasRoot {
			// The root wallet's id is unknown until opened; carry the
			// selector along so boot can verify the match.
			return &Identity{Account: sel, Dir: root, WalletPath: config.WalletPath(cfg, root)}, nil
		}
		return nil, fmt.Errorf("account %s not found under %s", sel, root)
	}

	if hasRoot {
		return &Identity{Dir: root, WalletPath: config.WalletPath(cfg, root)}, nil
	}
	if len(accounts) == 1 {
		dir := config.AccountDir(root, accounts[0])
		return &Identity{Account: accounts[0], Dir: dir, WalletPath: config.WalletPath(cfg, dir)}, nil
	}
	return nil, &ErrNoIdentity{Accounts: accounts}
}
