package server

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/auth"
	"github.com/anyproto/any-sync/util/crypto"
)

// OpenWallet loads or creates the file-backed wallet at path. A
// non-empty mnemonic seeds creation (restore / second-device path; the
// device key is always fresh) and must match an already-existing
// wallet; index is the matching account-derivation index, applied only
// on creation (the SDK reads it back from the file otherwise). On first
// generation the returned firstRun flag is true, and the caller is
// expected to display Mnemonic() once.
func OpenWallet(path, passkey, mnemonic string, index uint32) (provider *auth.FileProvider, firstRun bool, err error) {
	p, err := auth.NewFileProvider(auth.FileProviderConfig{Path: path, Passkey: passkey, Mnemonic: mnemonic, Index: index})
	if err != nil {
		return nil, false, fmt.Errorf("open wallet: %w", err)
	}
	return p, p.Created(), nil
}

// AccountID returns the anytype account identifier derived from the
// provider's account key — the string health exposes as "account".
func AccountID(ctx context.Context, p *auth.FileProvider) (string, error) {
	raw, err := p.AccountKey(ctx)
	if err != nil {
		return "", err
	}
	priv, err := crypto.UnmarshalEd25519PrivateKey(raw)
	if err != nil {
		return "", fmt.Errorf("unmarshal account key: %w", err)
	}
	return priv.GetPublic().Account(), nil
}
