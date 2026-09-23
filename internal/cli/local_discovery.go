package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// `any local-discovery [on|off]` — GET / PUT /v1/local-discovery: the
// mDNS announce+browse switch. Works before `any auth login`.
func newLocalDiscoveryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "local-discovery [on|off]",
		Short: "read or switch local-network discovery (mDNS announce + browse); works before auth",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			if len(args) == 0 {
				out, err := cl.LocalDiscovery(cmd.Context())
				if err != nil {
					return err
				}
				return printJSON(out)
			}
			var enabled bool
			switch args[0] {
			case "on":
				enabled = true
			case "off":
			default:
				return fmt.Errorf("local-discovery: want on or off, got %q", args[0])
			}
			out, err := cl.SetLocalDiscovery(cmd.Context(), enabled)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}
