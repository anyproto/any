package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
)

// `any backlinks` / `any links` — reads over the server's link index
// (docs/13-index.md § Links).

func newBacklinksCmd() *cobra.Command {
	var (
		opts   client.LinkOpts
		target string
	)
	cmd := &cobra.Command{
		Use:   "backlinks [<spaceId> <objectId>]",
		Short: "list what links to an object (or one of its records / property values)",
		Long: `Backlinks of an object in one space, grouped into edges to the object
itself and edges to one of its parts (records, property values). With
--target the read spans every space this server indexes.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			if target != "" {
				if len(args) != 0 {
					return cmd.Usage()
				}
				out, err := cl.BacklinksAll(cmd.Context(), target, opts.Kinds, opts.Limit)
				if err != nil {
					return err
				}
				return printJSON(out)
			}
			if len(args) != 2 {
				return cmd.Usage()
			}
			out, err := cl.Backlinks(cmd.Context(), args[0], args[1], opts)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addLinkFlags(cmd, &opts)
	cmd.Flags().StringVar(&target, "target", "", "canonical any:// target: read across every indexed space instead of one object")
	return cmd
}

func newLinksCmd() *cobra.Command {
	var opts client.LinkOpts
	cmd := &cobra.Command{
		Use:   "links <spaceId> <objectId>",
		Short: "list what an object (or one of its records / property values) links to",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.Links(cmd.Context(), args[0], args[1], opts)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addLinkFlags(cmd, &opts)
	return cmd
}

func addLinkFlags(cmd *cobra.Command, opts *client.LinkOpts) {
	cmd.Flags().StringVar(&opts.Record, "record", "", "narrow to one record (with --dataset)")
	cmd.Flags().StringVar(&opts.Dataset, "dataset", "", "the record's collection")
	cmd.Flags().StringVar(&opts.Prop, "prop", "", "narrow to one property value (propId)")
	cmd.Flags().StringArrayVar(&opts.Kinds, "kind", nil, "edge kind to keep: mention, link, card, embed, relation (repeatable)")
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "max edges (default and max 500)")
}
