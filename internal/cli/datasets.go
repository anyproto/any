package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
)

// newDatasetsCmd: `any datasets [<spaceId>]` — dump dataset schemas as
// JSON Schema (with per-field x-scope). With a spaceId, lists every
// dataset the space hosts (Space.Datasets); without one, lists the
// account's tech-space system datasets — spaces / profile — that back
// the space-list query/subscribe (Service.Datasets).
func newDatasetsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "datasets [<spaceId>]",
		Short: "list dataset schemas (JSON Schema with per-field x-scope)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			if len(args) == 1 {
				out, err := cl.SpaceDatasets(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return printJSON(out)
			}
			out, err := cl.Datasets(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}
