package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
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
	cmd.AddCommand(newBlocksCmd(), newEditorEditCmd())
	return cmd
}

// newEditorEditCmd is `any editor edit` — targeted oldText → newText
// replacements against the rendered markdown
// (PATCH .../editor/markdown). One pair via --old/--new, or a JSON
// batch via --edits.
func newEditorEditCmd() *cobra.Command {
	var (
		collection string
		oldText    string
		newText    string
		all        bool
		editsRaw   string
	)
	cmd := &cobra.Command{
		Use:   "edit <spaceId> <objectId>",
		Short: "PATCH the rendered markdown — exact oldText → newText replacements",
		Long: `Applies targeted replacements against the object's rendered markdown.
Each oldText must match the current rendering exactly (whole-line
fuzzy fallback tolerates unicode punctuation and trailing whitespace)
and, unless --all / "replaceAll" is set, occur exactly once. All
edits match against the original document and must not overlap;
any failing edit rejects the whole request.

Examples:
  any editor edit SPACE OBJ --old '- [ ] buy milk' --new '- [x] buy milk'
  any editor edit SPACE OBJ --edits '[{"oldText":"a","newText":"b"}]'
  any editor edit SPACE OBJ --edits @edits.json
`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.MarkdownEditRequest
			switch {
			case editsRaw != "":
				if oldText != "" || newText != "" || all {
					return fmt.Errorf("--edits is mutually exclusive with --old/--new/--all")
				}
				if err := readJSONBody(editsRaw, &req.Edits); err != nil {
					return fmt.Errorf("--edits: %w", err)
				}
			case oldText != "":
				req.Edits = []api.MarkdownEdit{{OldText: oldText, NewText: newText, ReplaceAll: all}}
			default:
				return fmt.Errorf("supply --old TEXT (with --new) or --edits JSON")
			}
			cl := newClient(flags.Timeout)
			out, err := cl.MarkdownEdit(cmd.Context(), args[0], args[1], collection, req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addCollectionFlag(cmd, &collection)
	cmd.Flags().StringVar(&oldText, "old", "", "exact text to replace (must be unique unless --all)")
	cmd.Flags().StringVar(&newText, "new", "", "replacement text (empty = delete the matched text)")
	cmd.Flags().BoolVar(&all, "all", false, "replace every occurrence instead of requiring a unique match")
	cmd.Flags().StringVar(&editsRaw, "edits", "", `JSON array of {"oldText","newText","replaceAll"?} (inline, @FILE, or - for stdin)`)
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
		collection string
		blockType  string
		text       string
		styleRaw   string
		parentId   string
		pos        string
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
			cl := newClient(flags.Timeout)
			out, err := cl.BlocksCreate(cmd.Context(), args[0], args[1], collection, req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addCollectionFlag(cmd, &collection)
	cmd.Flags().StringVar(&blockType, "type", "", "block type (paragraph, heading, list_item, code, quote, divider, ...)")
	cmd.Flags().StringVar(&text, "text", "", "inline markdown payload (no block-level syntax)")
	cmd.Flags().StringVar(&styleRaw, "style", "", "JSON object with style metadata (e.g. '{\"level\":2}')")
	cmd.Flags().StringVar(&parentId, "parent", "", "id of the parent block (empty = top-level)")
	cmd.Flags().StringVar(&pos, "pos", "", "lexid for sibling ordering (empty = append to end)")
	return cmd
}

func newBlocksPatchCmd() *cobra.Command {
	var (
		collection string
		setRaw     string
		unsetRaw   []string
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
			cl := newClient(flags.Timeout)
			out, err := cl.BlocksPatch(cmd.Context(), args[0], args[1], collection, args[2], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addCollectionFlag(cmd, &collection)
	cmd.Flags().StringVar(&setRaw, "set", "", `JSON map of dotted path → new value (e.g. '{"text":"...","style.level":2}')`)
	cmd.Flags().StringSliceVar(&unsetRaw, "unset", nil, `dotted path(s) to $unset (repeatable)`)
	return cmd
}

func newBlocksDeleteCmd() *cobra.Command {
	var collection string
	cmd := &cobra.Command{
		Use:   "delete <spaceId> <objectId> <blockId>",
		Short: "DELETE one block (sticky tombstone; children NOT cascaded)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.BlocksDelete(cmd.Context(), args[0], args[1], collection, args[2])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addCollectionFlag(cmd, &collection)
	return cmd
}

// addCollectionFlag adds --collection: the editor collection the
// command writes — the canonical editor_blocks a type shares (the
// default), or a namespaced <typeId>_<key> instance a part declares.
func addCollectionFlag(cmd *cobra.Command, dst *string) {
	cmd.Flags().StringVar(dst, "collection", api.CollectionEditorBlocks,
		"editor collection: editor_blocks (shared) or a namespaced <typeId>_<key> instance")
}
