package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// `any space ...` — space-level operations that don't fit chat /
// editor / members / acl. v1 surface is just `update` (rename /
// description / icon) — list / create / delete still go through
// direct HTTP or the web UI.
func newSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "space",
		Short: "space-level operations (metadata update, lookup)",
	}

	cmd.AddCommand(newSpaceGetCmd(), newSpaceUpdateCmd())
	return cmd
}

func newSpaceGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <spaceId>",
		Short: "fetch one space's metadata (includes spaceIndexObjectId)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.SpaceGet(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newSpaceUpdateCmd() *cobra.Command {
	var name, desc, iconCID string
	cmd := &cobra.Command{
		Use:   "update <spaceId>",
		Short: "patch space metadata; pass --name='' to clear a field, omit a flag to leave it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.SpaceUpdateRequest{}
			// Cobra's `Changed` distinguishes "flag absent" from
			// "flag set to empty" — that's exactly the pointer-to-
			// string semantics PATCH /v1/spaces/:id expects.
			if cmd.Flags().Changed("name") {
				req.Name = &name
			}
			if cmd.Flags().Changed("description") {
				req.Description = &desc
			}
			if cmd.Flags().Changed("icon-cid") {
				req.IconCID = &iconCID
			}
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.SpaceUpdate(cmd.Context(), args[0], req)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new display name (set --name='' to clear)")
	cmd.Flags().StringVar(&desc, "description", "", "new description")
	cmd.Flags().StringVar(&iconCID, "icon-cid", "", "new icon CID")
	return cmd
}
