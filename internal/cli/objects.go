package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// `any object …` — object create plus the two bindings that say what an
// object is and where it is filed: its one type
// (/v1/spaces/:id/properties/:objectId/type) and the collections it
// belongs to (…/properties/:objectId/collections/:collectionId).
func newObjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "object",
		Short: "create objects, set their type, file them under collections",
	}
	cmd.AddCommand(
		newObjectCreateCmd(),
		newObjectTypeCmd(),
		newObjectCollectionCmd(),
	)
	return cmd
}

func newObjectCreateCmd() *cobra.Command {
	var (
		typeId      string
		collections []string
		propsRaw    string
	)
	cmd := &cobra.Command{
		Use:   "create <spaceId>",
		Short: "create an object with its type and its collections",
		Long: `An object has one type — what it is, its parts and its layout — and
any number of collections it is filed under. The type is required
(page is the plain document); collections are optional.

--properties seeds property values keyed owner → propId → value, the
owner being the type or one of the collections.

Examples:
  any object create S --type page --collection wiki
  any object create S --type person --collection contact --collection investor \
    --properties '{"any":{"name":"Ada"}}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.ObjectCreateRequest{Type: typeId, Collections: collections}
			if propsRaw != "" {
				if err := readJSONBody(propsRaw, &req.InitialProperties); err != nil {
					return fmt.Errorf("--properties: %w", err)
				}
			}
			cl := newClient(flags.Timeout)
			out, err := cl.ObjectsCreate(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&typeId, "type", "", "the object's one type (required; page for a plain document)")
	cmd.Flags().StringArrayVar(&collections, "collection", nil, "collection id the object is filed under (repeatable)")
	cmd.Flags().StringVar(&propsRaw, "properties", "", "initial property values (inline JSON, @FILE, or - for stdin)")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

// newObjectTypeCmd is `any object type <subcommand>` — the one type an
// object carries. Setting replaces the previous one; its values stay as
// orphan data.
func newObjectTypeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "type",
		Short: "set the object's one type",
	}
	cmd.AddCommand(newObjectTypeSetCmd())
	return cmd
}

func newObjectTypeSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <spaceId> <objectId> <typeId>",
		Short: "set the object's type, replacing any previous one",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.ObjectSetType(cmd.Context(), args[0], args[1], args[2])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

// newObjectCollectionCmd is `any object collection <subcommand>` — the
// collections an object is filed under. Both verbs are idempotent;
// detaching leaves that namespace's values as orphan data.
func newObjectCollectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "collection",
		Short: "file the object under a collection / remove it from one",
	}
	cmd.AddCommand(newObjectCollectionAttachCmd(), newObjectCollectionDetachCmd())
	return cmd
}

func newObjectCollectionAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <spaceId> <objectId> <collectionId>",
		Short: "file the object under a collection (the bin collection moves it to the bin)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.ObjectAttachCollection(cmd.Context(), args[0], args[1], args[2])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newObjectCollectionDetachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detach <spaceId> <objectId> <collectionId>",
		Short: "remove the object from a collection (the bin collection restores it)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.ObjectDetachCollection(cmd.Context(), args[0], args[1], args[2])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}
