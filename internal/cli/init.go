package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/server"
)

func newInitCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "init",
		Short: "create the data dir and wallet, then exit",
		Long: `init creates the data directory (mode 0700) and generates a wallet
if one does not already exist. The BIP-39 mnemonic is printed to stderr
once. On subsequent runs init is a no-op (the existing wallet is loaded
silently).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configFlags())
			if err != nil {
				return err
			}
			dataDir, err := config.EnsureDataDir(cfg.DataDir)
			if err != nil {
				return err
			}
			passkey, err := config.ResolvePasskey(cfg, flags.PasskeyStdin)
			if err != nil {
				return err
			}
			provider, firstRun, err := server.OpenWallet(config.WalletPath(cfg, dataDir), passkey)
			if err != nil {
				return err
			}
			if firstRun {
				server.PrintMnemonic(provider.Mnemonic())
			}
			return nil
		},
	}
	addServerFlags(c)
	return c
}
