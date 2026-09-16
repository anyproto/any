package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// `any type part …` — the parts of a type: display units owning the
// datasets a module serves. Mirrors /v1/spaces/:id/types/:typeId/parts.
func newTypePartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "part",
		Short: "list / add / patch / remove a type's parts (display units owning datasets)",
	}
	cmd.AddCommand(
		newTypePartListCmd(),
		newTypePartAddCmd(),
		newTypePartPatchCmd(),
		newTypePartRemoveCmd(),
		newTypeDatasetCmd(),
	)
	return cmd
}

func newTypePartListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <spaceId> <typeId>",
		Short: "list a type's parts with their datasets",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.TypeParts(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newTypePartAddCmd() *cobra.Command {
	var draft string
	cmd := &cobra.Command{
		Use:   "add <spaceId> <typeId>",
		Short: "declare a part with its datasets from a JSON draft",
		Long: `Declare a part (api.PartDraftRequest shape) — one change for the part
and every dataset under it:
  {"key": "body", "name": "Description", "pos": "a0",
   "ui": {"type": "document"},
   "datasets": [{"module": "editor", "shared": true}]}
  {"key": "transcript", "name": "Transcript", "ui": {"type": "table"},
   "uses": ["speakers"],
   "datasets": [{"key": "segments", "idRule": "user",
     "fields": [{"key": "time", "kind": "number"},
                {"key": "speaker", "kind": "string"},
                {"key": "text", "kind": "string", "mutableBy": "any"}]}]}
A shared dataset is the module's canonical collection (editor_blocks,
chat_messages); a namespaced one lives in <typeId>_<key>. The part key
is pinned; name, icon, pos, hidden, ui and uses patch via 'type part
patch'; datasets are added with 'type part dataset add'.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.PartDraftRequest
			if err := readJSONBody(draft, &req); err != nil {
				return err
			}
			cl := newClient(flags.Timeout)
			out, err := cl.TypeAddPart(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&draft, "draft", "", "part draft (inline JSON, @FILE, or - for stdin)")
	return cmd
}

func newTypePartPatchCmd() *cobra.Command {
	var (
		setRaw   string
		unsetRaw []string
	)
	cmd := &cobra.Command{
		Use:   "patch <spaceId> <typeId> <partId>",
		Short: "PATCH a part's display slice",
		Long: `Mutable paths: name, icon, pos (strings), hidden (boolean), ui (an
object, replaced whole), uses (an array of dataset keys). The key is
pinned.

Examples:
  any type part patch S T P --set '{"name":"Summary","pos":"a2"}'
  any type part patch S T P --set '{"ui":{"type":"board","config":{"groupBy":"stage"}}}'
  any type part patch S T P --unset icon`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.PartPatchRequest{}
			if setRaw != "" {
				if err := json.Unmarshal([]byte(setRaw), &req.Set); err != nil {
					return fmt.Errorf("--set is not valid JSON: %w", err)
				}
			}
			req.Unset = unsetRaw
			cl := newClient(flags.Timeout)
			return cl.TypePatchPart(cmd.Context(), args[0], args[1], args[2], req)
		},
	}
	cmd.Flags().StringVar(&setRaw, "set", "", "JSON map of path → new value")
	cmd.Flags().StringSliceVar(&unsetRaw, "unset", nil, "path(s) to remove (repeatable)")
	return cmd
}

func newTypePartRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <spaceId> <typeId> <partId>",
		Aliases: []string{"delete", "rm"},
		Short:   "remove a part and its datasets (record data is not cleaned up)",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.TypeRemovePart(cmd.Context(), args[0], args[1], args[2])
		},
	}
}

// newTypeUpdateCmd — `any type update`: PATCH a type's display and
// rendering metadata. cobra's Changed distinguishes "flag absent"
// (keep) from "flag set to empty" (clear).
func newTypeUpdateCmd() *cobra.Command {
	var (
		name, desc, iconCID string
		layoutRaw           string
		hidden              bool
		metaRaw             []string
	)
	cmd := &cobra.Command{
		Use:   "update <spaceId> <typeId>",
		Short: "PATCH a type's name, description, icon or layout",
		Long: `Absent flags keep the current value; an empty string clears a text
field; --layout '' clears the layout.

Examples:
  any type update S T --name Meeting --layout '{"type":"page"}'
  any type update S T --layout '{"type":"tabs"}'`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.TypePatchRequest{}
			if cmd.Flags().Changed("name") {
				req.Name = &name
			}
			if cmd.Flags().Changed("description") {
				req.Description = &desc
			}
			if cmd.Flags().Changed("icon") {
				req.IconCID = &iconCID
			}
			if cmd.Flags().Changed("layout") {
				if layoutRaw == "" {
					req.Layout = json.RawMessage("null")
				} else {
					if !json.Valid([]byte(layoutRaw)) {
						return fmt.Errorf("--layout is not valid JSON")
					}
					req.Layout = json.RawMessage(layoutRaw)
				}
			}
			if cmd.Flags().Changed("hidden") {
				req.Hidden = &hidden
			}
			if len(metaRaw) > 0 {
				meta, err := parseMetaFlags(metaRaw)
				if err != nil {
					return err
				}
				req.Meta = meta // nil value = unset
			}
			if req.Name == nil && req.Description == nil && req.IconCID == nil && req.Layout == nil &&
				req.Hidden == nil && len(req.Meta) == 0 {
				return fmt.Errorf("nothing to update: pass at least one of --name, --description, --icon, --layout, --hidden, --meta")
			}
			cl := newClient(flags.Timeout)
			return cl.TypePatch(cmd.Context(), args[0], args[1], req)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&desc, "description", "", "description")
	cmd.Flags().StringVar(&iconCID, "icon", "", "icon CID")
	cmd.Flags().StringVar(&layoutRaw, "layout", "", `layout descriptor JSON, e.g. '{"type":"page"}' ('' clears)`)
	cmd.Flags().BoolVar(&hidden, "hidden", false, "hide (--hidden) or unhide (--hidden=false) the type")
	cmd.Flags().StringArrayVar(&metaRaw, "meta", nil, "consumer flag key=value (repeatable; key= unsets)")
	return cmd
}
