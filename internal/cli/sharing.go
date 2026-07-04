package cli

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// `any members ...` — read-side surface over the members collection.
func newMembersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "members",
		Short: "list / get / inspect space members and pending join requests",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "subscribe <spaceId>",
			Short: "SSE stream of membership changes (added/changed/removed)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, 0)
				enc := json.NewEncoder(os.Stdout)
				return cl.StreamSubscribeMembers(cmd.Context(), args[0], func(f client.SSEFrame) error {
					if f.Event == "" {
						return nil
					}
					out := struct {
						Event string          `json:"event"`
						Data  json.RawMessage `json:"data,omitempty"`
					}{Event: f.Event, Data: json.RawMessage(f.Data)}
					return enc.Encode(out)
				})
			},
		},
		&cobra.Command{
			Use:   "list <spaceId>",
			Short: "list every member visible in the ACL (includes tombstones and pending requests)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				out, err := cl.MembersList(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return printJSON(out)
			},
		},
		&cobra.Command{
			Use:   "me <spaceId>",
			Short: "the caller's own member entry",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				out, err := cl.MembersMe(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return printJSON(out)
			},
		},
		&cobra.Command{
			Use:   "get <spaceId> <identity>",
			Short: "one member by identity",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				out, err := cl.MembersGet(cmd.Context(), args[0], args[1])
				if err != nil {
					return err
				}
				return printJSON(out)
			},
		},
		&cobra.Command{
			Use:   "requests <spaceId>",
			Short: "pending join requests awaiting accept/decline",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				out, err := cl.JoinRequests(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return printJSON(out)
			},
		},
	)
	return cmd
}

// `any invite ...` — owner-side mint / list / revoke flow for token
// invites (joiners use `any join`, not `invite accept`), plus the
// receiver side of DIRECT-ADD invites: spaces this account was added to
// by identity (`any acl add` on the other side) surface as
// status=invite_pending rows — `invite pending` lists them, `invite
// accept` / `invite decline` resolve them.
func newInviteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "mint/list/revoke share invites; accept or decline direct-add invites",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "create <spaceId>",
			Short: "mint a fresh invite (replaces any prior one)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				out, err := cl.InviteCreate(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return printJSON(out)
			},
		},
		&cobra.Command{
			Use:   "list <spaceId>",
			Short: "every active invite record on the ACL",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				out, err := cl.InvitesList(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return printJSON(out)
			},
		},
		&cobra.Command{
			Use:   "revoke <spaceId> <recordId>",
			Short: "revoke one invite by record id",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				return cl.InviteRevoke(cmd.Context(), args[0], args[1])
			},
		},
		&cobra.Command{
			Use:   "revoke-all <spaceId>",
			Short: "revoke every active invite in one batch",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				return cl.InvitesRevokeAll(cmd.Context(), args[0])
			},
		},
		&cobra.Command{
			Use:   "pending",
			Short: "list direct-add invites awaiting approval",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				out, err := cl.SpaceList(cmd.Context(), api.SpaceStatusInvitePending)
				if err != nil {
					return err
				}
				return printJSON(out)
			},
		},
		&cobra.Command{
			Use:   "accept <spaceId>",
			Short: "accept a direct-add invite (loads the space)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				out, err := cl.SpaceInviteAccept(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return printJSON(out)
			},
		},
		&cobra.Command{
			Use:   "decline <spaceId>",
			Short: "decline a direct-add invite (sticky; accept later overrides)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				return cl.SpaceInviteDecline(cmd.Context(), args[0])
			},
		},
	)
	return cmd
}

// `any join` — joiner side. Takes the share-friendly invite token.
func newJoinCmd() *cobra.Command {
	var (
		token   string
		name    string
		desc    string
		iconCID string
	)
	cmd := &cobra.Command{
		Use:   "join",
		Short: "join a space via an invite token (--token)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.SpaceJoin(cmd.Context(), api.SpaceJoinRequest{
				InviteToken: token,
				Metadata:    api.AccountMetadata{Name: name, Description: desc, IconCID: iconCID},
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "invite token from `any invite create` (required)")
	cmd.Flags().StringVar(&name, "name", "", "joiner display name attached to the join record")
	cmd.Flags().StringVar(&desc, "description", "", "joiner description")
	cmd.Flags().StringVar(&iconCID, "icon-cid", "", "joiner icon CID")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}

// `any acl ...` — owner / admin operations on existing memberships.
func newACLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acl",
		Short: "owner/admin operations on space membership (accept, decline, permissions, remove, ownership, …)",
	}

	// accept --record <id> --permission <perm>
	{
		var record, perm string
		acc := &cobra.Command{
			Use:   "accept <spaceId>",
			Short: "approve a pending join request and grant a permission",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				return cl.ACLAccept(cmd.Context(), args[0], api.ACLAcceptRequest{
					RequestRecordId: record,
					Permission:      perm,
				})
			},
		}
		acc.Flags().StringVar(&record, "record", "", "request record id from `any members requests`")
		acc.Flags().StringVar(&perm, "permission", "writer", "granted permission (none|reader|guest|writer|admin)")
		_ = acc.MarkFlagRequired("record")
		cmd.AddCommand(acc)
	}

	// decline --identity <id>
	{
		var ident string
		dec := &cobra.Command{
			Use:   "decline <spaceId>",
			Short: "decline a pending join request",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				return cl.ACLDecline(cmd.Context(), args[0], api.ACLDeclineRequest{Identity: ident})
			},
		}
		dec.Flags().StringVar(&ident, "identity", "", "joiner account id (required)")
		_ = dec.MarkFlagRequired("identity")
		cmd.AddCommand(dec)
	}

	// grant <spaceId> <identity> <permission> — convenience for a 1-entry ChangePermissions batch.
	cmd.AddCommand(&cobra.Command{
		Use:   "grant <spaceId> <identity> <permission>",
		Short: "set a single member's permission",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.ACLChangePermissions(cmd.Context(), args[0], api.ACLChangePermissionsRequest{
				Changes: []api.ACLPermissionChange{{Identity: args[1], Permission: args[2]}},
			})
		},
	})

	// remove <spaceId> <identity>... — multi-account.
	cmd.AddCommand(&cobra.Command{
		Use:   "remove <spaceId> <identity>...",
		Short: "remove members and rotate the read key",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.ACLRemove(cmd.Context(), args[0], api.ACLRemoveRequest{Identities: args[1:]})
		},
	})

	// add <spaceId> <identity>[,<identity>...] <permission> — the whole
	// batch lands in ONE ACL record; each added account gets a durable
	// inbox notification and surfaces the space as invite_pending.
	{
		var name, desc string
		add := &cobra.Command{
			Use:   "add <spaceId> <identity>[,<identity>...] <permission>",
			Short: "add accounts directly without an invite/request round-trip (one ACL record per call)",
			Args:  cobra.ExactArgs(3),
			RunE: func(cmd *cobra.Command, args []string) error {
				var accounts []api.ACLAddAccount
				for _, id := range strings.Split(args[1], ",") {
					id = strings.TrimSpace(id)
					if id == "" {
						continue
					}
					accounts = append(accounts, api.ACLAddAccount{
						Identity:   id,
						Permission: args[2],
						Metadata:   api.AccountMetadata{Name: name, Description: desc},
					})
				}
				cl := client.New(flags.Addr, flags.Timeout)
				return cl.ACLAdd(cmd.Context(), args[0], api.ACLAddRequest{Accounts: accounts})
			},
		}
		add.Flags().StringVar(&name, "name", "", "metadata name (single-identity adds)")
		add.Flags().StringVar(&desc, "description", "", "metadata description (single-identity adds)")
		cmd.AddCommand(add)
	}

	// ownership --new-owner <id> --old-owner-perm <perm>
	{
		var newOwner, oldPerm string
		own := &cobra.Command{
			Use:   "ownership <spaceId>",
			Short: "transfer ownership; old owner becomes --old-owner-perm",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cl := client.New(flags.Addr, flags.Timeout)
				return cl.ACLOwnership(cmd.Context(), args[0], api.ACLOwnershipRequest{
					NewOwner:     newOwner,
					OldOwnerPerm: oldPerm,
				})
			},
		}
		own.Flags().StringVar(&newOwner, "new-owner", "", "new owner identity (required)")
		own.Flags().StringVar(&oldPerm, "old-owner-perm", "admin", "permission for the old owner after transfer")
		_ = own.MarkFlagRequired("new-owner")
		cmd.AddCommand(own)
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "self-remove <spaceId>",
		Short: "ask to be removed from a space (non-owner only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.ACLSelfRemove(cmd.Context(), args[0])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "cancel-join <spaceId>",
		Short: "withdraw your pending join request on this space",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.ACLCancelJoin(cmd.Context(), args[0])
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "stop-sharing <spaceId>",
		Short: "drop every non-owner member, revoke every invite, rotate the read key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.ACLStopSharing(cmd.Context(), args[0])
		},
	})

	return cmd
}
