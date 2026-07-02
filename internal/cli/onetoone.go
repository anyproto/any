package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// `any one-to-one ...` — 1-1 (direct) spaces: a space shared by exactly
// two identities, derived deterministically from both account keys (same
// id regardless of who initiates). The peer's account identity is the
// `id` from their `any account` output, exchanged out-of-band.
//
// Flow: `start <peer>` reaches out (active immediately, implicit
// self-approval); the other side either discovers the request through the
// coordinator inbox (surfaced automatically as a one_to_one_pending row)
// or has the app `register <peer>` it out-of-band, then `accept` /
// `decline` it. `pending` lists incoming requests awaiting approval.
// Contract: docs/03-api.md § Spaces + the SDK's
// docs/13-one-to-one-spaces.md.
func newOneToOneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "one-to-one",
		Aliases: []string{"1-1", "direct"},
		Short:   "1-1 (direct) spaces (start, accept, decline, register, pending)",
	}
	cmd.AddCommand(
		newOneToOneStartCmd(),
		newOneToOneAcceptCmd(),
		newOneToOneDeclineCmd(),
		newOneToOneRegisterCmd(),
		newOneToOnePendingCmd(),
	)
	return cmd
}

// newOneToOneStartCmd: `any one-to-one start <otherIdentity>` — POST
// /v1/spaces/one-to-one (Service.OneToOne). Initiates or explicitly
// accepts/un-declines the 1-1 with the peer; activates locally.
func newOneToOneStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <otherIdentity>",
		Short: "open a 1-1 space with a peer account identity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.SpaceOneToOne(cmd.Context(), api.SpaceOneToOneRequest{OtherIdentity: args[0]})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

// newOneToOneAcceptCmd: `any one-to-one accept <spaceId>` — POST
// /v1/spaces/:id/one-to-one/accept (Service.AcceptOneToOne). spaceId comes
// from a one_to_one_pending row (`any one-to-one pending`).
func newOneToOneAcceptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "accept <spaceId>",
		Short: "accept an incoming pending 1-1 space",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.SpaceOneToOneAccept(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

// newOneToOneDeclineCmd: `any one-to-one decline <spaceId>` — POST
// /v1/spaces/:id/one-to-one/decline (Service.DeclineOneToOne). Synced,
// account-wide sticky; `start <peer>` later un-declines.
func newOneToOneDeclineCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "decline <spaceId>",
		Short: "decline an incoming pending 1-1 space (sticky)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.SpaceOneToOneDecline(cmd.Context(), args[0])
		},
	}
}

// newOneToOneRegisterCmd: `any one-to-one register <peerIdentity>` — POST
// /v1/spaces/one-to-one/register-incoming (Service.RegisterIncoming). The
// out-of-band discovery path: drop an incoming request the app learned
// through its own channel into a pending row for the user to approve.
func newOneToOneRegisterCmd() *cobra.Command {
	var name, desc, iconCID string
	cmd := &cobra.Command{
		Use:   "register <peerIdentity>",
		Short: "register an out-of-band incoming 1-1 request as pending",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.SpaceRegisterIncoming(cmd.Context(), api.SpaceRegisterIncomingRequest{
				PeerIdentity: args[0],
				DisplayHint:  api.AccountMetadata{Name: name, Description: desc, IconCID: iconCID},
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display-hint name for the pending row")
	cmd.Flags().StringVar(&desc, "description", "", "display-hint description")
	cmd.Flags().StringVar(&iconCID, "icon-cid", "", "display-hint icon CID")
	return cmd
}

// newOneToOnePendingCmd: `any one-to-one pending` — GET
// /v1/spaces?status=one_to_one_pending. Lists incoming 1-1 requests
// awaiting local approval (hidden from the default space list).
func newOneToOnePendingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pending",
		Short: "list incoming pending 1-1 requests to approve",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.SpaceList(cmd.Context(), api.SpaceStatusOneToOnePending)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}
