package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// newEditorCmd is the root for `any editor <subcommand>`. It mirrors
// the HTTP namespace under /v1/spaces/:id/objects/:objectId/editor:
// `editor blocks {list,create,patch,delete}` plus future markdown
// helpers.
//
// For tailing live block changes use the existing
// `any subscribe SPACE OBJ --dataset editor_blocks` — the editor
// endpoints intentionally don't expose a dedicated subscribe.
func newEditorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "editor",
		Short: "atomic blocks + markdown bridge for an object's body",
	}
	cmd.AddCommand(newBlocksCmd())
	return cmd
}

// newBlocksCmd is the `any editor blocks <subcommand>` group — the
// four CRUD verbs line up 1:1 with the HTTP endpoints under
// /v1/spaces/:id/objects/:objectId/editor/blocks.
func newBlocksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blocks",
		Short: "list / create / patch / delete blocks on an object's body",
	}
	cmd.AddCommand(
		newBlocksCreateCmd(),
		newBlocksPatchCmd(),
		newBlocksDeleteCmd(),
	)
	return cmd
}

func newBlocksCreateCmd() *cobra.Command {
	var (
		blockType string
		text      string
		styleRaw  string
		parentId  string
		pos       string
	)
	cmd := &cobra.Command{
		Use:   "create <spaceId> <objectId>",
		Short: "POST a new block",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if blockType == "" {
				return fmt.Errorf("--type is required")
			}
			req := api.BlockCreateRequest{
				Type: blockType,
				Text: text,
			}
			if styleRaw != "" {
				var style map[string]any
				if err := json.Unmarshal([]byte(styleRaw), &style); err != nil {
					return fmt.Errorf("--style is not valid JSON: %w", err)
				}
				req.Style = style
			}
			if parentId != "" || pos != "" {
				req.Nav = &api.BlockNav{ParentId: parentId, Pos: pos}
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.BlocksCreate(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&blockType, "type", "", "block type (paragraph, heading, list_item, code, quote, divider, ...)")
	cmd.Flags().StringVar(&text, "text", "", "inline markdown payload (no block-level syntax)")
	cmd.Flags().StringVar(&styleRaw, "style", "", "JSON object with style metadata (e.g. '{\"level\":2}')")
	cmd.Flags().StringVar(&parentId, "parent", "", "id of the parent block (empty = top-level)")
	cmd.Flags().StringVar(&pos, "pos", "", "lexid for sibling ordering (empty = append to end)")
	return cmd
}

func newBlocksPatchCmd() *cobra.Command {
	var (
		setRaw   string
		unsetRaw []string
	)
	cmd := &cobra.Command{
		Use:   "patch <spaceId> <objectId> <blockId>",
		Short: "PATCH one block — atomic $set / $unset against dotted paths",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.BlockPatchRequest{}
			if setRaw != "" {
				var setMap map[string]json.RawMessage
				if err := json.Unmarshal([]byte(setRaw), &setMap); err != nil {
					return fmt.Errorf("--set is not valid JSON: %w", err)
				}
				req.Set = setMap
			}
			if len(unsetRaw) > 0 {
				req.Unset = unsetRaw
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.BlocksPatch(cmd.Context(), args[0], args[1], args[2], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&setRaw, "set", "", `JSON map of dotted path → new value (e.g. '{"text":"...","style.level":2}')`)
	cmd.Flags().StringSliceVar(&unsetRaw, "unset", nil, `dotted path(s) to $unset (repeatable)`)
	return cmd
}

func newBlocksDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <spaceId> <objectId> <blockId>",
		Short: "DELETE one block (sticky tombstone; children NOT cascaded)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			if err := cl.BlocksDelete(cmd.Context(), args[0], args[1], args[2]); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "deleted")
			return nil
		},
	}
}
