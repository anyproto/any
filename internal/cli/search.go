package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// newSearchCmd: `any search <spaceId> <query>` — search the server's
// local index (FTS + vector hybrid by default). Vector/hybrid quality
// depends on the server's configured embedder; without one the server
// answers FTS-only.
func newSearchCmd() *cobra.Command {
	var (
		scopes  string
		limit   int
		mode    string
		require []string
		exclude []string
		maxData int
	)
	cmd := &cobra.Command{
		Use:   "search <spaceId> <query>",
		Short: "search the space's local index (FTS/vector hybrid)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			req := api.SearchRequest{
				Query:   args[1],
				Limit:   limit,
				Mode:    mode,
				Require: require,
				Exclude: exclude,
				MaxData: maxData,
			}
			if scopes != "" {
				req.Scopes = strings.Split(scopes, ",")
			}
			out, err := cl.Search(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&scopes, "scopes", "", "comma-separated scope filter (basic,chat,agent)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max hits (default 10, max 100)")
	cmd.Flags().StringVar(&mode, "mode", "", "hybrid (default), fts, or vector")
	cmd.Flags().StringArrayVar(&require, "require", nil, "must-have term, enforced in every mode (repeatable; phrase/prefix ok)")
	cmd.Flags().StringArrayVar(&exclude, "exclude", nil, "must-not term, enforced in every mode (repeatable; phrase/prefix ok)")
	cmd.Flags().IntVar(&maxData, "max-data", 0, "max runes of data per hit around the first match (default 512; -1 = whole chunk)")
	return cmd
}
