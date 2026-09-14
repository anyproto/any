package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"
	"github.com/anyproto/any-sync-sdk/space"
	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
	"github.com/anyproto/any/internal/indexer"
)

// maxSearchLimit caps one search response.
const maxSearchLimit = 100

// search runs the local-index search (FTS / vector / hybrid). This is
// the one sanctioned endpoint that does not map 1:1 onto an SDK method:
// the index is a consumer-side feature built on the chunker feed (see
// docs/13-index.md).
//
//	@Summary	Search the space's local index
//	@Tags		search
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string				true	"Space ID"
//	@Param		body	body		api.SearchRequest	true	"Search request"
//	@Success	200		{object}	api.SearchResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/search [post]
func (d *deps) search(c echo.Context) error {
	// Body validation before space resolution so 400s don't pay for a
	// space lookup.
	req, ok := bindBodyStrict[api.SearchRequest](c, "")
	if !ok {
		return nil
	}
	if req.Query == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "query required", nil)
	}
	switch req.Mode {
	case "", api.SearchModeHybrid, api.SearchModeFTS, api.SearchModeVector:
	default:
		return writeError(c, http.StatusBadRequest, "search.bad_mode",
			"mode must be hybrid, fts or vector", map[string]any{"mode": req.Mode})
	}
	for _, sc := range req.Scopes {
		// Scopes are an open set (property meta flags mint new ones);
		// only the slug shape is validated. Unknown scopes return no
		// hits rather than erroring.
		if !index.ValidScope(sc) {
			return writeError(c, http.StatusBadRequest, "search.bad_scope",
				"scope must be a short slug ([a-z0-9_-], max 64)", map[string]any{"scope": sc})
		}
	}
	if req.Limit < 0 {
		return writeError(c, http.StatusBadRequest, "request.bad", "limit must be >= 0", nil)
	}
	if req.Limit > maxSearchLimit {
		req.Limit = maxSearchLimit
	}
	if req.MaxData < -1 {
		return writeError(c, http.StatusBadRequest, "request.invalid_field", "maxData must be >= -1 (-1 = unbounded)", map[string]any{"field": "maxData"})
	}
	if req.Passages < 0 || req.Passages > api.MaxSearchPassages {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			fmt.Sprintf("passages must be in [0, %d]", api.MaxSearchPassages), map[string]any{"field": "passages"})
	}

	if d.indexer == nil {
		return writeError(c, http.StatusConflict, "index.disabled",
			"the search index is disabled on this server (index.enabled)", nil)
	}
	if req.Mode == api.SearchModeVector && !d.indexer.HasEmbedder() {
		return writeError(c, http.StatusBadRequest, "index.no_embedder",
			"vector search needs an embedder configured (index.embedder)", nil)
	}
	if fts, _ := indexer.CompiledCaps(); termFilterUnsupported(fts, req) {
		return writeError(c, http.StatusConflict, "index.terms_unsupported",
			"this build has no full-text index, so require/exclude cannot be enforced", nil)
	}
	cond, errResp, done := parseSearchFilter(c, req.Filter)
	if done {
		return errResp
	}

	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	var host indexer.HostFilter
	if cond != nil {
		host = objectsHostFilter{sp: sp, cond: cond}
	}

	res, err := d.indexer.Search(c.Request().Context(), sp.Id(), *req, host)
	if err != nil {
		if errors.Is(err, indexer.ErrEmbedderUnavailable) {
			return writeError(c, http.StatusServiceUnavailable, "index.embedder_unavailable",
				"the embedder is not reachable right now — retry, or use mode fts/hybrid", nil)
		}
		return writeError(c, http.StatusInternalServerError, "internal", "search failed", nil)
	}
	return c.JSON(http.StatusOK, res)
}

// termFilterUnsupported reports whether the request carries term
// constraints this build cannot enforce. `require` / `exclude` bind every
// hit in every mode, and both legs enforce them through the FTS index —
// the lexical leg as query clauses, the vector leg as a post-filter
// (Store.FilterTerms). Without that index the post-filter passes hits
// through unchanged, so a vector-only build would answer with the
// constraint silently ignored; refusing is the honest answer.
func termFilterUnsupported(ftsCompiled bool, req *api.SearchRequest) bool {
	return !ftsCompiled && (len(req.Require) > 0 || len(req.Exclude) > 0)
}

// parseSearchFilter parses the request's object filter at the boundary
// — the /objects/query grammar, the same 400s (filter.invalid /
// filter.unknown_operator) — so a bad filter never surfaces from inside
// a leg. Absent and null mean no filter.
func parseSearchFilter(c echo.Context, raw json.RawMessage) (query.Filter, error, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil, false
	}
	cond, err := query.ParseCondition(string(raw))
	if err != nil {
		var pe *query.ParseError
		if errors.As(err, &pe) {
			return nil, filterParseError(c, pe, nil), true
		}
		return nil, writeError(c, http.StatusBadRequest, "filter.invalid",
			"invalid filter: "+err.Error(), nil), true
	}
	return cond, nil, false
}

// objectsHostFilter is the request's filter over the space's objects
// collection: Resolve lists the matching ids (bounded when asked),
// Match checks a batch of ids in one query restricted to them (a
// primary-key $in) and the condition. Tombstoned objects never match —
// the iterator skips them, and their index docs are evicted anyway.
type objectsHostFilter struct {
	sp   space.Space
	cond query.Filter
}

// Resolve walks an UNBOUNDED iterator and stops once max ids are in
// hand — never Query.Limit: the SDK applies a limit before it skips the
// collection's tombstones, so a bounded query can end short of max
// with the set still continuing. An early Close is free.
func (f objectsHostFilter) Resolve(ctx context.Context, max int) ([]string, bool, error) {
	it, err := f.sp.QueryObjects().Filter(f.cond).Iter(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = it.Close() }()
	var ids []string
	for it.Next() {
		doc, err := it.Doc()
		if err != nil {
			return nil, false, err
		}
		if max > 0 && len(ids) == max {
			return ids, true, nil
		}
		ids = append(ids, string(doc.GetStringBytes("id")))
	}
	return ids, false, it.Err()
}

func (f objectsHostFilter) Match(ctx context.Context, ids []string) (map[string]bool, error) {
	arena := &anyenc.Arena{}
	vals := make([]*anyenc.Value, len(ids))
	for i, id := range ids {
		vals[i] = arena.NewString(id)
	}
	it, err := f.sp.QueryObjects().Filter(query.And{
		query.Key{Path: []string{"id"}, Filter: query.NewInValue(vals...)},
		f.cond,
	}).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = it.Close() }()
	matched := make(map[string]bool, len(ids))
	for it.Next() {
		doc, err := it.Doc()
		if err != nil {
			return nil, err
		}
		matched[string(doc.GetStringBytes("id"))] = true
	}
	return matched, it.Err()
}
