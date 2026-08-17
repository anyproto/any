package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// newProcessCmd: `any process ...` — the live process view over the
// event bus (GET /v1/processes) and owner-addressed cancel. Watching
// the raw frames is `any events subscribe --type 'process.*'`. See
// docs/22-processes.md.
func newProcessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "process",
		Short: "list and cancel live processes",
	}
	cmd.AddCommand(newProcessListCmd(), newProcessCancelCmd())
	return cmd
}

func newProcessListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list live processes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.ProcessesList(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newProcessCancelCmd() *cobra.Command {
	var identity string
	cmd := &cobra.Command{
		Use:   "cancel ID",
		Short: "ask a process's owner to cancel it",
		Long: `Emit process.cancel toward the owner of process ID
(POST /v1/processes/:id/cancel). The owner reacts and emits the
terminal event — cancel itself changes nothing. When several
publishers run the same id, pass --identity to pick the owner.
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.ProcessCancel(cmd.Context(), args[0], api.ProcessCancelRequest{Identity: identity})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&identity, "identity", "", "owner identity (disambiguates same-id processes)")
	return cmd
}
