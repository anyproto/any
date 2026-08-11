package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// newEmailCmd is the root for `any email <subcommand>`. The four
// subcommands (mailbox / ingest / patch / delete) line up 1:1 with the
// HTTP endpoints; reads and live updates go through `any
// query-subscribe SPACE OBJ --dataset email_messages --sort
// -internalDate`.
func newEmailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "email",
		Short: "ingest / patch / delete synced mail on a mailbox object (reads via `any query-subscribe`)",
	}
	cmd.AddCommand(
		newEmailMailboxCmd(),
		newEmailIngestCmd(),
		newEmailPatchCmd(),
		newEmailDeleteCmd(),
	)
	return cmd
}

func newEmailMailboxCmd() *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:   "mailbox <spaceId>",
		Short: "resolve (creating on first use) the deterministic mailbox object for an address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if address == "" {
				return fmt.Errorf("--address required")
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.EmailMailbox(cmd.Context(), args[0], address)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "mailbox address (normalized: trimmed, lowercased)")
	return cmd
}

func newEmailIngestCmd() *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "ingest <spaceId> <objectId>",
		Short: "POST one batch of messages (upsert by provider id, one DAG change)",
		Long: `Ingest a sync page (api.EmailIngestRequest shape, ≤ 256 messages):
  {"messages": [{"id": "19fed5f923c0f962", "threadId": "...",
    "internalDate": 1786393890000, "from": {"name": "...", "address": "a@b"},
    "to": [{"address": "c@d"}], "subject": "...", "snippet": "...",
    "bodyText": "...", "labelIds": ["INBOX", "UNREAD"], "historyId": "..."}]}
New ids are created; existing ids get labelIds/historyId patched when
changed; identical ids are skipped. The reply lists created / updated /
unchanged ids.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.EmailIngestRequest
			if err := readJSONBody(body, &req); err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.EmailIngest(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&body, "json", "", "ingest batch JSON (inline, @FILE, or - for stdin)")
	return cmd
}

func newEmailPatchCmd() *cobra.Command {
	var (
		labels    []string
		clear     bool
		historyId string
	)
	cmd := &cobra.Command{
		Use:   "patch <spaceId> <objectId> <msgId>",
		Short: "PATCH a message's mutable fields (labelIds, historyId)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.EmailPatchRequest
			switch {
			case clear && len(labels) > 0:
				return fmt.Errorf("--label and --clear-labels are mutually exclusive")
			case clear:
				empty := []string{}
				req.LabelIds = &empty
			case len(labels) > 0:
				req.LabelIds = &labels
			}
			req.HistoryId = historyId
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.EmailPatch(cmd.Context(), args[0], args[1], args[2], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringArrayVar(&labels, "label", nil, "replacement label set (repeatable; replaces the whole array)")
	cmd.Flags().BoolVar(&clear, "clear-labels", false, "clear every label")
	cmd.Flags().StringVar(&historyId, "history-id", "", "provider history frontier for this message")
	return cmd
}

func newEmailDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <spaceId> <objectId> <msgId>",
		Short: "DELETE a message (provider-side delete/expunge)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.EmailDelete(cmd.Context(), args[0], args[1], args[2])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	return cmd
}
