package cli

import (
	"github.com/spf13/cobra"
)

// newReadStateCmd: `any read-state …` — the space's read/unread state
// (SDK Space.ReadState()). Marks are module writes: `any chat read…`.
func newReadStateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "read-state",
		Short: "what became unread in a space on this device",
	}
	cmd.AddCommand(newReadStateUnreadCmd())
	return cmd
}

func newReadStateUnreadCmd() *cobra.Command {
	var (
		cursor string
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "unread <spaceId> [--cursor C] [--limit N]",
		Short: "list the changes that became unread after a cursor (none: start at the present)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := newClient(flags.Timeout).ReadStateUnread(cmd.Context(), args[0], cursor, limit)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&cursor, "cursor", "", "cursor from the previous call")
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when 0)")
	return cmd
}
