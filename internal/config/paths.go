package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandTilde resolves a leading "~" to the user's home directory.
// Returns the input unchanged when no home can be resolved or no tilde
// is present.
func ExpandTilde(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// EnsureDataDir creates the data directory (mode 0700) if it does not
// exist and returns its absolute, tilde-expanded path.
func EnsureDataDir(raw string) (string, error) {
	p, err := filepath.Abs(ExpandTilde(raw))
	if err != nil {
		return "", fmt.Errorf("resolve data dir: %w", err)
	}
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", fmt.Errorf("create data dir %s: %w", p, err)
	}
	return p, nil
}

// WalletPath returns the effective wallet file path for the config:
// cfg.Auth.WalletPath when set, otherwise <dataDir>/wallet.key.
func WalletPath(cfg Config, dataDir string) string {
	if cfg.Auth.WalletPath != "" {
		return ExpandTilde(cfg.Auth.WalletPath)
	}
	return filepath.Join(dataDir, "wallet.key")
}

// PIDPath returns <dataDir>/server.pid — the plain-text pid of the
// process holding the lock, written after acquiring it. Advisory: it
// names the holder in error messages, it never decides who holds the
// lock. See LockPath.
func PIDPath(dataDir string) string {
	return filepath.Join(dataDir, "server.pid")
}

// LockPath returns <dataDir>/server.lock — the file the single-instance
// OS lock is taken on. Its contents are never read: on Windows the lock
// covers byte 0 and byte-range locks are mandatory, so no other process
// could read it anyway.
func LockPath(dataDir string) string {
	return filepath.Join(dataDir, "server.lock")
}

// AccountDir returns the per-account data dir <root>/<accountId>.
func AccountDir(root, accountId string) string {
	return filepath.Join(root, accountId)
}

// ModelsDir returns the shared embedder model cache <root>/models —
// one download per root, shared by every account under it.
func ModelsDir(root string) string {
	return filepath.Join(root, "models")
}

// HasRootWallet reports whether the root carries a legacy flat-layout
// wallet.key — the default account, whose data stays at the root.
func HasRootWallet(root string) bool {
	st, err := os.Stat(filepath.Join(root, "wallet.key"))
	return err == nil && st.Mode().IsRegular()
}

// ListAccounts returns the account ids that have a per-account dir
// (<root>/<id>/wallet.key) under the root, lexically sorted. The
// legacy root wallet is not included — probe it with HasRootWallet.
func ListAccounts(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read data dir %s: %w", root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if st, err := os.Stat(filepath.Join(root, e.Name(), "wallet.key")); err == nil && st.Mode().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// ConfigSearchPaths lists the files to probe when --config is not
// specified, in priority order.
func ConfigSearchPaths(dataDir string) []string {
	var out []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		out = append(out, filepath.Join(xdg, "any", "config.yaml"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".config", "any", "config.yaml"))
	}
	if dataDir != "" {
		out = append(out, filepath.Join(dataDir, "config.yaml"))
	}
	return out
}
