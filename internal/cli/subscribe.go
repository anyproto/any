package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
)

// newSubscribeCmd surfaces both /subscribe endpoints behind one
// command. With --properties (and no objectId) it opens the per-space
// property-firehose; otherwise the (objectId, --dataset) pair is
// required and it opens the per-(object, dataset) listener.
//
// Output is one JSON object per SSE frame on stdout. Each line is
// `{"event": "<name>", "data": <payload>}` — keeping the wrapper
// makes the stream easy to consume with jq:
//
//	any subscribe SPACE OBJ --dataset objects | jq 'select(.event=="changes")'
func newSubscribeCmd() *cobra.Command {
	var (
		dataset    string
		properties bool
	)
	cmd := &cobra.Command{
		Use:   "subscribe <spaceId> [<objectId>]",
		Short: "open an SSE stream of CRDT apply events",
		Long: `Stream CRDT apply events from the running server over Server-Sent
Events. With --properties, opens the per-space property-firehose; otherwise
takes <objectId> + --dataset and opens an explicit (object, dataset) stream.

Each SSE frame is emitted as a single JSON line on stdout:

  {"event": "ready",   "data": {}}
  {"event": "changes", "data": [{"spaceId":"...","addSeq":42,...}]}
  {"event": "lagged",  "data": {"total": 3}}
  {"event": "closed",  "data": {"reason": "server_shutdown"}}
`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			spaceId := args[0]

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
			if properties {
				if len(args) != 1 {
					return fmt.Errorf("--properties takes only the spaceId argument")
				}
				return cl.StreamSubscribeProperties(ctx, spaceId, handle)
			}
			if len(args) != 2 {
				return fmt.Errorf("objectId is required (or pass --properties for the per-space firehose)")
			}
			if dataset == "" {
				return fmt.Errorf("--dataset is required")
			}
			return cl.StreamSubscribeObject(ctx, spaceId, args[1], dataset, handle)
		},
	}
	cmd.Flags().StringVar(&dataset, "dataset", "", "dataset name (required without --properties)")
	cmd.Flags().BoolVar(&properties, "properties", false, "open the per-space property-firehose instead of an (object, dataset) stream")
	return cmd
}
