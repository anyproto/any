package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/config"
)

// addServerFlags attaches the server-side flags used by `run` and `init`.
// Kept separate from the persistent global flags so CLI-only commands
// don't offer --config / --data-dir.
func addServerFlags(c *cobra.Command) {
	c.Flags().StringVar(&flags.ConfigPath, "config", "", "path to config.yaml")
	c.Flags().StringVar(&flags.DataDir, "data-dir", "", "override data directory root (default ~/.any)")
	c.Flags().StringVar(&flags.Account, "account", "", "account id to use when the data dir holds several")
	c.Flags().StringVar(&flags.WalletPath, "wallet", "", "override wallet file path")
	c.Flags().StringVar(&flags.LogLevel, "log-level", "", "override default log level")
	c.Flags().BoolVar(&flags.PasskeyStdin, "passkey-stdin", false, "read wallet passkey from stdin")
}

func configFlags() config.Flags {
	return config.Flags{
		ConfigPath: flags.ConfigPath,
		DataDir:    flags.DataDir,
		Account:    flags.Account,
		Addr:       flags.Addr,
		WalletPath: flags.WalletPath,
		LogLevel:   flags.LogLevel,
	}
}
