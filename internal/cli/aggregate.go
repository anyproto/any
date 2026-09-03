package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newAggregateCmd surfaces both aggregation endpoints behind one
// command, mirroring query-subscribe's arg shape. With `--properties`
// (no objectId/dataset), it runs over the per-space `objects`
// collection; otherwise (objectId, --dataset) is required and it runs
// over a per-object dataset.
func newAggregateCmd() *cobra.Command {
	var (
		dataset    string
		properties bool
		pipeline   string
		groupLimit int
		accumLimit int
		memLimit   int
		explain    bool
	)
	cmd := &cobra.Command{
		Use:   "aggregate <spaceId> [<objectId>]",
		Short: "run a MongoDB-style aggregation pipeline (snapshot)",
		Long: `Run an aggregation pipeline against the running server. With
--properties, POSTs /v1/spaces/:id/objects/aggregate over the per-space
objects collection; otherwise takes <objectId> + --dataset and POSTs
/v1/spaces/:id/aggregate over a per-object dataset.

The pipeline is a JSON array of stages ($match / $sort / $skip /
$limit / $count / $project / $addFields / $unwind / $group) — inline,
@FILE, or - for stdin. See docs/14-aggregation.md for the stage set
and the MongoDB divergences.

  any aggregate SPACE OBJ --dataset chat_messages \
    --pipeline '[{"$group":{"_id":"$creator","n":{"$count":{}}}}]'
  any aggregate SPACE --properties --pipeline '[{"$count":"objects"}]'
`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			spaceId := args[0]

			var stages json.RawMessage
			if err := readJSONBody(pipeline, &stages); err != nil {
				return fmt.Errorf("--pipeline: %w", err)
			}
			body := map[string]any{"pipeline": stages}
			if !properties {
				if len(args) != 2 {
					return fmt.Errorf("objectId is required (or pass --properties for the objects collection)")
				}
				if dataset == "" {
					return fmt.Errorf("--dataset is required")
				}
				body["objectId"] = args[1]
				body["dataset"] = dataset
			}
			if cmd.Flags().Changed("group-limit") {
				body["groupLimit"] = groupLimit
			}
			if cmd.Flags().Changed("accum-limit") {
				body["accumArrayLimit"] = accumLimit
			}
			if cmd.Flags().Changed("memory-limit") {
				body["memoryLimitBytes"] = memLimit
			}
			if explain {
				body["explain"] = true
			}
			raw, err := json.Marshal(body)
			if err != nil {
				return err
			}

			cl := newClient(flags.Timeout)
			ctx := cmd.Context()
			if properties {
				out, err := cl.AggregateObjects(ctx, spaceId, raw)
				if err != nil {
					return err
				}
				return printJSON(out)
			}
			out, err := cl.Aggregate(ctx, spaceId, raw)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&dataset, "dataset", "", "dataset name (required without --properties)")
	cmd.Flags().BoolVar(&properties, "properties", false, "aggregate over the per-space objects collection instead of a per-object dataset")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "JSON array of pipeline stages (inline, @FILE, or - for stdin)")
	cmd.Flags().IntVar(&groupLimit, "group-limit", 0, "max unique $group keys (negative = unlimited; default: server-side 50000)")
	cmd.Flags().IntVar(&accumLimit, "accum-limit", 0, "max $push/$addToSet array length (negative = unlimited; default: server-side 10000)")
	cmd.Flags().IntVar(&memLimit, "memory-limit", 0, "blocking-stage memory budget in bytes (negative = unlimited; default: server-side 256MiB)")
	cmd.Flags().BoolVar(&explain, "explain", false, "return the access plan instead of results (diagnostic, not stable)")
	return cmd
}
