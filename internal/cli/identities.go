package cli

import (
	"github.com/spf13/cobra"
)

// `any identities ...` — the account-global identities directory
// (SDK.Identities()): every account identity this account has
// encountered, with the last resolved profile and the spaces where each
// was seen. Account-scoped — not tied to any single space. Roles
// (owner/admin/writer/etc.) are NOT here; read those from
// `any members list <spaceId>`.
func newIdentitiesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "identities",
		Aliases: []string{"contacts"},
		Short:   "account-global directory of known identities (profiles, shared spaces)",
	}
	cmd.AddCommand(
		newIdentitiesListCmd(),
		newIdentityGetCmd(),
		newIdentitiesSubscribeCmd(),
	)
	return cmd
}

func newIdentitiesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list every known identity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.IdentitiesList(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newIdentityGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <identity>",
		Short: "get one known identity by account address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.IdentityGet(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

// newIdentitiesSubscribeCmd: SSE stream of directory changes. Same
// JSON-per-line frame wrapper as `any subscribe` / `any sync-status
// subscribe`.
func newIdentitiesSubscribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subscribe",
		Short: "SSE stream of directory changes (added / updated / removed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(0) // timeout doesn't apply to streams
			return cl.StreamIdentities(cmd.Context(), jsonFrameHandler())
		},
	}
}
