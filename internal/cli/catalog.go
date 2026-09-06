package cli

import (
	"github.com/spf13/cobra"
)

// `any catalog …` — the server's usecase catalog: the well-known
// bundles it can set up in a space. Mirrors /v1/catalog.
func newCatalogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "list the usecase catalog and set a usecase up in a space",
	}
	cmd.AddCommand(newCatalogListCmd(), newCatalogGetCmd(), newCatalogSetupCmd())
	return cmd
}

func newCatalogListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "every usecase the server can set up",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.CatalogList(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newCatalogGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <usecaseId>",
		Short: "one usecase as the catalog declares it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.CatalogGet(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newCatalogSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup <usecaseId> <spaceId>",
		Short: "set a usecase up in a space, its dependencies first",
		Long: `Adopt-or-install, idempotent: every bundle of the usecase and of the
usecases it requires, dependencies first. The reply lists each bundle
with the converged registry row, whether THIS call installed it, the
root as the type id and the property handles resolved to ids. Run it
again after a failure — every step resumes.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.CatalogSetup(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}
