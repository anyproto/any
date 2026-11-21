package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// `any child ...` — a child space reached from outside it. The commands
// take the child's id alone; the account need not be a member of the
// child. The network accepts `remove` from an admin or the owner of the
// parent, `ownership` and `delete` from the owner of the parent and
// from the child's own owner. Each needs the network. The children of a
// parent are listed by `any space children`.
func newChildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "child",
		Short: "govern a child space from its parent's side (members, remove, ownership, delete)",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "members <childId>",
		Short: "the accounts of a child space with role and status (no names: the caller holds no key of the child)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.ChildMembers(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "remove <childId> <identity>...",
		Short: "remove members of a child space (parent's admin or owner); the read key changes later, by itself",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.ChildRemoveMembers(cmd.Context(), args[0], api.ACLRemoveRequest{Identities: args[1:]})
		},
	})

	// ownership --new-owner <id> --old-owner-perm <perm>
	{
		var newOwner, oldPerm string
		own := &cobra.Command{
			Use:   "ownership <childId>",
			Short: "hand a child space to one of its members (parent's owner, or the child's own); the previous owner keeps --old-owner-perm",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := newClient(flags.Timeout)
				return cl.ChildChangeOwner(cmd.Context(), args[0], api.ACLOwnershipRequest{
					NewOwner:     newOwner,
					OldOwnerPerm: oldPerm,
				})
			},
		}
		own.Flags().StringVar(&newOwner, "new-owner", "", "new owner identity, an active member of the child (required)")
		own.Flags().StringVar(&oldPerm, "old-owner-perm", "", "role the previous owner keeps: reader|writer|admin (required)")
		_ = own.MarkFlagRequired("new-owner")
		_ = own.MarkFlagRequired("old-owner-perm")
		cmd.AddCommand(own)
	}

	// delete <childId> --yes — irreversible, and for every member.
	{
		var yes bool
		del := &cobra.Command{
			Use:   "delete <childId>",
			Short: "delete a child space for everyone (parent's owner, or the child's own; irreversible; requires --yes)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if !yes {
					return fmt.Errorf("refusing to delete %s without --yes (this is irreversible, and deletes the space for all its members)", args[0])
				}
				cl := newClient(flags.Timeout)
				return cl.ChildDelete(cmd.Context(), args[0])
			},
		}
		del.Flags().BoolVar(&yes, "yes", false, "confirm the irreversible delete")
		cmd.AddCommand(del)
	}

	return cmd
}
