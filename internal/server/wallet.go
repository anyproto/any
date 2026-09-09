package server

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/auth"
	"github.com/anyproto/any-sync/util/crypto"

	"github.com/anyproto/any/internal/config"
)

// OpenWallet loads or creates the file-backed wallet at path. A
// non-empty mnemonic seeds creation (restore / second-device path; the
// device key is always fresh) and must match an already-existing
// wallet; index is the account-derivation index applied on creation —
// fresh generation included — and read back from the file otherwise.
// On first generation the returned firstRun flag is true, and the
// caller is expected to display Mnemonic() once.
func OpenWallet(path, passkey, mnemonic string, index uint32) (provider *auth.FileProvider, firstRun bool, err error) {
	p, err := auth.NewFileProvider(auth.FileProviderConfig{Path: path, Passkey: passkey, Mnemonic: mnemonic, Index: index})
	if err != nil {
		return nil, false, fmt.Errorf("open wallet: %w", err)
	}
	return p, p.Created(), nil
}

// AccountID returns the anytype account identifier derived from the
// provider's account key — the string health exposes as "account".
func AccountID(ctx context.Context, p auth.Provider) (string, error) {
	priv, err := accountKey(ctx, p)
	if err != nil {
		return "", err
	}
	return priv.GetPublic().Account(), nil
}

// accountKey returns the provider's account signing key — what the
// engine keeps to sign on the account's behalf (access codes).
func accountKey(ctx context.Context, p auth.Provider) (crypto.PrivKey, error) {
	raw, err := p.AccountKey(ctx)
	if err != nil {
		return nil, err
	}
	priv, err := crypto.UnmarshalEd25519PrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal account key: %w", err)
	}
	return priv, nil
}

// credential opens the keys an engine boots with: the provider the
// SDK signs with, and whether this call minted local state (a wallet
// file or a device key) that a failed boot must remove again. Invoked
// by bootEngine after the account dir's instance lock is held.
type credential func(ctx context.Context) (provider auth.Provider, created bool, err error)

// fileCredential is the standalone custody: the wallet file at path
// (created from seed when absent), unlocked with the configured
// passkey. Both keys live in the file.
func fileCredential(cfg config.Config, path string, seed walletSeed) credential {
	return func(context.Context) (auth.Provider, bool, error) {
		passkey, err := config.ResolvePasskey(cfg, false)
		if err != nil {
			return nil, false, err
		}
		return OpenWallet(path, passkey, seed.mnemonic, seed.index)
	}
}
