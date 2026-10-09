package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
)

// newQuerySubscribeCmd surfaces the windowed query/subscribe endpoints
// behind one command. With `--properties` (no objectId/dataset), it
// opens the per-space `objects` firehose; with `--all-objects` and
// --dataset (no objectId), a shared dataset across its objects;
// otherwise (objectId, --dataset) is required and it opens the
// per-object dataset stream.
//
// Output is one JSON object per SSE frame on stdout. Each line is
// `{"event": "<name>", "data": <payload>}`. Pipe through jq:
//
//	any query-subscribe SPACE --properties | jq 'select(.event=="changes")'
func newQuerySubscribeCmd() *cobra.Command {
	var (
		dataset    string
		properties bool
		allObjects bool
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
space objects collection; with --all-objects + --dataset, opens
POST /v1/spaces/:id/datasets/query/subscribe over a shared dataset across
its objects; otherwise takes <objectId> + --dataset and opens
POST /v1/spaces/:id/query/subscribe over a per-object dataset.

Frames (one JSON object per line on stdout):

  {"event": "ready",    "data": {}}
  {"event": "snapshot", "data": {"records":[...], "total": 17, "hasNext": true}}
  {"event": "changes",  "data": [{"versionId":"...","added":[...],"updated":[...],"removed":[{"id":"...","reason":"deleted"}]}]}
  {"event": "closed",   "data": {"reason": "server_shutdown" | "deauthorized" | "sdk_closed" | "overflow" | "drifted" | "object_deleted"}}
`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			spaceId := args[0]

			body, err := buildQueryBody(properties, allObjects, args, dataset, filter, sort, projection, limit, offset, includeTot)
			if err != nil {
				return err
			}

			cl := newClient(0) // timeout doesn't apply to streams
			handle := jsonFrameHandler()

			ctx := cmd.Context()
			switch {
			case properties:
				return cl.StreamObjectsQuerySubscribe(ctx, spaceId, body, handle)
			case allObjects:
				return cl.StreamDatasetQuerySubscribe(ctx, spaceId, body, handle)
			}
			return cl.StreamQuerySubscribe(ctx, spaceId, body, handle)
		},
	}
	cmd.Flags().StringVar(&dataset, "dataset", "", "dataset name (required without --properties)")
	cmd.Flags().BoolVar(&properties, "properties", false, "subscribe to the per-space objects collection instead of a per-object dataset")
	cmd.Flags().BoolVar(&allObjects, "all-objects", false, "subscribe to a shared dataset across every object that holds it (with --dataset, no objectId)")
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
		"comma-separated field paths to return, '-' prefix to exclude (e.g. 'any,page' or '-_ver'); id always rides along and _ver follows the projection")
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

// buildQueryBody assembles the JSON request body for the query
// endpoints. objectId / dataset are required for per-object queries;
// --properties skips both, --all-objects takes the dataset alone.
func buildQueryBody(properties, allObjects bool, args []string, dataset, filter, sort, projection string, limit, offset int, includeTotal bool) ([]byte, error) {
	body := map[string]any{}
	if err := addressQueryBody(body, properties, allObjects, args, dataset); err != nil {
		return nil, err
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

// addressQueryBody writes the addressing fields of a query or
// aggregate body from the command's scope flags: nothing for
// --properties, the dataset for --all-objects, objectId and dataset
// otherwise.
func addressQueryBody(body map[string]any, properties, allObjects bool, args []string, dataset string) error {
	switch {
	case properties && allObjects:
		return fmt.Errorf("--properties and --all-objects are different scopes: pass one")
	case properties:
		return nil
	case allObjects:
		if len(args) != 1 {
			return fmt.Errorf("--all-objects takes no objectId")
		}
		if dataset == "" {
			return fmt.Errorf("--dataset is required")
		}
		body["dataset"] = dataset
		return nil
	}
	if len(args) != 2 {
		return fmt.Errorf("objectId is required (or pass --properties for the objects collection, --all-objects for a shared dataset)")
	}
	if dataset == "" {
		return fmt.Errorf("--dataset is required")
	}
	body["objectId"] = args[1]
	body["dataset"] = dataset
	return nil
}

// applyProjection folds the --projection CSV shorthand into an
// untyped request body.
func applyProjection(body map[string]any, spec string) error {
	proj, err := parseProjectionFlag(spec)
	if err != nil || proj == nil {
		return err
	}
	body["projection"] = proj
	return nil
}

// parseProjectionFlag turns the --projection CSV shorthand into the
// wire map. `any,page,-_ver` becomes {"any":1,"page":1,"_ver":-1} — the
// '-' prefix is the same exclude marker --sort uses for descending, so
// the two flags read alike. Returns nil for an empty spec.
func parseProjectionFlag(spec string) (map[string]int, error) {
	if spec == "" {
		return nil, nil
	}
	proj := map[string]int{}
	for _, field := range splitCSV(spec) {
		// Trim so `--projection 'any, page'` reads the way it looks; an
		// untrimmed " page" is a legal path server-side that matches
		// nothing, which would silently drop the field.
		field = strings.TrimSpace(field)
		mark := 1
		if strings.HasPrefix(field, "-") {
			field, mark = strings.TrimSpace(field[1:]), -1
		}
		if field == "" {
			return nil, fmt.Errorf("--projection: empty field path")
		}
		proj[field] = mark
	}
	return proj, nil
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
