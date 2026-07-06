package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
)

// `any debug ...` — diagnostic snapshots from the SDK's Space.Debug()
// surface. Not stable; intended for inspection / tooling. Production
// callers should prefer `any sync-status ...` (server returns 501
// until the SDK lands the production methods).
func newDebugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "diagnostic snapshots (not stable — see `any sync-status` for production)",
	}
	cmd.AddCommand(newDebugSpaceCmd(), newDebugObjectCmd(), newDebugP2PCmd())
	return cmd
}

func newDebugP2PCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "p2p",
		Short: "local-network layer snapshot: listener, discovery state, discovered LAN peers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.DebugP2P(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newDebugSpaceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "space <spaceId>",
		Short: "per-peer headsync counters for the space (in-memory; resets on restart)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.DebugSpace(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newDebugObjectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "object <spaceId> <objectId>",
		Short: "per-object tree + sync snapshot (triggers a tree walk; not for high-frequency polling)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.DebugObject(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}
