package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// `any local ...` — the local store: device-local, non-CRDT any-store
// collections that never sync (docs/26-local-store.md). One
// subcommand per /v1/local endpoint. Every data command takes the
// collection NAME as its first argument; `--space ID` binds it to a
// space, otherwise it is account-scoped.
func newLocalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "local",
		Short: "local store: device-local, never-synced collections with any-store query/aggregate",
		Long: `Device-local, non-CRDT collections in the server's own any-store DB.
Nothing here syncs, subscribes, or is search-indexed; a space-scoped
collection outlives its space. See docs/26-local-store.md.

  any local ensure scratch --index k,-at
  any local insert scratch --doc '[{"id":"a","k":1},{"k":2}]'
  any local query scratch --filter '{"k":{"$gt":0}}' --sort -k --total
  any local aggregate scratch --pipeline '[{"$group":{"_id":null,"n":{"$count":{}}}}]'
`,
	}
	cmd.AddCommand(
		newLocalMetaCmd(),
		newLocalCollectionsCmd(),
		newLocalEnsureCmd(),
		newLocalDropCmd(),
		newLocalInsertCmd(),
		newLocalUpsertCmd(),
		newLocalUpdateCmd(),
		newLocalDeleteCmd(),
		newLocalGetCmd(),
		newLocalQueryCmd(),
		newLocalAggregateCmd(),
		newLocalIndexesCmd(),
	)
	return cmd
}

// localColl builds the wire collection ref from NAME + --space.
func localColl(name, spaceId string) api.LocalCollection {
	if spaceId != "" {
		return api.LocalCollection{Scope: "space", SpaceId: spaceId, Name: name}
	}
	return api.LocalCollection{Scope: "account", Name: name}
}

// localIndexFlags turns repeatable --index / --unique-index values
// ("a,-b" field lists) into wire indexes.
func localIndexFlags(plain, unique []string) []api.LocalIndex {
	var out []api.LocalIndex
	for _, spec := range plain {
		out = append(out, api.LocalIndex{Fields: splitFields(spec)})
	}
	for _, spec := range unique {
		out = append(out, api.LocalIndex{Fields: splitFields(spec), Unique: true})
	}
	return out
}

func splitFields(spec string) []string {
	var fields []string
	for _, f := range strings.Split(spec, ",") {
		if f = strings.TrimSpace(f); f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}

// readDocs accepts a single JSON object or an array of them (inline,
// @FILE, or - for stdin).
func readDocs(raw string) ([]map[string]any, error) {
	var msg json.RawMessage
	if err := readJSONBody(raw, &msg); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(msg)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var one map[string]any
		if err := json.Unmarshal(trimmed, &one); err != nil {
			return nil, err
		}
		return []map[string]any{one}, nil
	}
	var many []map[string]any
	if err := json.Unmarshal(trimmed, &many); err != nil {
		return nil, fmt.Errorf("--doc must be a JSON object or an array of objects: %w", err)
	}
	return many, nil
}

func newLocalMetaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "meta",
		Short: "the aggregation stage and accumulator vocabulary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := client.New(flags.Addr, flags.Timeout).LocalMeta(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

func newLocalCollectionsCmd() *cobra.Command {
	var scope, spaceId string
	cmd := &cobra.Command{
		Use:   "collections",
		Short: "list local collections (count, indexes, storage name)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := client.New(flags.Addr, flags.Timeout).LocalCollections(cmd.Context(), scope, spaceId)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "account | space (default: both)")
	cmd.Flags().StringVar(&spaceId, "space", "", "only this space's collections")
	return cmd
}

func newLocalEnsureCmd() *cobra.Command {
	var spaceId string
	var indexes, unique []string
	cmd := &cobra.Command{
		Use:   "ensure <name>",
		Short: "create a collection if absent (idempotent) and ensure its indexes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := client.New(flags.Addr, flags.Timeout).LocalEnsure(cmd.Context(), api.LocalEnsureRequest{
				LocalCollection: localColl(args[0], spaceId),
				Indexes:         localIndexFlags(indexes, unique),
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&spaceId, "space", "", "bind the collection to this space")
	cmd.Flags().StringArrayVar(&indexes, "index", nil, "index field list, e.g. 'kind,-at' (repeatable)")
	cmd.Flags().StringArrayVar(&unique, "unique-index", nil, "unique index field list (repeatable)")
	return cmd
}

func newLocalDropCmd() *cobra.Command {
	var spaceId string
	var yes bool
	cmd := &cobra.Command{
		Use:   "drop <name> --yes",
		Short: "drop a collection and all its data (irreversible; requires --yes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to drop %s without --yes (local data has no backup)", args[0])
			}
			return client.New(flags.Addr, flags.Timeout).LocalDrop(cmd.Context(), localColl(args[0], spaceId))
		},
	}
	cmd.Flags().StringVar(&spaceId, "space", "", "the collection's space")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the drop")
	return cmd
}

func newLocalDocsCmd(use, short string, call func(*client.Client, *cobra.Command, api.LocalDocsRequest) (*api.LocalIdsResponse, error)) *cobra.Command {
	var spaceId, doc string
	cmd := &cobra.Command{
		Use:   use + " <name> --doc JSON",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			docs, err := readDocs(doc)
			if err != nil {
				return fmt.Errorf("--doc: %w", err)
			}
			out, err := call(client.New(flags.Addr, flags.Timeout), cmd, api.LocalDocsRequest{
				Coll: localColl(args[0], spaceId),
				Docs: docs,
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&spaceId, "space", "", "the collection's space")
	cmd.Flags().StringVar(&doc, "doc", "", "one JSON object or an array of them (inline, @FILE, or - for stdin)")
	return cmd
}

func newLocalInsertCmd() *cobra.Command {
	return newLocalDocsCmd("insert", "insert documents (a missing id is minted; an existing id is a 409)",
		func(cl *client.Client, cmd *cobra.Command, req api.LocalDocsRequest) (*api.LocalIdsResponse, error) {
			return cl.LocalInsert(cmd.Context(), req)
		})
}

func newLocalUpsertCmd() *cobra.Command {
	return newLocalDocsCmd("upsert", "insert or replace whole documents by id",
		func(cl *client.Client, cmd *cobra.Command, req api.LocalDocsRequest) (*api.LocalIdsResponse, error) {
			return cl.LocalUpsert(cmd.Context(), req)
		})
}

func newLocalUpdateCmd() *cobra.Command {
	var spaceId, modifier string
	var upsert bool
	cmd := &cobra.Command{
		Use:   "update <name> <id> --modifier JSON",
		Short: "apply a mongo-style modifier ($set/$unset/$inc/…) to one document",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var mod map[string]any
			if err := readJSONBody(modifier, &mod); err != nil {
				return fmt.Errorf("--modifier: %w", err)
			}
			out, err := client.New(flags.Addr, flags.Timeout).LocalUpdate(cmd.Context(), api.LocalUpdateRequest{
				Coll:     localColl(args[0], spaceId),
				Id:       args[1],
				Modifier: mod,
				Upsert:   upsert,
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&spaceId, "space", "", "the collection's space")
	cmd.Flags().StringVar(&modifier, "modifier", "", "modifier object (inline, @FILE, or - for stdin)")
	cmd.Flags().BoolVar(&upsert, "upsert", false, "create the document from the modifier when the id is absent")
	return cmd
}

func newLocalDeleteCmd() *cobra.Command {
	var spaceId, filter string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name> [<id>...] [--filter JSON] --yes",
		Short: "delete documents by ids or by filter (requires --yes)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := args[1:]
			if (len(ids) == 0) == (filter == "") {
				return fmt.Errorf("pass either ids or --filter")
			}
			for _, id := range ids {
				if strings.HasPrefix(id, "{") || strings.HasPrefix(id, "@") || id == "-" {
					return fmt.Errorf("%q looks like a filter, not an id — pass it as --filter %s", id, id)
				}
			}
			if !yes {
				return fmt.Errorf("refusing to delete without --yes (local data has no backup)")
			}
			req := api.LocalDeleteRequest{Coll: localColl(args[0], spaceId), Ids: ids}
			if filter != "" {
				if err := readJSONBody(filter, &req.Filter); err != nil {
					return fmt.Errorf("--filter: %w", err)
				}
				if req.Filter == nil {
					req.Filter = map[string]any{}
				}
			}
			out, err := client.New(flags.Addr, flags.Timeout).LocalDelete(cmd.Context(), req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&spaceId, "space", "", "the collection's space")
	cmd.Flags().StringVar(&filter, "filter", "", "delete every match of this filter ('{}' = everything)")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the delete")
	return cmd
}

func newLocalGetCmd() *cobra.Command {
	var spaceId string
	cmd := &cobra.Command{
		Use:   "get <name> <id>",
		Short: "read one document by id",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := client.New(flags.Addr, flags.Timeout).LocalGet(cmd.Context(), api.LocalGetRequest{
				Coll: localColl(args[0], spaceId), Id: args[1],
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&spaceId, "space", "", "the collection's space")
	return cmd
}

func newLocalQueryCmd() *cobra.Command {
	var spaceId, filter, projection string
	var sort []string
	var limit, offset int
	var total bool
	cmd := &cobra.Command{
		Use:   "query <name>",
		Short: "query a collection (filter / sort / limit / offset / total)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.LocalQueryRequest{
				Coll: localColl(args[0], spaceId), Sort: sort, Limit: limit, Offset: offset, IncludeTotal: total,
			}
			if filter != "" {
				if err := readJSONBody(filter, &req.Filter); err != nil {
					return fmt.Errorf("--filter: %w", err)
				}
			}
			proj, err := parseProjectionFlag(projection)
			if err != nil {
				return err
			}
			req.Projection = proj
			out, err := client.New(flags.Addr, flags.Timeout).LocalQuery(cmd.Context(), req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&spaceId, "space", "", "the collection's space")
	cmd.Flags().StringVar(&filter, "filter", "", "mongo-style filter (inline, @FILE, or - for stdin)")
	cmd.Flags().StringSliceVar(&sort, "sort", nil, "sort keys, '-' prefix for descending (comma-separated or repeatable)")
	cmd.Flags().IntVar(&limit, "limit", 0, "page size (server default 100, cap 1000)")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip the first N matches")
	cmd.Flags().BoolVar(&total, "total", false, "include the unbounded match count + hasNext")
	cmd.Flags().StringVar(&projection, "projection", "",
		"comma-separated field paths to return, '-' prefix to exclude (e.g. 'title,at' or '-body'); id always rides along")
	return cmd
}

func newLocalAggregateCmd() *cobra.Command {
	var spaceId, pipeline string
	var groupLimit, accumLimit, memLimit int
	var explain bool
	cmd := &cobra.Command{
		Use:   "aggregate <name> --pipeline JSON",
		Short: "run an aggregation pipeline ($out/$merge/$lookup may name local collections by storage name)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.LocalAggregateRequest{Coll: localColl(args[0], spaceId), Explain: explain}
			if err := readJSONBody(pipeline, &req.Pipeline); err != nil {
				return fmt.Errorf("--pipeline: %w", err)
			}
			if cmd.Flags().Changed("group-limit") {
				req.GroupLimit = &groupLimit
			}
			if cmd.Flags().Changed("accum-limit") {
				req.AccumArrayLimit = &accumLimit
			}
			if cmd.Flags().Changed("memory-limit") {
				req.MemoryLimitBytes = &memLimit
			}
			out, err := client.New(flags.Addr, flags.Timeout).LocalAggregate(cmd.Context(), req)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&spaceId, "space", "", "the collection's space")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "JSON array of stages (inline, @FILE, or - for stdin); `any local meta` lists them")
	cmd.Flags().IntVar(&groupLimit, "group-limit", 0, "max unique $group keys (negative = unlimited)")
	cmd.Flags().IntVar(&accumLimit, "accum-limit", 0, "max $push/$addToSet array length (negative = unlimited)")
	cmd.Flags().IntVar(&memLimit, "memory-limit", 0, "blocking-stage memory budget in bytes (negative = unlimited)")
	cmd.Flags().BoolVar(&explain, "explain", false, "return the access plan instead of results (diagnostic, not stable)")
	return cmd
}

func newLocalIndexesCmd() *cobra.Command {
	var spaceId string
	var ensure, unique, drop []string
	cmd := &cobra.Command{
		Use:   "indexes <name> [--ensure 'a,-b'] [--unique-ensure 'k'] [--drop NAME]",
		Short: "ensure / drop indexes and list the result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := client.New(flags.Addr, flags.Timeout).LocalIndexes(cmd.Context(), api.LocalIndexesRequest{
				Coll:   localColl(args[0], spaceId),
				Ensure: localIndexFlags(ensure, unique),
				Drop:   drop,
			})
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.Flags().StringVar(&spaceId, "space", "", "the collection's space")
	cmd.Flags().StringArrayVar(&ensure, "ensure", nil, "index field list to ensure, e.g. 'kind,-at' (repeatable)")
	cmd.Flags().StringArrayVar(&unique, "unique-ensure", nil, "unique index field list to ensure (repeatable)")
	cmd.Flags().StringArrayVar(&drop, "drop", nil, "index name to drop (repeatable)")
	return cmd
}
