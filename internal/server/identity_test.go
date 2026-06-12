package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anyproto/any/internal/config"
)

func touchWallet(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wallet.key"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveIdentity(t *testing.T) {
	t.Run("fresh root → no identity", func(t *testing.T) {
		_, err := ResolveIdentity(config.Config{}, t.TempDir())
		var noID *ErrNoIdentity
		if !errors.As(err, &noID) || len(noID.Accounts) != 0 {
			t.Fatalf("want empty ErrNoIdentity, got %v", err)
		}
	})

	t.Run("root wallet → default flat identity", func(t *testing.T) {
		root := t.TempDir()
		touchWallet(t, root)
		id, err := ResolveIdentity(config.Config{}, root)
		if err != nil {
			t.Fatal(err)
		}
		if id.Dir != root || id.Account != "" || id.WalletPath != filepath.Join(root, "wallet.key") {
			t.Fatalf("unexpected identity: %+v", id)
		}
	})

	t.Run("sole nested account → that identity", func(t *testing.T) {
		root := t.TempDir()
		touchWallet(t, filepath.Join(root, "Aaa"))
		id, err := ResolveIdentity(config.Config{}, root)
		if err != nil {
			t.Fatal(err)
		}
		if id.Account != "Aaa" || id.Dir != filepath.Join(root, "Aaa") {
			t.Fatalf("unexpected identity: %+v", id)
		}
	})

	t.Run("root wallet wins over sole nested", func(t *testing.T) {
		root := t.TempDir()
		touchWallet(t, root)
		touchWallet(t, filepath.Join(root, "Aaa"))
		id, err := ResolveIdentity(config.Config{}, root)
		if err != nil {
			t.Fatal(err)
		}
		if id.Dir != root {
			t.Fatalf("default root wallet should win: %+v", id)
		}
	})

	t.Run("several nested, no selector → ambiguous", func(t *testing.T) {
		root := t.TempDir()
		touchWallet(t, filepath.Join(root, "Aaa"))
		touchWallet(t, filepath.Join(root, "Abb"))
		_, err := ResolveIdentity(config.Config{}, root)
		var noID *ErrNoIdentity
		if !errors.As(err, &noID) || len(noID.Accounts) != 2 {
			t.Fatalf("want ErrNoIdentity with 2 accounts, got %v", err)
		}
	})

	t.Run("selector picks nested", func(t *testing.T) {
		root := t.TempDir()
		touchWallet(t, root)
		touchWallet(t, filepath.Join(root, "Aaa"))
		id, err := ResolveIdentity(config.Config{Account: "Aaa"}, root)
		if err != nil {
			t.Fatal(err)
		}
		if id.Account != "Aaa" || id.Dir != filepath.Join(root, "Aaa") {
			t.Fatalf("unexpected identity: %+v", id)
		}
	})

	t.Run("selector falls back to root wallet for boot-time verify", func(t *testing.T) {
		root := t.TempDir()
		touchWallet(t, root)
		id, err := ResolveIdentity(config.Config{Account: "Azz"}, root)
		if err != nil {
			t.Fatal(err)
		}
		if id.Account != "Azz" || id.Dir != root {
			t.Fatalf("unexpected identity: %+v", id)
		}
	})

	t.Run("selector with no candidates → error", func(t *testing.T) {
		if _, err := ResolveIdentity(config.Config{Account: "Azz"}, t.TempDir()); err == nil {
			t.Fatal("want error for unknown account")
		}
	})

	t.Run("explicit walletPath → manual mode", func(t *testing.T) {
		root := t.TempDir()
		wallet := filepath.Join(t.TempDir(), "w.key")
		cfg := config.Config{Auth: config.Auth{WalletPath: wallet}}
		id, err := ResolveIdentity(cfg, root)
		if err != nil {
			t.Fatal(err)
		}
		if id.Dir != root || id.WalletPath != wallet {
			t.Fatalf("unexpected identity: %+v", id)
		}
	})
}

func TestListAccountsAndRootWallet(t *testing.T) {
	root := t.TempDir()
	if config.HasRootWallet(root) {
		t.Fatal("fresh root must not report a root wallet")
	}
	touchWallet(t, filepath.Join(root, "Abb"))
	touchWallet(t, filepath.Join(root, "Aaa"))
	// Dirs without a wallet (models/, index/) are not accounts.
	if err := os.MkdirAll(filepath.Join(root, "models"), 0o700); err != nil {
		t.Fatal(err)
	}
	touchWallet(t, root)

	ids, err := config.ListAccounts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "Aaa" || ids[1] != "Abb" {
		t.Fatalf("ListAccounts = %v", ids)
	}
	if !config.HasRootWallet(root) {
		t.Fatal("root wallet not detected")
	}
}
