package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
)

// newQuerySubscribeCmd surfaces both windowed query/subscribe endpoints
// behind one command. With `--properties` (no objectId/dataset), it
// opens the per-space `objects` firehose; otherwise (objectId,
// --dataset) is required and it opens the per-object dataset stream.
//
// Output is one JSON object per SSE frame on stdout. Each line is
// `{"event": "<name>", "data": <payload>}`. Pipe through jq:
//
//	any query-subscribe SPACE --properties | jq 'select(.event=="changes")'
func newQuerySubscribeCmd() *cobra.Command {
	var (
		dataset    string
		properties bool
		filter     string
		sort       string
		projection string
		limit      int
		offset     int
		includeTot bool
	)
	cmd := &cobra.Command{
		Use:   "query-subscribe <spaceId> [<objectId>]",
		Short: "open a windowed query/subscribe SSE stream",
		Long: `Stream the windowed view of a query from the running server. With
--properties, opens POST /v1/spaces/:id/objects/query/subscribe over the per-
space objects collection; otherwise takes <objectId> + --dataset and opens
POST /v1/spaces/:id/query/subscribe over a per-object dataset.

Frames (one JSON object per line on stdout):

  {"event": "ready",    "data": {}}
  {"event": "snapshot", "data": {"records":[...], "total": 17, "hasNext": true}}
  {"event": "changes",  "data": [{"versionId":"...","added":[...],"updated":[...],"removed":[{"id":"...","reason":"deleted"}]}]}
  {"event": "closed",   "data": {"reason": "server_shutdown" | "sdk_closed" | "overflow" | "drifted"}}
`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			spaceId := args[0]

			body, err := buildQueryBody(properties, args, dataset, filter, sort, projection, limit, offset, includeTot)
			if err != nil {
				return err
			}

			cl := client.New(flags.Addr, 0) // timeout doesn't apply to streams
			handle := jsonFrameHandler()

			ctx := cmd.Context()
			if properties {
				return cl.StreamObjectsQuerySubscribe(ctx, spaceId, body, handle)
			}
			return cl.StreamQuerySubscribe(ctx, spaceId, body, handle)
		},
	}
	cmd.Flags().StringVar(&dataset, "dataset", "", "dataset name (required without --properties)")
	cmd.Flags().BoolVar(&properties, "properties", false, "subscribe to the per-space objects collection instead of a per-object dataset")
	addWindowQueryFlags(cmd, &filter, &sort, &projection, &limit, &offset, &includeTot)
	return cmd
}

// addWindowQueryFlags declares the shared windowed-query flags every
// query/query-subscribe command takes (the wire QueryBodyParams minus
// the addressing fields). One definition so the flag surface and help
// text can't drift per command; commands with extra addressing flags
// (--dataset, --properties) declare those on top.
func addWindowQueryFlags(cmd *cobra.Command, filter, sort, projection *string, limit, offset *int, includeTot *bool) {
	cmd.Flags().StringVar(filter, "filter", "", "JSON filter object")
	cmd.Flags().StringVar(projection, "projection", "",
		"comma-separated field paths to return, '-' prefix to exclude (e.g. 'any,nav' or '-_ver'); id always rides along and _ver follows the projection")
	cmd.Flags().StringVar(sort, "sort", "", "comma-separated sort keys (prefix '-' for descending)")
	cmd.Flags().IntVar(limit, "limit", 0, "window size; required when --sort is set")
	cmd.Flags().IntVar(offset, "offset", 0, "skip the first N records of the snapshot")
	cmd.Flags().BoolVar(includeTot, "total", false, "include the unbounded match count and hasNext flag")
}

// jsonFrameHandler returns the CLI's SSE→stdout bridge — the one
// implementation of the "one JSON frame per line" output contract
// every subscribe command shares: {"event": name, "data": payload},
// keepalive/comment frames (empty event) skipped.
func jsonFrameHandler() func(client.SSEFrame) error {
	enc := json.NewEncoder(os.Stdout)
	return func(f client.SSEFrame) error {
		if f.Event == "" {
			return nil
		}
		return enc.Encode(struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data,omitempty"`
		}{Event: f.Event, Data: json.RawMessage(f.Data)})
	}
}

// buildQueryBody assembles the JSON request body for both query
// endpoints. objectId / dataset are required for per-object queries;
// --properties skips both.
func buildQueryBody(properties bool, args []string, dataset, filter, sort, projection string, limit, offset int, includeTotal bool) ([]byte, error) {
	body := map[string]any{}
	if !properties {
		if len(args) != 2 {
			return nil, fmt.Errorf("objectId is required (or pass --properties for the firehose)")
		}
		if dataset == "" {
			return nil, fmt.Errorf("--dataset is required")
		}
		body["objectId"] = args[1]
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
	if err := applyProjection(body, projection); err != nil {
		return nil, err
	}
	return json.Marshal(body)
}

// applyProjection folds the --projection CSV shorthand into a request
// body. `any,nav,-_ver` becomes {"any":1,"nav":1,"_ver":-1} — the
// '-' prefix is the same exclude marker --sort uses for descending, so
// the two flags read alike.
func applyProjection(body map[string]any, spec string) error {
	if spec == "" {
		return nil
	}
	proj := map[string]int{}
	for _, field := range splitCSV(spec) {
		// Trim so `--projection 'any, nav'` reads the way it looks; an
		// untrimmed " nav" is a legal path server-side that matches
		// nothing, which would silently drop the field.
		field = strings.TrimSpace(field)
		mark := 1
		if strings.HasPrefix(field, "-") {
			field, mark = strings.TrimSpace(field[1:]), -1
		}
		if field == "" {
			return fmt.Errorf("--projection: empty field path")
		}
		proj[field] = mark
	}
	body["projection"] = proj
	return nil
}

func splitCSV(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
