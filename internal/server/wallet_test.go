package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anyproto/any-sync-sdk/auth"

	"github.com/anyproto/any/internal/config"
)

// TestOpenWallet_HonorsIndex is the F1 regression: the account-derivation
// index must reach FileProviderConfig, so a wallet created at index N
// derives the index-N account id — the same id its <root>/<id>/ dir is
// named after. Before the fix OpenWallet dropped the index and seeded
// every wallet at index 0.
func TestOpenWallet_HonorsIndex(t *testing.T) {
	m, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wallet.key")

	provider, created, err := OpenWallet(path, "", m, 1)
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}
	if !created {
		t.Fatal("first OpenWallet should report created")
	}
	got, err := AccountID(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}

	want, err := auth.AccountId(m, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("index-1 wallet derived %s, want %s (index dropped?)", got, want)
	}
	id0, _ := auth.AccountId(m, 0)
	if got == id0 {
		t.Fatal("index 1 derived the index-0 account — index ignored")
	}
}

// bogusTopologyConfig forces OpenSDK to fail deterministically AFTER the
// wallet is created (the topology switch runs after LoadNodeconf and key
// derivation), with no network access — the lever the bootEngine
// cleanup tests need.
func bogusTopologyConfig(root string) config.Config {
	cfg := config.Defaults()
	cfg.DataDir = root
	cfg.Storage.Topology = "bogus-topology"
	cfg.Index.Enabled = false
	return cfg
}

// TestBootEngine_CleansOrphanOnFailure is the F2 regression: when a boot
// freshly creates a per-account wallet and the SDK then fails to come
// up, the orphan account dir is removed — otherwise a generated account
// whose phrase was never surfaced would linger and be auto-selected on
// the next start.
func TestBootEngine_CleansOrphanOnFailure(t *testing.T) {
	root := t.TempDir()
	m, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	id, err := auth.AccountId(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	dir := config.AccountDir(root, id)
	identity := &Identity{Account: id, Dir: dir, WalletPath: config.WalletPath(config.Config{}, dir)}

	ctx := context.Background()
	cfg := bogusTopologyConfig(root)
	_, err = bootEngine(ctx, cfg, root, identity, fileCredential(cfg, identity.WalletPath, walletSeed{mnemonic: m}), nil, nil)
	if err == nil {
		t.Fatal("expected boot failure on bogus topology")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("orphan account dir was not cleaned up: stat err = %v", statErr)
	}
}

// TestBootEngine_KeepsExistingWalletOnFailure is the other half of F2:
// a boot failure must NOT remove a wallet that already existed before
// the call (only this-call creations are orphans).
func TestBootEngine_KeepsExistingWalletOnFailure(t *testing.T) {
	root := t.TempDir()
	m, err := auth.GenerateMnemonic()
	if err != nil {
		t.Fatal(err)
	}
	id, err := auth.AccountId(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	dir := config.AccountDir(root, id)
	walletPath := config.WalletPath(config.Config{}, dir)
	if _, _, err := OpenWallet(walletPath, "", m, 0); err != nil {
		t.Fatalf("pre-create wallet: %v", err)
	}

	identity := &Identity{Account: id, Dir: dir, WalletPath: walletPath}
	ctx := context.Background()
	cfg := bogusTopologyConfig(root)
	_, err = bootEngine(ctx, cfg, root, identity, fileCredential(cfg, identity.WalletPath, walletSeed{}), nil, nil)
	if err == nil {
		t.Fatal("expected boot failure on bogus topology")
	}
	if _, statErr := os.Stat(walletPath); statErr != nil {
		t.Fatalf("pre-existing wallet was wrongly removed: %v", statErr)
	}
}
