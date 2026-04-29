package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "POST /v1/shutdown on the running server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.Shutdown(cmd.Context())
		},
	}
}
