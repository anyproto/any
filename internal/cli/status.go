package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "GET /v1/health on the running server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl := newClient(flags.Timeout)
			h, err := cl.Health(cmd.Context())
			if err != nil {
				return err
			}
			out, err := json.MarshalIndent(h, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(out))
			return nil
		},
	}
}
