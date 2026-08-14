package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// newTypeCmd is the root for `any type <subcommand>` — mirrors the HTTP
// namespace under /v1/spaces/:id/types. Type create/list plus the
// property sub-group (list / add / patch / remove / option).
func newTypeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "type",
		Short: "types + property definitions in a space",
	}
	cmd.AddCommand(
		newTypeCreateCmd(),
		newTypeListCmd(),
		newTypePropertyCmd(),
		newTypeDatasetCmd(),
	)
	return cmd
}

func newTypeCreateCmd() *cobra.Command {
	var name, desc, iconCID, xkey string
	cmd := &cobra.Command{
		Use:   "create <spaceId>",
		Short: "create a user-defined type",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.TypesCreate(cmd.Context(), args[0], api.TypesCreateRequest{
				Name: name, Description: desc, IconCID: iconCID, XKey: xkey,
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&desc, "description", "", "description")
	cmd.Flags().StringVar(&iconCID, "icon-cid", "", "icon CID")
	cmd.Flags().StringVar(&xkey, "xkey", "", "stable programmatic key (required, unique per space)")
	return cmd
}

func newTypeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <spaceId>",
		Short: "list types in a space",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.TypesList(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

// newTypePropertyCmd is `any type property <subcommand>` — the property
// verbs line up with the HTTP endpoints under
// /v1/spaces/:id/types/:typeId/properties.
func newTypePropertyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "property",
		Aliases: []string{"prop"},
		Short:   "list / add / patch / remove property definitions",
	}
	cmd.AddCommand(
		newTypePropertyListCmd(),
		newTypePropertyAddCmd(),
		newTypePropertyPatchCmd(),
		newTypePropertyRemoveCmd(),
		newTypePropertyOptionCmd(),
	)
	return cmd
}

func newTypePropertyListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <spaceId> <typeId>",
		Short: "list a type's property definitions",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.TypeListProperties(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newTypePropertyAddCmd() *cobra.Command {
	var name, xkey, kind, formatType, formatUI, scope string
	cmd := &cobra.Command{
		Use:   "add <spaceId> <typeId>",
		Short: "add a property (use --format-type select|multiselect for option properties)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.AddPropertyRequest{Name: name, XKey: xkey, Kind: kind, Scope: scope}
			if formatType != "" {
				req.Format = &api.PropertyFormat{Type: formatType, UI: formatUI}
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.TypeAddProperty(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&xkey, "xkey", "", "stable programmatic key")
	cmd.Flags().StringVar(&kind, "kind", "", "value kind (string/number/boolean/array/object; omit to default from format)")
	cmd.Flags().StringVar(&formatType, "format-type", "", "format: links/date/datetime/select/multiselect")
	cmd.Flags().StringVar(&formatUI, "format-ui", "", "presentation hint: select/multiselect/link/links")
	cmd.Flags().StringVar(&scope, "scope", "", "write/sync class: synced (default) / account / local")
	return cmd
}

func newTypePropertyPatchCmd() *cobra.Command {
	var (
		setRaw   string
		unsetRaw []string
	)
	cmd := &cobra.Command{
		Use:   "patch <spaceId> <typeId> <propId>",
		Short: "PATCH a property — {set,unset} over dotted paths (rename, options, colors, order)",
		Long: `Patch a property definition via dotted paths.

Mutable paths: name, description, xKey, xKind, meta.<k>, format.ui,
format.filter, format.meta.<k>, format.options.<key>.{name,color,pos},
format.options.<key>.meta.<k>. Pinned paths (kind, scope, items,
properties, format, format.type) are rejected.

Examples:
  any type property patch S T P --set '{"name":"Priority"}'
  any type property patch S T P --set '{"format.options.high.name":"High","format.options.high.color":"red","format.options.high.pos":"a0"}'
  any type property patch S T P --unset format.options.high`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.PropertyPatchRequest{}
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
			return cl.TypePatchProperty(cmd.Context(), args[0], args[1], args[2], req)
		},
	}
	cmd.Flags().StringVar(&setRaw, "set", "", `JSON map of dotted path → new value`)
	cmd.Flags().StringSliceVar(&unsetRaw, "unset", nil, `dotted path(s) to remove (repeatable)`)
	return cmd
}

func newTypePropertyRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <spaceId> <typeId> <propId>",
		Aliases: []string{"delete", "rm"},
		Short:   "remove a property definition (values are not cleaned up)",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.TypeRemoveProperty(cmd.Context(), args[0], args[1], args[2])
		},
	}
}

// newTypePropertyOptionCmd is convenience sugar over `property patch`
// for select/multiselect options.
func newTypePropertyOptionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "option",
		Short: "set / delete a select-property option (sugar over patch)",
	}
	cmd.AddCommand(newTypePropertyOptionSetCmd(), newTypePropertyOptionDeleteCmd())
	return cmd
}

func newTypePropertyOptionSetCmd() *cobra.Command {
	var name, color, pos string
	cmd := &cobra.Command{
		Use:   "set <spaceId> <typeId> <propId> <key>",
		Short: "add or update an option's name/color/pos leaves",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			set := map[string]json.RawMessage{}
			add := func(leaf, val string) error {
				b, err := json.Marshal(val)
				if err != nil {
					return err
				}
				set[fmt.Sprintf("format.options.%s.%s", args[3], leaf)] = b
				return nil
			}
			if cmd.Flags().Changed("name") {
				if err := add("name", name); err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("color") {
				if err := add("color", color); err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("pos") {
				if err := add("pos", pos); err != nil {
					return err
				}
			}
			if len(set) == 0 {
				return fmt.Errorf("at least one of --name/--color/--pos is required")
			}
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.TypePatchProperty(cmd.Context(), args[0], args[1], args[2], api.PropertyPatchRequest{Set: set})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "option display label")
	cmd.Flags().StringVar(&color, "color", "", "option color")
	cmd.Flags().StringVar(&pos, "pos", "", "option lexid order key")
	return cmd
}

// newTypeDatasetCmd is `any type dataset <subcommand>` — the runtime
// dataset-schema verbs, 1:1 with the endpoints under
// /v1/spaces/:id/types/:typeId/datasets. Data flows through the
// existing modify/query surface plus `any upsert` for id:user datasets.
func newTypeDatasetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "dataset",
		Aliases: []string{"ds"},
		Short:   "list / add / patch / remove runtime dataset definitions",
	}
	cmd.AddCommand(
		newTypeDatasetListCmd(),
		newTypeDatasetAddCmd(),
		newTypeDatasetPatchCmd(),
		newTypeDatasetRemoveCmd(),
		newTypeDatasetFieldCmd(),
	)
	return cmd
}

func newTypeDatasetListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <spaceId> <typeId>",
		Short: "list a type's runtime dataset definitions",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.TypeDatasets(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newTypeDatasetAddCmd() *cobra.Command {
	var draft string
	cmd := &cobra.Command{
		Use:   "add <spaceId> <typeId>",
		Short: "define a dataset on a type from a JSON draft",
		Long: `Define a runtime dataset (api.DatasetDraftRequest shape):
  {"name": "articles", "idRule": "user", "deleteBy": "author",
   "search": {"title": "title", "text": "body"},
   "fields": [
     {"key": "title", "kind": "string", "required": true, "mutableBy": "author"},
     {"key": "body",  "kind": "string", "mutableBy": "author"},
     {"key": "author",    "stamp": "creator"},
     {"key": "createdAt", "stamp": "createTime"},
     {"key": "updatedAt", "stamp": "modifyTime"}]}
Behavioral parts (name, idRule, deleteBy, field kinds/flags) are pinned;
display parts patch via 'type dataset patch'. Declare required fields
here — fields added later cannot be required.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.DatasetDraftRequest
			if err := readJSONBody(draft, &req); err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.TypeAddDataset(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&draft, "draft", "", "dataset draft (inline JSON, @FILE, or - for stdin)")
	return cmd
}

func newTypeDatasetPatchCmd() *cobra.Command {
	var (
		setRaw   string
		unsetRaw []string
	)
	cmd := &cobra.Command{
		Use:   "patch <spaceId> <typeId> <defId>",
		Short: "PATCH a dataset definition's display leaves",
		Long: `Mutable paths: name, description, displayName, search.title,
search.text. Everything else is pinned — remove and re-add.

Example:
  any type dataset patch S T D --set '{"displayName":"Articles","search.title":"headline"}'`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.DatasetPatchRequest{}
			if setRaw != "" {
				if err := json.Unmarshal([]byte(setRaw), &req.Set); err != nil {
					return fmt.Errorf("--set is not valid JSON: %w", err)
				}
			}
			req.Unset = unsetRaw
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.TypePatchDataset(cmd.Context(), args[0], args[1], args[2], req)
		},
	}
	cmd.Flags().StringVar(&setRaw, "set", "", "JSON map of dotted path → new string value")
	cmd.Flags().StringSliceVar(&unsetRaw, "unset", nil, "dotted path(s) to remove (repeatable)")
	return cmd
}

func newTypeDatasetRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <spaceId> <typeId> <defId>",
		Aliases: []string{"delete", "rm"},
		Short:   "remove a dataset definition (record data is not cleaned up)",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.TypeRemoveDataset(cmd.Context(), args[0], args[1], args[2])
		},
	}
}

func newTypeDatasetFieldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "field",
		Short: "add / remove dataset field definitions (additive evolution)",
	}
	cmd.AddCommand(newTypeDatasetFieldAddCmd(), newTypeDatasetFieldRemoveCmd())
	return cmd
}

func newTypeDatasetFieldAddCmd() *cobra.Command {
	var field string
	cmd := &cobra.Command{
		Use:   "add <spaceId> <typeId> <defId>",
		Short: "append a field to a dataset definition",
		Long: `Append one field (api.DatasetFieldDraft shape):
  {"key": "rating", "kind": "number", "mutableBy": "any"}
Additive fields cannot be required.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.DatasetFieldDraft
			if err := readJSONBody(field, &req); err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.TypeAddDatasetField(cmd.Context(), args[0], args[1], args[2], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&field, "field", "", "field draft (inline JSON, @FILE, or - for stdin)")
	return cmd
}

func newTypeDatasetFieldRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <spaceId> <typeId> <defId> <fieldId>",
		Aliases: []string{"delete", "rm"},
		Short:   "remove a field definition (values stay stored)",
		Args:    cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.TypeRemoveDatasetField(cmd.Context(), args[0], args[1], args[2], args[3])
		},
	}
}

func newTypePropertyOptionDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <spaceId> <typeId> <propId> <key>",
		Aliases: []string{"remove", "rm"},
		Short:   "delete an option (unset format.options.<key>)",
		Args:    cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.TypePatchProperty(cmd.Context(), args[0], args[1], args[2], api.PropertyPatchRequest{
				Unset: []string{fmt.Sprintf("format.options.%s", args[3])},
			})
		},
	}
}
