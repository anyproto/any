package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
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

	cmd.AddCommand(newSpaceGetCmd(), newSpaceUpdateCmd(), newSpaceSettingsCmd(),
		newSpaceSyncCmd(), newSpaceDeleteCmd(), newSpaceQueryCmd(),
		newSpaceSubscribeCmd(), newSpaceDerivedCmd())
	return cmd
}

// newSpaceDerivedCmd: `any space derived` lists the well-known
// derived-space registry resolved against the account (name, spaceId,
// created); `any space derived create <name>` materializes one
// (idempotent). Derived spaces are permanent — delete refuses them.
func newSpaceDerivedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "derived",
		Short: "well-known derived spaces (list; `create <name>` materializes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.SpaceDerivedList(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "create <name>",
		Short: "materialize a well-known derived space by registry name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.SpaceDerivedCreate(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	})
	return cmd
}

// newSpaceSettingsCmd: `any space settings <spaceId> --set key=value
// ... --unset key ...` — PATCH /v1/spaces/:id/settings, the
// account-private per-space settings patch (Spaces().SetSettings).
// Values are scalars: --set takes the value as a bare string;
// --set-bool / --set-num are the typed variants (explicit flags
// instead of literal-sniffing, so "true" the string stays writable).
// Keys are single-level (no dots). Prints nothing on success.
func newSpaceSettingsCmd() *cobra.Command {
	var (
		set     []string
		setBool []string
		setNum  []string
		unset   []string
	)
	cmd := &cobra.Command{
		Use:   "settings <spaceId> [--set k=v]... [--set-bool k=true|false]... [--set-num k=N]... [--unset k]...",
		Short: "patch the account-private per-space settings (e.g. notifyMode)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := buildSettingsPatch(set, setBool, setNum, unset)
			if err != nil {
				return err
			}
			cl := newClient(flags.Timeout)
			return cl.SpaceSettingsPatch(cmd.Context(), args[0], req)
		},
	}
	cmd.Flags().StringArrayVar(&set, "set", nil, "set a string value: key=value (repeatable)")
	cmd.Flags().StringArrayVar(&setBool, "set-bool", nil, "set a boolean value: key=true|false (repeatable)")
	cmd.Flags().StringArrayVar(&setNum, "set-num", nil, "set a numeric value: key=N (repeatable)")
	cmd.Flags().StringArrayVar(&unset, "unset", nil, "remove a key (repeatable)")
	return cmd
}

// buildSettingsPatch assembles the SpaceSettingsPatchRequest from the
// typed --set* flag groups. Client-side checks are only the ones the
// server can't phrase better: entry syntax, typed-value parses, and
// duplicate keys across the set flags (a map would silently drop one).
// Everything else (key shape, set/unset overlap) is the server's 400.
func buildSettingsPatch(set, setBool, setNum, unset []string) (api.SpaceSettingsPatchRequest, error) {
	req := api.SpaceSettingsPatchRequest{Unset: unset}
	if len(set)+len(setBool)+len(setNum)+len(unset) == 0 {
		return req, fmt.Errorf("nothing to do: pass at least one --set / --set-bool / --set-num / --unset")
	}
	add := func(entry string, parse func(string) (any, error)) error {
		key, raw, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return fmt.Errorf("bad entry %q: want key=value", entry)
		}
		val, err := parse(raw)
		if err != nil {
			return fmt.Errorf("bad value for %q: %w", key, err)
		}
		if req.Set == nil {
			req.Set = make(map[string]any)
		}
		if _, dup := req.Set[key]; dup {
			return fmt.Errorf("key %q set more than once", key)
		}
		req.Set[key] = val
		return nil
	}
	for _, e := range set {
		if err := add(e, func(raw string) (any, error) { return raw, nil }); err != nil {
			return req, err
		}
	}
	for _, e := range setBool {
		if err := add(e, func(raw string) (any, error) { return strconv.ParseBool(raw) }); err != nil {
			return req, err
		}
	}
	for _, e := range setNum {
		if err := add(e, func(raw string) (any, error) { return strconv.ParseFloat(raw, 64) }); err != nil {
			return req, err
		}
	}
	return req, nil
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
			cl := newClient(flags.Timeout)
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
		projection string
		limit      int
		offset     int
		includeTot bool
	)
	cmd := &cobra.Command{
		Use:   "query",
		Short: "windowed snapshot over the account's space list (filter/sort/limit)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := buildSpaceListQueryBody(dataset, filter, sort, projection, limit, offset, includeTot)
			if err != nil {
				return err
			}
			cl := newClient(flags.Timeout)
			out, err := cl.SpaceListQuery(cmd.Context(), body)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addSpaceListQueryFlags(cmd, &dataset, &filter, &sort, &projection, &limit, &offset, &includeTot)
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
		projection string
		limit      int
		offset     int
		includeTot bool
	)
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "open a windowed space-list SSE stream (spaces added/changed/removed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := buildSpaceListQueryBody(dataset, filter, sort, projection, limit, offset, includeTot)
			if err != nil {
				return err
			}
			cl := newClient(0) // timeout doesn't apply to streams
			return cl.StreamSpaceListQuerySubscribe(cmd.Context(), body, jsonFrameHandler())
		},
	}
	addSpaceListQueryFlags(cmd, &dataset, &filter, &sort, &projection, &limit, &offset, &includeTot)
	return cmd
}

func addSpaceListQueryFlags(cmd *cobra.Command, dataset, filter, sort, projection *string, limit, offset *int, includeTot *bool) {
	cmd.Flags().StringVar(dataset, "dataset", "", "system dataset to read (default spaces; profile also available)")
	addWindowQueryFlags(cmd, filter, sort, projection, limit, offset, includeTot)
}

// buildSpaceListQueryBody assembles the request body for the space-list
// query/subscribe endpoints. All fields optional — an empty body is a
// full snapshot. objectId is server-fixed to the tech-space index.
func buildSpaceListQueryBody(dataset, filter, sort, projection string, limit, offset int, includeTotal bool) ([]byte, error) {
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
	if err := applyProjection(body, projection); err != nil {
		return nil, err
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
			cl := newClient(flags.Timeout)
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
			cl := newClient(flags.Timeout)
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
			cl := newClient(flags.Timeout)
			return cl.SpaceUpdate(cmd.Context(), args[0], req)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new display name (set --name='' to clear)")
	cmd.Flags().StringVar(&desc, "description", "", "new description")
	cmd.Flags().StringVar(&iconCID, "icon-cid", "", "new icon CID")
	return cmd
}
