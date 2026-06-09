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

// newAgentCmd is the root for `any agent <subcommand>` — the agent
// data layer's write endpoints (turns / chunks / memory) plus the
// brain-id resolver. Reads go through `any query` with dataset ∈
// {agent_turns, agent_chunks, agent_memory_items}; see
// docs/11-agent-memory.md.
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "agent data layer: append turns / chunks, manage memory items (reads via `any query`)",
	}
	cmd.AddCommand(
		newAgentTurnAppendCmd(),
		newAgentChunkCreateCmd(),
		newAgentBrainCmd(),
		newAgentMemoryCmd(),
	)
	return cmd
}

// readJSONBody decodes a request body from --json (inline string or
// @file / - for stdin). Complex nested bodies (turn records, memory
// items) are JSON-first in the CLI; common scalar flags would explode
// into dozens of options without adding clarity.
func readJSONBody(raw string, out any) error {
	var buf []byte
	switch {
	case raw == "":
		return fmt.Errorf("supply --json BODY (inline JSON, @FILE, or - for stdin)")
	case raw == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		buf = b
	case raw[0] == '@':
		b, err := os.ReadFile(raw[1:])
		if err != nil {
			return fmt.Errorf("read %s: %w", raw[1:], err)
		}
		buf = b
	default:
		buf = []byte(raw)
	}
	if err := json.Unmarshal(buf, out); err != nil {
		return fmt.Errorf("parse body: %w", err)
	}
	return nil
}

func newAgentTurnAppendCmd() *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "turn-append <spaceId> <objectId>",
		Short: "POST one immutable turn record to the chat object's agent_turns dataset",
		Long: `Append one agent turn record (api.AgentTurnAppendRequest shape):
  {"seq": 0, "userName": "alice", "userText": "...", "think": "...",
   "replies": ["..."], "effects": ["..."], "messageIds": ["..."],
   "debugRef": "...", "fromAgent": "bobrik",
   "llm": {"stopReason": "end_turn", "inTokens": 0, "outTokens": 0}}
seq is the per-chat monotonic ordering key (last seen + 1).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.AgentTurnAppendRequest
			if err := readJSONBody(body, &req); err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.AgentTurnAppend(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&body, "json", "", "turn record JSON (inline, @FILE, or - for stdin)")
	return cmd
}

func newAgentChunkCreateCmd() *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "chunk-create <spaceId> <objectId>",
		Short: "POST one immutable compressed-history chunk to the chat object's agent_chunks dataset",
		Long: `Create one chunk record (api.AgentChunkCreateRequest shape):
  {"seq": 0, "summary": "...", "periodStart": 1700000000, "periodEnd": 1700003600,
   "fromSeq": 0, "toSeq": 9, "turnsCovered": 10}
fromSeq/toSeq are the inclusive agent_turns range this chunk summarizes.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.AgentChunkCreateRequest
			if err := readJSONBody(body, &req); err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.AgentChunkCreate(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&body, "json", "", "chunk record JSON (inline, @FILE, or - for stdin)")
	return cmd
}

func newAgentBrainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "brain <spaceId>",
		Short: "GET the deterministic per-space brain object id (hosts agent_memory_items)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.AgentBrain(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newAgentMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "create / evolve / delete memory items on the space brain object",
	}
	cmd.AddCommand(
		newAgentMemoryAddCmd(),
		newAgentMemoryEvolveCmd(),
		newAgentMemoryDeleteCmd(),
	)
	return cmd
}

func newAgentMemoryAddCmd() *cobra.Command {
	var (
		body     string
		category string
		context  string
		text     string
	)
	cmd := &cobra.Command{
		Use:   "add <spaceId>",
		Short: "POST a new memory item",
		Long: `Create a memory item. Quick form:
  any agent memory add SPACE --category preference --context "user prefers dark mode" [--body "..."]
Full form (api.AgentMemoryCreateRequest shape — tags/entities/keywords/edges/...):
  any agent memory add SPACE --json '{"category":"lesson","context":"...","tags":["..."]}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.AgentMemoryCreateRequest
			if body != "" {
				if err := readJSONBody(body, &req); err != nil {
					return err
				}
			}
			if category != "" {
				req.Category = category
			}
			if context != "" {
				req.Context = context
			}
			if text != "" {
				req.Body = text
			}
			if req.Category == "" || req.Context == "" {
				return fmt.Errorf("category and context are required (--category/--context or --json)")
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.AgentMemoryCreate(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&body, "json", "", "full item JSON (inline, @FILE, or - for stdin)")
	cmd.Flags().StringVar(&category, "category", "", "category slug (claim, preference, decision, lesson, episode, taskstate, insight, or custom)")
	cmd.Flags().StringVar(&context, "context", "", "one-line summary")
	cmd.Flags().StringVar(&text, "body", "", "memory text (markdown)")
	return cmd
}

func newAgentMemoryEvolveCmd() *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "evolve <spaceId> <itemId>",
		Short: "PATCH a memory item's mutable fields (author only)",
		Long: `Set allow-listed mutable fields (api.AgentMemoryEvolveRequest shape):
  any agent memory evolve SPACE ITEM --json '{"salience": 3, "tags": ["..."]}'
Mutable: salience, accessCount, confidence, importance, context, body, tags, edges.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req api.AgentMemoryEvolveRequest
			if err := readJSONBody(body, &req); err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.AgentMemoryEvolve(cmd.Context(), args[0], args[1], req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&body, "json", "", "fields JSON (inline, @FILE, or - for stdin)")
	return cmd
}

func newAgentMemoryDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <spaceId> <itemId>",
		Short: "DELETE a memory item (author only)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.AgentMemoryDelete(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}
