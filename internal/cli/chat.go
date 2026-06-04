package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// newChatCmd is the root for `any chat <subcommand>`. The five
// subcommands (send / list / edit / delete / react) line up 1:1 with
// the HTTP endpoints under /v1/spaces/:id/objects/:objectId/messages.
//
// For tailing live messages use the existing `any subscribe SPACE OBJ
// --dataset chat_messages` — the chat endpoints intentionally don't
// expose a chat-specific subscribe.
func newChatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "send / edit / delete / react to messages on a chat object (reads via `any query`)",
	}
	cmd.AddCommand(
		newChatSendCmd(),
		newChatEditCmd(),
		newChatDeleteCmd(),
		newChatReactCmd(),
	)
	return cmd
}

func newChatSendCmd() *cobra.Command {
	var (
		text      string
		file      string
		replyTo   string
		fromAgent string
	)
	cmd := &cobra.Command{
		Use:   "send <spaceId> <objectId>",
		Short: "POST a new message",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readTextInput(text, file)
			if err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.ChatSend(cmd.Context(), args[0], args[1], api.ChatSendRequest{
				Text:             body,
				ReplyToMessageId: replyTo,
				FromAgent:        fromAgent,
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "message text (markdown). Mutually exclusive with --file")
	cmd.Flags().StringVar(&file, "file", "", "read message text from FILE (use - for stdin)")
	cmd.Flags().StringVar(&replyTo, "reply-to", "", "id of a message this reply targets")
	cmd.Flags().StringVar(&fromAgent, "from-agent", "", "opaque tag marking this message as written by an agent")
	return cmd
}

func newChatEditCmd() *cobra.Command {
	var (
		text string
		file string
	)
	cmd := &cobra.Command{
		Use:   "edit <spaceId> <objectId> <msgId>",
		Short: "PATCH the text of one of your own messages",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readTextInput(text, file)
			if err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.ChatEdit(cmd.Context(), args[0], args[1], args[2], api.ChatEditRequest{Text: body})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "new message text (markdown). Mutually exclusive with --file")
	cmd.Flags().StringVar(&file, "file", "", "read new text from FILE (use - for stdin)")
	return cmd
}

func newChatDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <spaceId> <objectId> <msgId>",
		Short: "DELETE one of your own messages",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.ChatDelete(cmd.Context(), args[0], args[1], args[2])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newChatReactCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "react <spaceId> <objectId> <msgId> <emoji>",
		Short: "toggle your reaction (add if absent, remove if present)",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.ChatReact(cmd.Context(), args[0], args[1], args[2], args[3])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

// readTextInput resolves the (--text, --file) pair into the message
// body. Exactly one must be supplied. --file - reads stdin so callers
// can pipe markdown in from $(cat file.md) or a heredoc.
func readTextInput(text, file string) (string, error) {
	switch {
	case text != "" && file != "":
		return "", fmt.Errorf("--text and --file are mutually exclusive")
	case text != "":
		return text, nil
	case file == "-":
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(buf), nil
	case file != "":
		buf, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", file, err)
		}
		return string(buf), nil
	default:
		return "", fmt.Errorf("supply --text TEXT or --file PATH (use - for stdin)")
	}
}

// printJSON pretty-prints v to stdout. House style matches `any
// status`, `any chat list`, etc. — one big indented JSON block.
func printJSON(v any) error {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(buf))
	return nil
}
