package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
	"github.com/anyproto/any/internal/server"
)

// `any auth ...` — authorize a server that started without an account.
// Maps 1:1 onto GET/POST /v1/auth.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "authorize the running server (generate, restore or select an account)",
	}

	var (
		mnemonic      string
		mnemonicStdin bool
		account       string
		index         uint32
	)
	login := &cobra.Command{
		Use:   "login",
		Short: "POST /v1/auth — generate a fresh account, restore one from a mnemonic, or select a local one",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if mnemonicStdin {
				if mnemonic != "" {
					return errors.New("--mnemonic and --mnemonic-stdin are mutually exclusive")
				}
				var err error
				if mnemonic, err = readLine(os.Stdin); err != nil {
					return fmt.Errorf("read mnemonic: %w", err)
				}
			}
			cl := client.New(flags.Addr, flags.Timeout)
			resp, err := cl.Authorize(cmd.Context(), api.AuthRequest{
				Mnemonic:  mnemonic,
				AccountId: account,
				Index:     index,
			})
			if err != nil {
				return err
			}
			// The generated phrase goes to stderr (backup warning),
			// keeping stdout machine-readable like `init`.
			if resp.Mnemonic != "" {
				server.PrintMnemonic(resp.Mnemonic)
				resp = &api.AuthResponse{AccountId: resp.AccountId, Created: resp.Created}
			}
			return printJSON(resp)
		},
	}
	login.Flags().StringVar(&mnemonic, "mnemonic", "", "restore from an existing BIP-39 phrase (prefer --mnemonic-stdin: flags leak into shell history)")
	login.Flags().BoolVar(&mnemonicStdin, "mnemonic-stdin", false, "read the BIP-39 phrase from stdin")
	login.Flags().StringVar(&account, "account", "", "select an account that already has a local wallet")
	login.Flags().Uint32Var(&index, "index", 0, "account derivation index for --mnemonic")

	status := &cobra.Command{
		Use:   "status",
		Short: "GET /v1/auth — authorization state + locally available accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			resp, err := cl.AuthStatus(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(resp)
		},
	}

	cmd.AddCommand(login, status)
	return cmd
}

