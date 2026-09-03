package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
	"github.com/anyproto/any/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print binary version and, if running, the server version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Println(version.String())

			// Best-effort probe of the server; don't fail the command if
			// the server isn't up.
			cl := newClient(flags.Timeout)
			h, err := cl.Health(cmd.Context())
			if err != nil {
				var te *client.TransportError
				if errors.As(err, &te) {
					return nil
				}
				return err
			}
			fmt.Println("server:", h.Version)
			return nil
		},
	}
}
