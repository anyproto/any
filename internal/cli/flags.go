package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/config"
)

// addDataDirFlags attaches the flags that locate a data dir and pick an
// account in it — shared by every command that works on the root
// itself (`run`, `init`, `stop`). Kept separate from the persistent
// global flags so client-only commands don't offer --config / --data-dir.
func addDataDirFlags(c *cobra.Command) {
	c.Flags().StringVar(&flags.ConfigPath, "config", "", "path to config.yaml")
	c.Flags().StringVar(&flags.DataDir, "data-dir", "", "override data directory root (default ~/.any)")
	c.Flags().StringVar(&flags.Account, "account", "", "account id to use when the data dir holds several (standalone only)")
}

// addServerFlags attaches the server-side flags used by `run` and `init`.
func addServerFlags(c *cobra.Command) {
	addDataDirFlags(c)
	c.Flags().StringVar(&flags.WalletPath, "wallet", "", "override wallet file path")
	c.Flags().StringVar(&flags.LogLevel, "log-level", "", "override default log level")
	c.Flags().BoolVar(&flags.PasskeyStdin, "passkey-stdin", false, "read wallet passkey from stdin")
}

func configFlags() config.Flags {
	return config.Flags{
		ConfigPath: flags.ConfigPath,
		DataDir:    flags.DataDir,
		Mode:       flags.Mode,
		Account:    flags.Account,
		Addr:       flags.Addr,
		WalletPath: flags.WalletPath,
		LogLevel:   flags.LogLevel,
	}
}
