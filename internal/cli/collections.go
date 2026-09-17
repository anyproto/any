package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// newCollectionCmd is the root for `any collection <subcommand>` —
// mirrors the HTTP namespace under /v1/spaces/:id/collections. A
// collection is a type without parts or layout: the column group an
// object is filed under next to its one type. Collection
// create/list/get/update plus the property sub-group (list / add /
// patch / remove), the same property surface `any type property`
// serves.
func newCollectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "collection",
		Short: "collections and their property definitions in a space",
	}
	cmd.AddCommand(
		newCollectionCreateCmd(),
		newCollectionListCmd(),
		newCollectionGetCmd(),
		newCollectionUpdateCmd(),
		newCollectionPropertyCmd(),
	)
	return cmd
}

func newCollectionCreateCmd() *cobra.Command {
	var (
		name, desc, iconCID, xkey string
		hidden                    bool
		metaRaw                   []string
	)
	cmd := &cobra.Command{
		Use:   "create <spaceId>",
		Short: "create a user-defined collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.CollectionsCreateRequest{Name: name, Description: desc, IconCID: iconCID, XKey: xkey, Hidden: hidden}
			meta, err := parseMetaFlags(metaRaw)
			if err != nil {
				return err
			}
			req.Meta = meta
			cl := newClient(flags.Timeout)
			out, err := cl.CollectionsCreate(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&desc, "description", "", "description")
	cmd.Flags().StringVar(&iconCID, "icon-cid", "", "icon CID")
	cmd.Flags().StringVar(&xkey, "xkey", "", "stable programmatic key (required, unique per space across types and collections)")
	cmd.Flags().BoolVar(&hidden, "hidden", false, "keep the collection out of default listings and pickers")
	cmd.Flags().StringArrayVar(&metaRaw, "meta", nil, "consumer flag key=value (repeatable; value parsed as JSON scalar, else a string)")
	return cmd
}

func newCollectionListCmd() *cobra.Command {
	var includeHidden bool
	cmd := &cobra.Command{
		Use:   "list <spaceId>",
		Short: "list collections in a space",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.CollectionsList(cmd.Context(), args[0], includeHidden)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().BoolVar(&includeHidden, "include-hidden", false, "also list hidden collections (the built-in miniapp / bin, a collection marked hidden)")
	return cmd
}

func newCollectionGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <spaceId> <collectionId>",
		Short: "read one collection, hidden ones included",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.CollectionGet(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

// newCollectionUpdateCmd — `any collection update`: PATCH a
// collection's display and listing metadata. cobra's Changed
// distinguishes "flag absent" (keep) from "flag set to empty" (clear).
func newCollectionUpdateCmd() *cobra.Command {
	var (
		name, desc, iconCID string
		hidden              bool
		metaRaw             []string
	)
	cmd := &cobra.Command{
		Use:   "update <spaceId> <collectionId>",
		Short: "PATCH a collection's name, description, icon or listing flags",
		Long: `Absent flags keep the current value; an empty string clears a text
field. A collection has no layout and no parts.

Examples:
  any collection update S C --name Wiki
  any collection update S C --hidden=false --meta pinned=true`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.CollectionPatchRequest{}
			if cmd.Flags().Changed("name") {
				req.Name = &name
			}
			if cmd.Flags().Changed("description") {
				req.Description = &desc
			}
			if cmd.Flags().Changed("icon") {
				req.IconCID = &iconCID
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
			if req.Name == nil && req.Description == nil && req.IconCID == nil &&
				req.Hidden == nil && len(req.Meta) == 0 {
				return fmt.Errorf("nothing to update: pass at least one of --name, --description, --icon, --hidden, --meta")
			}
			cl := newClient(flags.Timeout)
			return cl.CollectionPatch(cmd.Context(), args[0], args[1], req)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&desc, "description", "", "description")
	cmd.Flags().StringVar(&iconCID, "icon", "", "icon CID")
	cmd.Flags().BoolVar(&hidden, "hidden", false, "hide (--hidden) or unhide (--hidden=false) the collection")
	cmd.Flags().StringArrayVar(&metaRaw, "meta", nil, "consumer flag key=value (repeatable; key= unsets)")
	return cmd
}

// newCollectionPropertyCmd is `any collection property <subcommand>` —
// the property verbs line up with the HTTP endpoints under
// /v1/spaces/:id/collections/:collectionId/properties, the type ones on
// a collection owner.
func newCollectionPropertyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "property",
		Aliases: []string{"prop"},
		Short:   "list / add / patch / remove property definitions",
	}
	cmd.AddCommand(
		newCollectionPropertyListCmd(),
		newCollectionPropertyAddCmd(),
		newCollectionPropertyPatchCmd(),
		newCollectionPropertyRemoveCmd(),
	)
	return cmd
}

func newCollectionPropertyListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <spaceId> <collectionId>",
		Short: "list a collection's property definitions",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.CollectionListProperties(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newCollectionPropertyAddCmd() *cobra.Command {
	var name, desc, xkey, kind, scope, xformat string
	cmd := &cobra.Command{
		Use:   "add <spaceId> <collectionId>",
		Short: "add a property (--kind is pinned; --x-format carries the descriptor)",
		Long: `Add a property definition — the column every object filed under the
collection carries. kind is the guarantee and is pinned; everything
descriptive — slug, icon, order, options, relation targets, per-format
config — is the x-format descriptor (docs/27-descriptors.md).

Examples:
  any collection property add S C --name Stage --xkey stage --kind array \
    --x-format '{"type":"choice","options":{"lead":{"name":"Lead","color":"grey","pos":"a0"}}}'
  any collection property add S C --name Due --xkey due --kind datetime --x-format '{"type":"date"}'`,
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
			out, err := cl.CollectionAddProperty(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&desc, "description", "", "description")
	cmd.Flags().StringVar(&xkey, "xkey", "", "handle, unique within the collection")
	cmd.Flags().StringVar(&kind, "kind", "", "value kind: string/number/boolean/array/object/datetime (required, pinned)")
	cmd.Flags().StringVar(&xformat, "x-format", "", "descriptor (inline JSON, @FILE, or - for stdin)")
	cmd.Flags().StringVar(&scope, "scope", "", "write/sync class: synced (default) / account / local")
	_ = cmd.MarkFlagRequired("kind")
	return cmd
}

func newCollectionPropertyPatchCmd() *cobra.Command {
	var (
		setRaw   string
		unsetRaw []string
	)
	cmd := &cobra.Command{
		Use:   "patch <spaceId> <collectionId> <propId>",
		Short: "PATCH a property — {set,unset} over dotted paths (rename, handle, descriptor)",
		Long: `Patch a property definition via dotted paths — the rules a type's
property patch follows.

Mutable paths: name, description, xKey, meta.index, and every path
under xFormat (type, icon, pos, options.<key>.{name,color,pos},
options.<key>.meta.<k>, relation.{targetTypes,filter}, config.<k>,
vendor subtrees). A set targets a leaf — never an object; a container
(xFormat, options, options.<key>, relation, config) can only be unset.
Pinned paths (kind, scope, items, properties) are rejected.

Examples:
  any collection property patch S C P --set '{"name":"Priority"}'
  any collection property patch S C P --unset xFormat.options.high`,
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
			return cl.CollectionPatchProperty(cmd.Context(), args[0], args[1], args[2], req)
		},
	}
	cmd.Flags().StringVar(&setRaw, "set", "", `JSON map of dotted path → new value`)
	cmd.Flags().StringSliceVar(&unsetRaw, "unset", nil, `dotted path(s) to remove (repeatable)`)
	return cmd
}

func newCollectionPropertyRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <spaceId> <collectionId> <propId>",
		Aliases: []string{"delete", "rm"},
		Short:   "remove a property definition (values are not cleaned up)",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.CollectionRemoveProperty(cmd.Context(), args[0], args[1], args[2])
		},
	}
}
