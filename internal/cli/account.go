package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// `any account ...` — account-level operations that aren't scoped to
// any single space. Mirrors sdk.Account() in the SDK.
func newAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "account-level operations (profile, identity)",
	}

	var name, desc, iconCID string
	setMeta := &cobra.Command{
		Use:   "set-metadata",
		Short: "publish profile (name / description / iconCid) across every space",
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.AccountUpdateMetadata(cmd.Context(), api.AccountMetadata{
				Name:        name,
				Description: desc,
				IconCID:     iconCID,
			})
		},
	}
	setMeta.Flags().StringVar(&name, "name", "", "display name")
	setMeta.Flags().StringVar(&desc, "description", "", "short bio / description")
	setMeta.Flags().StringVar(&iconCID, "icon-cid", "", "file CID for the avatar (resolved via the file service)")

	redeem := &cobra.Command{
		Use:   "redeem <code>",
		Short: "redeem an alpha invite code to raise this account's limits",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			res, err := cl.AccountRedeemAccessCode(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(res)
		},
	}

	cmd.AddCommand(setMeta, redeem)
	return cmd
}
