package cli

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// newEventsCmd: `any events ...` — the account-wide ephemeral event
// bus (in-memory, at-most-once, fire-and-forget). See
// docs/21-events.md.
func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "publish and subscribe on the event bus",
	}
	cmd.AddCommand(newEventsPublishCmd(), newEventsSubscribeCmd())
	return cmd
}

func newEventsPublishCmd() *cobra.Command {
	var (
		typ     string
		scope   string
		spaceId string
		target  string
		data    string
	)
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "publish one event",
		Long: `Publish one event on the bus (POST /v1/events). The data payload is
JSON — inline, @FILE, or - for stdin.

  any events publish --type ui.open_space --data '{"spaceId":"SPACE"}'
  any events publish --type process.progress --target run1 --data @progress.json
`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if typ == "" {
				return fmt.Errorf("--type is required")
			}
			req := api.EventPublishRequest{
				Type:    typ,
				Scope:   scope,
				SpaceId: spaceId,
				Target:  target,
			}
			if data != "" {
				var raw json.RawMessage
				if err := readJSONBody(data, &raw); err != nil {
					return fmt.Errorf("--data: %w", err)
				}
				req.Data = raw
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.EventsPublish(cmd.Context(), req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "event type — dotted slug (e.g. process.progress)")
	cmd.Flags().StringVar(&scope, "scope", api.EventScopeDevice, "delivery scope: device, account or space")
	cmd.Flags().StringVar(&spaceId, "space", "", "target space (required for --scope space)")
	cmd.Flags().StringVar(&target, "target", "", "subject id (objectId, runId, ...)")
	cmd.Flags().StringVar(&data, "data", "", "JSON payload (inline, @FILE, or - for stdin)")
	return cmd
}

func newEventsSubscribeCmd() *cobra.Command {
	var (
		scopes   []string
		spaceIds []string
		types    []string
		targets  []string
	)
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "stream events (one JSON line per frame)",
		Long: `Stream matching events over SSE (GET /v1/events/subscribe). All
filter flags are repeatable and AND across dimensions, OR within one;
--type accepts an exact type or a prefix ending in .* .

  any events subscribe --type ui.*
  any events subscribe --scope space --space SPACE --type process.progress
`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			filter := url.Values{}
			for _, v := range scopes {
				filter.Add("scope", v)
			}
			for _, v := range spaceIds {
				filter.Add("spaceId", v)
			}
			for _, v := range types {
				filter.Add("type", v)
			}
			for _, v := range targets {
				filter.Add("target", v)
			}
			cl := client.New(flags.Addr, 0) // timeout doesn't apply to streams
			return cl.StreamEvents(cmd.Context(), filter, jsonFrameHandler())
		},
	}
	cmd.Flags().StringArrayVar(&scopes, "scope", nil, "scope filter (repeatable)")
	cmd.Flags().StringArrayVar(&spaceIds, "space", nil, "spaceId filter (repeatable)")
	cmd.Flags().StringArrayVar(&types, "type", nil, "type filter, exact or prefix x.* (repeatable)")
	cmd.Flags().StringArrayVar(&targets, "target", nil, "target filter (repeatable)")
	return cmd
}
