package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newAggregateCmd surfaces the aggregation endpoints behind one
// command, mirroring query-subscribe's arg shape. With `--properties`
// (no objectId/dataset), it runs over the per-space `objects`
// collection; with `--all-objects` and --dataset (no objectId), over a
// shared dataset across its objects; otherwise (objectId, --dataset)
// is required and it runs over a per-object dataset.
func newAggregateCmd() *cobra.Command {
	var (
		dataset    string
		properties bool
		allObjects bool
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
objects collection; with --all-objects + --dataset, POSTs
/v1/spaces/:id/datasets/aggregate over a shared dataset across its
objects; otherwise takes <objectId> + --dataset and POSTs
/v1/spaces/:id/aggregate over a per-object dataset.

The pipeline is a JSON array of stages ($match / $sort / $skip /
$limit / $count / $project / $addFields / $unwind / $group) — inline,
@FILE, or - for stdin. See docs/14-aggregation.md for the stage set
and the MongoDB divergences.

  any aggregate SPACE OBJ --dataset chat_messages \
    --pipeline '[{"$group":{"_id":"$creator","n":{"$count":{}}}}]'
  any aggregate SPACE --properties --pipeline '[{"$count":"objects"}]'
  any aggregate SPACE --all-objects --dataset TYPE_samples \
    --pipeline '[{"$group":{"_id":"$_objectId","n":{"$count":{}}}}]'
`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			spaceId := args[0]

			var stages json.RawMessage
			if err := readJSONBody(pipeline, &stages); err != nil {
				return fmt.Errorf("--pipeline: %w", err)
			}
			body := map[string]any{"pipeline": stages}
			if err := addressQueryBody(body, properties, allObjects, args, dataset); err != nil {
				return err
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
			if allObjects {
				out, err := cl.AggregateDataset(ctx, spaceId, raw)
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
	cmd.Flags().BoolVar(&allObjects, "all-objects", false, "aggregate over a shared dataset across every object that holds it (with --dataset, no objectId)")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "JSON array of pipeline stages (inline, @FILE, or - for stdin)")
	cmd.Flags().IntVar(&groupLimit, "group-limit", 0, "max unique $group keys (negative = unlimited; default: server-side 50000)")
	cmd.Flags().IntVar(&accumLimit, "accum-limit", 0, "max $push/$addToSet array length (negative = unlimited; default: server-side 10000)")
	cmd.Flags().IntVar(&memLimit, "memory-limit", 0, "blocking-stage memory budget in bytes (negative = unlimited; default: server-side 256MiB)")
	cmd.Flags().BoolVar(&explain, "explain", false, "return the access plan instead of results (diagnostic, not stable)")
	return cmd
}
