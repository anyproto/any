package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// newTypeCmd is the root for `any type <subcommand>` — mirrors the HTTP
// namespace under /v1/spaces/:id/types. Type create/list/update plus
// the property sub-group (list / add / patch / remove / option) and the
// part sub-group (list / add / patch / remove, with the datasets under
// a part).
func newTypeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "type",
		Short: "types, property definitions and parts in a space",
	}
	cmd.AddCommand(
		newTypeCreateCmd(),
		newTypeListCmd(),
		newTypeUpdateCmd(),
		newTypePropertyCmd(),
		newTypePartCmd(),
	)
	return cmd
}

func newTypeCreateCmd() *cobra.Command {
	var (
		name, desc, iconCID, xkey string
		layoutRaw                 string
		hidden                    bool
		metaRaw                   []string
	)
	cmd := &cobra.Command{
		Use:   "create <spaceId>",
		Short: "create a user-defined type",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.TypesCreateRequest{Name: name, Description: desc, IconCID: iconCID, XKey: xkey, Hidden: hidden}
			if layoutRaw != "" {
				if !json.Valid([]byte(layoutRaw)) {
					return fmt.Errorf("--layout is not valid JSON")
				}
				req.Layout = json.RawMessage(layoutRaw)
			}
			meta, err := parseMetaFlags(metaRaw)
			if err != nil {
				return err
			}
			req.Meta = meta
			cl := newClient(flags.Timeout)
			out, err := cl.TypesCreate(cmd.Context(), args[0], req)
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
	cmd.Flags().StringVar(&layoutRaw, "layout", "", `layout descriptor JSON, e.g. '{"type":"page"}'`)
	cmd.Flags().BoolVar(&hidden, "hidden", false, "keep the type out of default listings and pickers")
	cmd.Flags().StringArrayVar(&metaRaw, "meta", nil, "consumer flag key=value (repeatable; value parsed as JSON scalar, else a string)")
	return cmd
}

// parseMetaFlags turns repeatable key=value flags into the meta bag: a
// value that parses as a JSON scalar (true, 3, "x") is taken as such,
// anything else is a string; an empty value unsets the key.
func parseMetaFlags(raw []string) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(raw))
	for _, kv := range raw {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--meta expects key=value, got %q", kv)
		}
		if v == "" {
			out[k] = nil
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(v), &parsed); err == nil {
			switch parsed.(type) {
			case string, bool, float64:
				out[k] = parsed
				continue
			}
		}
		out[k] = v
	}
	return out, nil
}

func newTypeListCmd() *cobra.Command {
	var includeHidden bool
	cmd := &cobra.Command{
		Use:   "list <spaceId>",
		Short: "list types in a space",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.TypesList(cmd.Context(), args[0], includeHidden)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().BoolVar(&includeHidden, "include-hidden", false, "also list hidden types (a records-hosting bundle root, a type marked hidden)")
	return cmd
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
			cl := newClient(flags.Timeout)
			out, err := cl.TypeListProperties(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newTypePropertyAddCmd() *cobra.Command {
	var name, desc, xkey, kind, scope, xformat string
	cmd := &cobra.Command{
		Use:   "add <spaceId> <typeId>",
		Short: "add a property (--kind is pinned; --x-format carries the descriptor)",
		Long: `Add a property definition. kind is the guarantee and is pinned;
everything descriptive — slug, icon, order, options, relation targets,
per-format config — is the x-format descriptor (docs/27-descriptors.md).

Examples:
  any type property add S T --name Stage --xkey stage --kind array \
    --x-format '{"type":"choice","options":{"lead":{"name":"Lead","color":"grey","pos":"a0"}}}'
  any type property add S T --name Due --xkey due --kind datetime --x-format '{"type":"date"}'
  any type property add S T --name Company --xkey company --kind array \
    --x-format '{"type":"relation","relation":{"targetTypes":["companies"]}}'`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.AddPropertyRequest{Name: name, Description: desc, XKey: xkey, Kind: kind, Scope: scope}
			if xformat != "" {
				var xf json.RawMessage
				if err := readJSONBody(xformat, &xf); err != nil {
					return fmt.Errorf("--x-format: %w", err)
				}
				req.XFormat = xf
			}
			cl := newClient(flags.Timeout)
			out, err := cl.TypeAddProperty(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&desc, "description", "", "description")
	cmd.Flags().StringVar(&xkey, "xkey", "", "handle, unique within the type")
	cmd.Flags().StringVar(&kind, "kind", "", "value kind: string/number/boolean/array/object/datetime (required, pinned)")
	cmd.Flags().StringVar(&xformat, "x-format", "", "descriptor (inline JSON, @FILE, or - for stdin)")
	cmd.Flags().StringVar(&scope, "scope", "", "write/sync class: synced (default) / account / local")
	_ = cmd.MarkFlagRequired("kind")
	return cmd
}

func newTypePropertyPatchCmd() *cobra.Command {
	var (
		setRaw   string
		unsetRaw []string
	)
	cmd := &cobra.Command{
		Use:   "patch <spaceId> <typeId> <propId>",
		Short: "PATCH a property — {set,unset} over dotted paths (rename, handle, descriptor)",
		Long: `Patch a property definition via dotted paths.

Mutable paths: name, description, xKey, meta.index, and every path
under xFormat (type, icon, pos, options.<key>.{name,color,pos},
options.<key>.meta.<k>, relation.{targetTypes,filter}, config.<k>,
vendor subtrees). A set targets a leaf — never an object; a container
(xFormat, options, options.<key>, relation, config) can only be unset.
Pinned paths (kind, scope, items, properties) are rejected.

Examples:
  any type property patch S T P --set '{"name":"Priority"}'
  any type property patch S T P --set '{"xFormat.options.high.name":"High","xFormat.options.high.color":"red","xFormat.options.high.pos":"a0"}'
  any type property patch S T P --set '{"xFormat.config.multiple":true}'
  any type property patch S T P --unset xFormat.options.high`,
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
			cl := newClient(flags.Timeout)
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
			cl := newClient(flags.Timeout)
			return cl.TypeRemoveProperty(cmd.Context(), args[0], args[1], args[2])
		},
	}
}

// newTypePropertyOptionCmd is convenience sugar over `property patch`
// for choice options.
func newTypePropertyOptionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "option",
		Short: "set / delete a choice-property option (sugar over patch)",
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
				set[fmt.Sprintf("xFormat.options.%s.%s", args[3], leaf)] = b
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
			cl := newClient(flags.Timeout)
			return cl.TypePatchProperty(cmd.Context(), args[0], args[1], args[2], api.PropertyPatchRequest{Set: set})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "option display label")
	cmd.Flags().StringVar(&color, "color", "", "option color")
	cmd.Flags().StringVar(&pos, "pos", "", "option lexid order key")
	return cmd
}

// newTypeDatasetCmd is `any type part dataset <subcommand>` — the
// dataset verbs under a part, 1:1 with the endpoints under
// /v1/spaces/:id/types/:typeId/parts/:partId/datasets (add) and
// …/types/:typeId/datasets/:defId (patch / remove / fields). `type
// part list` shows every dataset with its part. Data flows through the
// existing modify/query surface plus `any upsert` for id:user datasets.
func newTypeDatasetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "dataset",
		Aliases: []string{"ds"},
		Short:   "list / add / patch / remove a part's dataset definitions",
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
		Short: "list a type's dataset definitions flat (every part's)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
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
		Use:   "add <spaceId> <typeId> <partId>",
		Short: "declare a dataset on a part from a JSON draft",
		Long: `Declare a dataset on an existing part (api.DatasetDraftRequest shape):
  {"key": "articles", "idRule": "user", "deleteBy": "author",
   "search": {"title": "title", "text": "body", "scope": "news"},
   "fields": [
     {"key": "title", "kind": "string", "required": true, "mutableBy": "author",
      "description": "Headline", "xFormat": {"type": "text", "icon": "heading"}},
     {"key": "body",  "kind": "string", "mutableBy": "author"},
     {"key": "author",    "stamp": "creator"},
     {"key": "createdAt", "stamp": "createTime"},
     {"key": "updatedAt", "stamp": "modifyTime"}]}
search.text is a bare field key or a non-empty array of keys, e.g.
"search": {"title": "subject", "text": ["body", "notes"]} — the index
joins the mapped fields into one body. A field's xFormat is the same
descriptor a property carries (docs/27-descriptors.md).
A module-served dataset names its module instead of fields:
  {"module": "editor", "shared": true}          the shared editor body
  {"key": "summary", "module": "editor"}        a second, namespaced editor
Behavioral parts (key, module, shared, idRule, deleteBy, field
kinds/flags) are pinned; display parts patch via 'type part dataset
patch' and 'type part dataset field patch'. Declare required fields
here — fields added later cannot be required. The reply carries the
computed collection reads and writes address.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.DatasetDraftRequest
			if err := readJSONBody(draft, &req); err != nil {
				return err
			}
			cl := newClient(flags.Timeout)
			out, err := cl.TypeAddDataset(cmd.Context(), args[0], args[1], args[2], req)
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
		Long: `Mutable paths: description, displayName, search.title,
search.text, search.scope. Everything else (key, module, shared, id
rule, delete gate) is pinned — remove and re-add. Values are strings; search.text also takes a non-empty array
of field keys.

Examples:
  any type part dataset patch S T D --set '{"displayName":"Articles","search.title":"headline"}'
  any type part dataset patch S T D --set '{"search.text":["body","notes"]}'`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.DatasetPatchRequest{}
			if setRaw != "" {
				if err := json.Unmarshal([]byte(setRaw), &req.Set); err != nil {
					return fmt.Errorf("--set is not valid JSON: %w", err)
				}
			}
			req.Unset = unsetRaw
			cl := newClient(flags.Timeout)
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
			cl := newClient(flags.Timeout)
			return cl.TypeRemoveDataset(cmd.Context(), args[0], args[1], args[2])
		},
	}
}

func newTypeDatasetFieldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "field",
		Short: "add / patch / remove dataset field definitions (additive evolution)",
	}
	cmd.AddCommand(newTypeDatasetFieldAddCmd(), newTypeDatasetFieldPatchCmd(), newTypeDatasetFieldRemoveCmd())
	return cmd
}

func newTypeDatasetFieldPatchCmd() *cobra.Command {
	var (
		setRaw   string
		unsetRaw []string
	)
	cmd := &cobra.Command{
		Use:   "patch <spaceId> <typeId> <defId> <fieldId>",
		Short: "PATCH a field definition's display leaves and descriptor",
		Long: `Mutable paths: name, description, and every path under xFormat (the
property patch rules — a set targets a leaf, containers are unset-only).
The behavioral declaration (key, kind, shape, scope, required,
mutableBy, stamp) is pinned.

Examples:
  any type part dataset field patch S T D F --set '{"description":"Headline","xFormat.icon":"title"}'
  any type part dataset field patch S T D F --unset xFormat.options.old`,
		Args: cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.DatasetFieldPatchRequest{}
			if setRaw != "" {
				if err := json.Unmarshal([]byte(setRaw), &req.Set); err != nil {
					return fmt.Errorf("--set is not valid JSON: %w", err)
				}
			}
			req.Unset = unsetRaw
			cl := newClient(flags.Timeout)
			return cl.TypePatchDatasetField(cmd.Context(), args[0], args[1], args[2], args[3], req)
		},
	}
	cmd.Flags().StringVar(&setRaw, "set", "", "JSON map of dotted path → new value")
	cmd.Flags().StringSliceVar(&unsetRaw, "unset", nil, "dotted path(s) to remove (repeatable)")
	return cmd
}

func newTypeDatasetFieldAddCmd() *cobra.Command {
	var field string
	cmd := &cobra.Command{
		Use:   "add <spaceId> <typeId> <defId>",
		Short: "append a field to a dataset definition",
		Long: `Append one field (api.DatasetFieldDraft shape):
  {"key": "rating", "kind": "number", "mutableBy": "any",
   "xFormat": {"type": "rating", "config": {"max": 5}}}
Additive fields cannot be required.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.DatasetFieldDraft
			if err := readJSONBody(field, &req); err != nil {
				return err
			}
			cl := newClient(flags.Timeout)
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
			cl := newClient(flags.Timeout)
			return cl.TypeRemoveDatasetField(cmd.Context(), args[0], args[1], args[2], args[3])
		},
	}
}

func newTypePropertyOptionDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <spaceId> <typeId> <propId> <key>",
		Aliases: []string{"remove", "rm"},
		Short:   "delete an option (unset xFormat.options.<key>)",
		Args:    cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.TypePatchProperty(cmd.Context(), args[0], args[1], args[2], api.PropertyPatchRequest{
				Unset: []string{fmt.Sprintf("xFormat.options.%s", args[3])},
			})
		},
	}
}
