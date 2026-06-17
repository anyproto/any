package cli

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// newUICmd: `any ui ...` — drive a connected any-ui window over the
// account-wide UI command channel (in-memory, fire-and-forget). See
// docs/15-ui-commands.md.
func newUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "drive the any-ui window (open space/object)",
	}
	cmd.AddCommand(newUIOpenSpaceCmd(), newUIOpenObjectCmd(), newUISubscribeCmd())
	return cmd
}

func newUIOpenSpaceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open-space <spaceId>",
		Short: "tell the UI to open a space",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.UICommandPublish(cmd.Context(), api.UICommand{
				Action:  api.UICommandOpenSpace,
				SpaceId: args[0],
				Source:  "cli",
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newUIOpenObjectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open-object <spaceId> <objectId>",
		Short: "tell the UI to open an object in a space",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.UICommandPublish(cmd.Context(), api.UICommand{
				Action:   api.UICommandOpenObject,
				SpaceId:  args[0],
				ObjectId: args[1],
				Source:   "cli",
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newUISubscribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subscribe",
		Short: "stream UI commands (one JSON line per frame)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, 0) // timeout doesn't apply to streams
			enc := json.NewEncoder(os.Stdout)
			return cl.StreamUICommands(cmd.Context(), func(f client.SSEFrame) error {
				if f.Event == "" {
					return nil
				}
				out := struct {
					Event string          `json:"event"`
					Data  json.RawMessage `json:"data,omitempty"`
				}{Event: f.Event, Data: json.RawMessage(f.Data)}
				return enc.Encode(out)
			})
		},
	}
}
