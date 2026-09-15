package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// `any bundle …` — the per-space bundles registry: what a client has
// installed into a space. Mirrors /v1/spaces/:id/bundles.
func newBundleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "install / list / read / resolve bundles in a space",
	}
	cmd.AddCommand(
		newBundleEnsureCmd(),
		newBundleListCmd(),
		newBundleGetCmd(),
		newBundleResolveCmd(),
		newBundleChildCmd(),
	)
	return cmd
}

func newBundleChildCmd() *cobra.Command {
	var (
		seed        string
		typeId      string
		collections []string
	)
	cmd := &cobra.Command{
		Use:   "child <spaceId> <bundleId> --seed SEED [--type T] [--collection C ...]",
		Short: "derive a setup object under the bundle's winner (deterministic per seed)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.BundleChild(cmd.Context(), args[0], args[1], api.BundleChildRequest{Seed: seed, Type: typeId, Collections: collections})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&seed, "seed", "", "child seed (permanent; a successor object takes a new one)")
	cmd.Flags().StringVar(&typeId, "type", "", "type id set on first materialization")
	cmd.Flags().StringArrayVar(&collections, "collection", nil, "collection id the child is filed under (repeatable)")
	_ = cmd.MarkFlagRequired("seed")
	return cmd
}

func newBundleEnsureCmd() *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "ensure <spaceId> --body '<json>|@FILE|-'",
		Short: "install a bundle or adopt the existing install",
		Long: `Adopt-or-install (api.BundleEnsureRequest shape). The body declares
what the root is: parts (datasets a module serves), properties (the
columns objects carry — every property needs an xKey, its id derives
from it), xKey (the handle; alone it declares a marker), layout and
hidden. collection: true declares a collection instead of a type
(parts and layout are refused with it). rootType / rootCollections /
rootProperties ride a derived root or a created root — rootType is
refused next to a declaration, the declaring root's any.type holding
the marker.
  {"id": "notes/v1", "name": "Notes", "hidden": true,
   "parts": [{"key": "body", "datasets": [{"module": "editor", "shared": true}]}]}
  {"id": "wiki/v1", "name": "Wiki", "derived": true, "collection": true,
   "properties": [{"xKey": "parentId", "name": "Parent", "kind": "string"},
                  {"xKey": "pos", "name": "Position", "kind": "string"}]}
Ids under "system:" are the server's and are refused. The reply carries
the converged row and whether THIS call installed it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.BundleEnsureRequest
			if err := readJSONBody(body, &req); err != nil {
				return err
			}
			cl := newClient(flags.Timeout)
			out, err := cl.BundleEnsure(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "ensure request (inline JSON, @FILE, or - for stdin)")
	_ = cmd.MarkFlagRequired("body")
	return cmd
}

func newBundleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <spaceId>",
		Short: "list the space's bundles (locked on registry convergence; the reply says whether it converged)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.BundleList(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newBundleGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <spaceId> <bundleId>",
		Short: "read one bundle row (the id verbatim, e.g. favorites/v1)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.BundleGet(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newBundleResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve <spaceId> <bundleId> <loserRootId>",
		Short: "delete a losing root after merging its content (cascades to its children)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.BundleResolve(cmd.Context(), args[0], args[1], args[2])
		},
	}
}
