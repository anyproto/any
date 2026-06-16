package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// `any space ...` — space-level operations that don't fit chat /
// editor / members / acl. v1 surface covers metadata `update` (rename /
// description / icon), `delete`, lookup, sync, and the space-list
// query/subscribe primitives — `create` / `join` still go through
// direct HTTP or the web UI.
func newSpaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "space",
		Short: "space-level operations (metadata update, lookup)",
	}

	cmd.AddCommand(newSpaceGetCmd(), newSpaceUpdateCmd(), newSpaceSyncCmd(),
		newSpaceDeleteCmd(), newSpaceQueryCmd(), newSpaceSubscribeCmd())
	return cmd
}

// newSpaceDeleteCmd: `any space delete <spaceId> --yes` — DELETE
// /v1/spaces/:id (Service.Delete). Destructive and irreversible — the
// space is offloaded locally and the row becomes a sticky `deleted`
// tombstone — so it refuses to run without the explicit --yes flag.
func newSpaceDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <spaceId>",
		Short: "delete a space (irreversible; requires --yes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to delete %s without --yes (this is irreversible)", args[0])
			}
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.SpaceDelete(cmd.Context(), args[0])
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the irreversible delete")
	return cmd
}

// newSpaceQueryCmd: `any space query` — windowed snapshot over the
// account's space list via POST /v1/spaces/query (Service.Query on the
// tech-space `spaces` dataset). GET /v1/spaces stays the mapped
// convenience; this is the filterable/sortable raw-row primitive.
func newSpaceQueryCmd() *cobra.Command {
	var (
		dataset    string
		filter     string
		sort       string
		limit      int
		offset     int
		includeTot bool
	)
	cmd := &cobra.Command{
		Use:   "query",
		Short: "windowed snapshot over the account's space list (filter/sort/limit)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := buildSpaceListQueryBody(dataset, filter, sort, limit, offset, includeTot)
			if err != nil {
				return err
			}
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.SpaceListQuery(cmd.Context(), body)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addSpaceListQueryFlags(cmd, &dataset, &filter, &sort, &limit, &offset, &includeTot)
	return cmd
}

// newSpaceSubscribeCmd: `any space subscribe` — windowed live view over
// the account's space list via POST /v1/spaces/query/subscribe. One JSON
// frame per line on stdout (same shape as `any query-subscribe`).
func newSpaceSubscribeCmd() *cobra.Command {
	var (
		dataset    string
		filter     string
		sort       string
		limit      int
		offset     int
		includeTot bool
	)
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "open a windowed space-list SSE stream (spaces added/changed/removed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := buildSpaceListQueryBody(dataset, filter, sort, limit, offset, includeTot)
			if err != nil {
				return err
			}
			cl := client.New(flags.Addr, 0) // timeout doesn't apply to streams
			enc := json.NewEncoder(os.Stdout)
			handle := func(f client.SSEFrame) error {
				if f.Event == "" {
					return nil
				}
				out := struct {
					Event string          `json:"event"`
					Data  json.RawMessage `json:"data,omitempty"`
				}{Event: f.Event, Data: json.RawMessage(f.Data)}
				return enc.Encode(out)
			}
			return cl.StreamSpaceListQuerySubscribe(cmd.Context(), body, handle)
		},
	}
	addSpaceListQueryFlags(cmd, &dataset, &filter, &sort, &limit, &offset, &includeTot)
	return cmd
}

func addSpaceListQueryFlags(cmd *cobra.Command, dataset, filter, sort *string, limit, offset *int, includeTot *bool) {
	cmd.Flags().StringVar(dataset, "dataset", "", "system dataset to read (default spaces; profile also available)")
	cmd.Flags().StringVar(filter, "filter", "", "JSON filter object")
	cmd.Flags().StringVar(sort, "sort", "", "comma-separated sort keys (prefix '-' for descending)")
	cmd.Flags().IntVar(limit, "limit", 0, "window size; required when --sort is set")
	cmd.Flags().IntVar(offset, "offset", 0, "skip the first N records of the snapshot")
	cmd.Flags().BoolVar(includeTot, "total", false, "include the unbounded match count and hasNext flag")
}

// buildSpaceListQueryBody assembles the request body for the space-list
// query/subscribe endpoints. All fields optional — an empty body is a
// full snapshot. objectId is server-fixed to the tech-space index.
func buildSpaceListQueryBody(dataset, filter, sort string, limit, offset int, includeTotal bool) ([]byte, error) {
	body := map[string]any{}
	if dataset != "" {
		body["dataset"] = dataset
	}
	if filter != "" {
		var f any
		if err := json.Unmarshal([]byte(filter), &f); err != nil {
			return nil, fmt.Errorf("--filter: %w", err)
		}
		body["filter"] = f
	}
	if sort != "" {
		var sorts []string
		for _, s := range splitCSV(sort) {
			if s != "" {
				sorts = append(sorts, s)
			}
		}
		if len(sorts) > 0 {
			body["sort"] = sorts
		}
	}
	if limit > 0 {
		body["limit"] = limit
	}
	if offset > 0 {
		body["offset"] = offset
	}
	if includeTotal {
		body["includeTotal"] = true
	}
	return json.Marshal(body)
}

// newSpaceSyncCmd: `any space sync <spaceId>` — force an immediate
// head-sync round instead of waiting for the periodic timer. Blocks
// until the server-side round completes. Prints nothing on success.
func newSpaceSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync <spaceId>",
		Short: "force an immediate head-sync round (sync now)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.SpaceSync(cmd.Context(), args[0])
		},
	}
}

func newSpaceGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <spaceId>",
		Short: "fetch one space's metadata (includes spaceIndexObjectId)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := client.New(flags.Addr, flags.Timeout)
			out, err := cl.SpaceGet(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newSpaceUpdateCmd() *cobra.Command {
	var name, desc, iconCID string
	cmd := &cobra.Command{
		Use:   "update <spaceId>",
		Short: "patch space metadata; pass --name='' to clear a field, omit a flag to leave it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.SpaceUpdateRequest{}
			// Cobra's `Changed` distinguishes "flag absent" from
			// "flag set to empty" — that's exactly the pointer-to-
			// string semantics PATCH /v1/spaces/:id expects.
			if cmd.Flags().Changed("name") {
				req.Name = &name
			}
			if cmd.Flags().Changed("description") {
				req.Description = &desc
			}
			if cmd.Flags().Changed("icon-cid") {
				req.IconCID = &iconCID
			}
			cl := client.New(flags.Addr, flags.Timeout)
			return cl.SpaceUpdate(cmd.Context(), args[0], req)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new display name (set --name='' to clear)")
	cmd.Flags().StringVar(&desc, "description", "", "new description")
	cmd.Flags().StringVar(&iconCID, "icon-cid", "", "new icon CID")
	return cmd
}
