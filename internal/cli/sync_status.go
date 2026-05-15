package cli

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
)

// `any sync-status ...` — production-shape sync state from the SDK.
// One-shot reads + push streams; `peers` stays unimplemented until
// the SDK lands the production list (use `any debug space` for the
// diagnostic equivalent today).
func newSyncStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync-status",
		Short: "per-space / per-object sync state (production surface)",
	}
	cmd.AddCommand(
		newSyncStatusSpaceCmd(),
		newSyncStatusObjectCmd(),
		newSyncStatusSubscribeCmd(),
	)
	return cmd
}

func newSyncStatusSpaceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "space <spaceId>",
		Short: "rolled-up sync state for one space",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.SyncStatusSpace(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newSyncStatusObjectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "object <spaceId> <objectId>",
		Short: "sync state for one object (unknown ids return state=unknown)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.SyncStatusObject(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

// newSyncStatusSubscribeCmd: one command for both stream flavors.
// No args → account-wide stream covering every known space.
// Two args (spaceId objectId) → per-object stream.
//
// Same JSON-per-line frame wrapper as `any subscribe`.
func newSyncStatusSubscribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subscribe [<spaceId> <objectId>]",
		Short: "SSE stream of sync-state transitions (no args: account-wide; two args: per-object)",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, 0) // timeout doesn't apply to streams
			enc := json.NewEncoder(os.Stdout)

			handle := func(f client.SSEFrame) error {
				if f.Event == "" {
					return nil
				}
				out := struct {
					Event string          `json:"event"`
					Data  json.RawMessage `json:"data,omitempty"`
				}{Event: f.Event, Data: json.RawMessage(f.Data)}
				return enc.Encode(out)
			}

			ctx := cmd.Context()
			switch len(args) {
			case 0:
				return cl.StreamSyncStatusAccount(ctx, handle)
			case 2:
				return cl.StreamSyncStatusObject(ctx, args[0], args[1], handle)
			default:
				return cmd.Usage()
			}
		},
	}
}
