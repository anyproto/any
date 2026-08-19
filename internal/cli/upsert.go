package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// newUpsertCmd is `any upsert` — POST /v1/spaces/:spaceId/upsert,
// schema-driven batch ingest into an id:user dataset. Partial success
// prints the result with rejections[] and exits 0 (the 200 stance).
func newUpsertCmd() *cobra.Command {
	var (
		dataset  string
		records  string
		pageSize int
		traceIds []string
	)
	cmd := &cobra.Command{
		Use:   "upsert <spaceId> <objectId>",
		Short: "batch-upsert records into an id:user dataset",
		Long: `Upsert records by caller-supplied id (the idempotency key):
absent records are created, present ones diff only declared-mutable
fields, identical ones are skipped — re-running a batch is a no-op.

--records is a JSON array of {"id": "...", "fields": {...}}:
  any upsert S OBJ --dataset articles --records '[
    {"id": "a1", "fields": {"title": "Hello", "body": "..."}}]'`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var recs []api.UpsertRecord
			if err := readJSONBody(records, &recs); err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.Upsert(cmd.Context(), args[0], api.UpsertRequest{
				ObjectId: args[1],
				Dataset:  dataset,
				Records:  recs,
				PageSize: pageSize,
				TraceIds: traceIds,
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&dataset, "dataset", "", "target dataset (required, declared with idRule \"user\")")
	cmd.Flags().StringVar(&records, "records", "", "records array (inline JSON, @FILE, or - for stdin)")
	cmd.Flags().IntVar(&pageSize, "page-size", 0, "records per emitted change (0 = server default 500)")
	cmd.Flags().StringSliceVar(&traceIds, "trace-id", nil, "trace id(s) to ride the changes (repeatable)")
	_ = cmd.MarkFlagRequired("dataset")
	return cmd
}
