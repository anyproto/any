package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anyproto/any-sync-sdk/auth"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/server"
)

func newInitCmd() *cobra.Command {
	var (
		mnemonic      string
		mnemonicStdin bool
		index         uint32
		forceNew      bool
	)
	c := &cobra.Command{
		Use:   "init",
		Short: "create the data dir and an account wallet, then exit",
		Long: `init creates the data directory root (mode 0700) and an account
wallet under <root>/<accountId>/wallet.key.

Without flags it generates a fresh account when none exists yet (the
BIP-39 mnemonic is printed to stderr once) and is a no-op otherwise.
--mnemonic / --mnemonic-stdin authorize an existing account: the same
phrase always derives the same account id, while the device key is
freshly generated — the supported way to add a second device. --new
forces an additional fresh account.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configFlags())
			if err != nil {
				return err
			}
			root, err := config.EnsureDataDir(cfg.DataDir)
			if err != nil {
				return err
			}
			if mnemonicStdin && flags.PasskeyStdin {
				return errors.New("--mnemonic-stdin and --passkey-stdin both read stdin — pass one of them another way")
			}
			passkey, err := config.ResolvePasskey(cfg, flags.PasskeyStdin)
			if err != nil {
				return err
			}
			if mnemonicStdin {
				if mnemonic, err = readLine(os.Stdin); err != nil {
					return fmt.Errorf("read mnemonic: %w", err)
				}
			}

			// Explicit wallet override: single-wallet manual mode, no
			// per-account nesting.
			if cfg.Auth.WalletPath != "" {
				return initWallet(cmd, config.WalletPath(cfg, root), passkey, mnemonic, mnemonic == "")
			}

			if mnemonic != "" {
				id, err := auth.AccountId(mnemonic, index)
				if err != nil {
					return err
				}
				if config.HasRootWallet(root) && rootWalletID(cmd, root, passkey) == id {
					fmt.Fprintf(cmd.ErrOrStderr(), "already authorized as %s (default wallet)\n", id)
					return printInitResult(cmd, id, false)
				}
				return initWallet(cmd, config.WalletPath(config.Config{}, config.AccountDir(root, id)), passkey, mnemonic, false)
			}

			accounts, err := config.ListAccounts(root)
			if err != nil {
				return err
			}
			if !forceNew && (config.HasRootWallet(root) || len(accounts) > 0) {
				if config.HasRootWallet(root) {
					accounts = append([]string{rootWalletID(cmd, root, passkey) + " (default)"}, accounts...)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "already initialized — accounts: %s\nuse --new for another account, or --mnemonic to restore one\n",
					strings.Join(accounts, ", "))
				return nil
			}

			fresh, err := auth.GenerateMnemonic()
			if err != nil {
				return err
			}
			id, err := auth.AccountId(fresh, 0)
			if err != nil {
				return err
			}
			return initWallet(cmd, config.WalletPath(config.Config{}, config.AccountDir(root, id)), passkey, fresh, true)
		},
	}
	addServerFlags(c)
	c.Flags().StringVar(&mnemonic, "mnemonic", "", "authorize with an existing BIP-39 phrase (prefer --mnemonic-stdin: flags leak into shell history)")
	c.Flags().BoolVar(&mnemonicStdin, "mnemonic-stdin", false, "read the BIP-39 phrase from stdin")
	c.Flags().Uint32Var(&index, "index", 0, "account derivation index for --mnemonic")
	c.Flags().BoolVar(&forceNew, "new", false, "create an additional fresh account even when accounts already exist")
	return c
}

// initWallet opens-or-creates the wallet and reports
// {accountId, created} on stdout. printPhrase is set when the phrase
// is news to the user (fresh generation, not a restore) so the backup
// warning prints to stderr on first creation.
func initWallet(cmd *cobra.Command, walletPath, passkey, mnemonic string, printPhrase bool) error {
	provider, firstRun, err := server.OpenWallet(walletPath, passkey, mnemonic)
	if err != nil {
		return err
	}
	if firstRun && printPhrase {
		server.PrintMnemonic(provider.Mnemonic())
	}
	id, err := server.AccountID(cmd.Context(), provider)
	if err != nil {
		return err
	}
	return printInitResult(cmd, id, firstRun)
}

func printInitResult(cmd *cobra.Command, accountId string, created bool) error {
	out, err := json.MarshalIndent(map[string]any{"accountId": accountId, "created": created}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(out))
	return nil
}

// rootWalletID best-effort derives the root (default) wallet's account
// id; empty when it can't be opened (e.g. encrypted, wrong passkey).
func rootWalletID(cmd *cobra.Command, root, passkey string) string {
	provider, _, err := server.OpenWallet(config.WalletPath(config.Config{}, root), passkey, "")
	if err != nil {
		return ""
	}
	id, err := server.AccountID(cmd.Context(), provider)
	if err != nil {
		return ""
	}
	return id
}

func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
