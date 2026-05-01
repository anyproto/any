package cli

import (
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/server"
)

func newRunCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "run",
		Short: "start the HTTP server (foreground)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configFlags())
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return server.Run(ctx, cfg)
		},
	}
	addServerFlags(c)
	return c
}
