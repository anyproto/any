package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/anyuri"
	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
	"github.com/anyproto/any/internal/indexer"
)

// Links and backlinks — reads over the link index the search indexer
// maintains next to its text docs (docs/13-index.md § Links). Like
// `/search`, a consumer-side exception to the 1:1 rule: the SDK has no
// reverse index. `409 index.disabled` when the indexer is off.

// maxLinksLimit caps one links reply.
const maxLinksLimit = 500

func registerLinkRoutes(g *echo.Group, d *deps) {
	g.GET("/spaces/:spaceId/objects/:objectId/backlinks", d.objectBacklinks)
	g.GET("/spaces/:spaceId/objects/:objectId/links", d.objectLinks)
	// Account-wide: every indexed space in one read. Outside the
	// :spaceId group like /datasets.
	g.GET("/backlinks", d.backlinksAll)
}

// linkQueryParams reads the shared narrowing params: `kind`
// (repeatable) and `limit`.
func linkQueryParams(c echo.Context) (indexer.LinkQuery, error, bool) {
	var q indexer.LinkQuery
	for _, k := range c.QueryParams()["kind"] {
		// Kinds are an open slug set like scopes; the same shape check.
		if !index.ValidScope(k) {
			return q, writeError(c, http.StatusBadRequest, "request.invalid_field",
				"kind must be a short slug ([a-z0-9_-], max 64)", map[string]any{"field": "kind"}), true
		}
		q.Kinds = append(q.Kinds, k)
	}
	if raw := c.QueryParam("limit"); raw != "" {
		n, ok := parseIntParam(raw)
		if !ok {
			return q, writeError(c, http.StatusBadRequest, "request.invalid_field",
				"limit must be a non-negative integer", map[string]any{"field": "limit"}), true
		}
		q.Limit = n
	}
	if q.Limit <= 0 || q.Limit > maxLinksLimit {
		q.Limit = maxLinksLimit
	}
	return q, nil, false
}

func parseIntParam(s string) (int, bool) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
		if n > maxLinksLimit {
			return maxLinksLimit, true // any larger value clamps to the cap
		}
	}
	return n, len(s) > 0
}

// partParams reads the optional part narrowing: `record` + `dataset`
// (a record needs its dataset; a dataset alone is admitted only when
// datasetAlone — the forward read of one collection) or `prop`.
// Returns the part's dataset and record id under the link-source
// vocabulary (`prop` + propId for a value).
func partParams(c echo.Context, datasetAlone bool) (dataset, recordId string, errResp error, done bool) {
	record, ds, prop := c.QueryParam("record"), c.QueryParam("dataset"), c.QueryParam("prop")
	switch {
	case prop != "" && (record != "" || ds != ""):
		return "", "", writeError(c, http.StatusBadRequest, "request.invalid_field",
			"prop cannot be combined with record/dataset", map[string]any{"field": "prop"}), true
	case prop != "":
		return index.DatasetProp, prop, nil, false
	case record != "" && ds == "":
		return "", "", writeError(c, http.StatusBadRequest, "request.missing_field",
			"record needs its dataset", nil), true
	case record == "" && ds != "" && !datasetAlone:
		return "", "", writeError(c, http.StatusBadRequest, "request.missing_field",
			"dataset needs a record", nil), true
	}
	return ds, record, nil, false
}

// objectBacklinks handles GET /v1/spaces/:spaceId/objects/:objectId/backlinks.
//
// No existence check on objectId — backlinks of an unknown (or
// deleted) object is an empty reply, not a 404.
//
//	@Summary	List the edges pointing at an object, or at one of its records / property values
//	@Tags		links
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Param		record		query		string	false	"Narrow to one record (with dataset)"
//	@Param		dataset		query		string	false	"The record's collection"
//	@Param		prop		query		string	false	"Narrow to one property value (propId)"
//	@Param		kind		query		[]string	false	"Edge kinds to keep (repeatable)"
//	@Param		limit		query		int		false	"Max edges (default and max 500)"
//	@Success	200			{object}	api.BacklinksResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	409			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/backlinks [get]
func (d *deps) objectBacklinks(c echo.Context) error {
	q, errResp, done := linkQueryParams(c)
	if done {
		return errResp
	}
	dataset, recordId, errResp, done := partParams(c, false)
	if done {
		return errResp
	}
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	if d.indexer == nil {
		return indexDisabled(c)
	}
	target := anyuri.URI{Kind: anyuri.KindObject, SpaceId: sp.Id(), ObjectId: objectId}
	narrowed := recordId != ""
	switch {
	case dataset == index.DatasetProp:
		target = anyuri.URI{Kind: anyuri.KindProp, SpaceId: sp.Id(), ObjectId: objectId, PropId: recordId}
	case narrowed:
		target.Dataset, target.RecordId = dataset, recordId
	}
	docs, more, err := d.indexer.Backlinks(c.Request().Context(), sp.Id(), target, q)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	out := splitBacklinks(sp.Id(), docs, narrowed)
	out.Truncated = more
	return c.JSON(http.StatusOK, out)
}

// splitBacklinks groups edges into object-level and part-level; a
// narrowed read keeps everything under Object.
func splitBacklinks(spaceId string, docs []indexer.LinkDoc, narrowed bool) api.BacklinksResponse {
	out := api.BacklinksResponse{Object: []api.Link{}, Parts: []api.Link{}}
	for _, doc := range docs {
		l := linkToAPI(spaceId, doc)
		if !narrowed && doc.Target.IsPart() {
			out.Parts = append(out.Parts, l)
		} else {
			out.Object = append(out.Object, l)
		}
	}
	return out
}

// objectLinks handles GET /v1/spaces/:spaceId/objects/:objectId/links.
//
//	@Summary	List the edges an object (or one of its records / property values) points at
//	@Tags		links
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Param		record		query		string	false	"Narrow to one record (with dataset)"
//	@Param		dataset		query		string	false	"The record's collection, or alone: one collection's edges"
//	@Param		prop		query		string	false	"Narrow to one property value (propId)"
//	@Param		kind		query		[]string	false	"Edge kinds to keep (repeatable)"
//	@Param		limit		query		int		false	"Max edges (default and max 500)"
//	@Success	200			{object}	api.LinksResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	409			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/links [get]
func (d *deps) objectLinks(c echo.Context) error {
	q, errResp, done := linkQueryParams(c)
	if done {
		return errResp
	}
	dataset, recordId, errResp, done := partParams(c, true)
	if done {
		return errResp
	}
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	if d.indexer == nil {
		return indexDisabled(c)
	}
	docs, more, err := d.indexer.Links(c.Request().Context(), sp.Id(), objectId, dataset, recordId, q)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	out := api.LinksResponse{Links: []api.Link{}, Truncated: more}
	for _, doc := range docs {
		out.Links = append(out.Links, linkToAPI(sp.Id(), doc))
	}
	return c.JSON(http.StatusOK, out)
}

// backlinksAll handles GET /v1/backlinks?target=<uri>.
//
//	@Summary	List the edges pointing at a target from every indexed space
//	@Tags		links
//	@Produce	json
//	@Param		target	query		string	true	"Canonical any:// target (object, record, property value, identity or file)"
//	@Param		kind	query		[]string	false	"Edge kinds to keep (repeatable)"
//	@Param		limit	query		int		false	"Max edges per space (default and max 500)"
//	@Success	200		{object}	api.BacklinksAllResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/backlinks [get]
func (d *deps) backlinksAll(c echo.Context) error {
	q, errResp, done := linkQueryParams(c)
	if done {
		return errResp
	}
	raw := c.QueryParam("target")
	if raw == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "target required", nil)
	}
	u, err := anyuri.Parse(raw)
	if err != nil {
		if errors.Is(err, anyuri.ErrKindUnknown) {
			return writeError(c, http.StatusBadRequest, "request.invalid_field", "target: unknown link kind", map[string]any{"field": "target"})
		}
		return writeError(c, http.StatusBadRequest, "request.invalid_field", "target: not a valid any:// URI", map[string]any{"field": "target"})
	}
	target, ok := u.Canonical("")
	if !ok {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"target must be a global any:// reference to an object, record, property value, identity or file", map[string]any{"field": "target"})
	}
	if d.indexer == nil {
		return indexDisabled(c)
	}
	res, err := d.indexer.BacklinksAll(c.Request().Context(), target, q)
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	narrowed := target.IsPart() || target.Kind != anyuri.KindObject
	out := api.BacklinksAllResponse{Spaces: []api.SpaceBacklinks{}}
	for _, sb := range res {
		split := splitBacklinks(sb.SpaceId, sb.Links, narrowed)
		out.Spaces = append(out.Spaces, api.SpaceBacklinks{SpaceId: sb.SpaceId, Object: split.Object, Parts: split.Parts, Truncated: sb.More})
	}
	return c.JSON(http.StatusOK, out)
}

func indexDisabled(c echo.Context) error {
	return writeError(c, http.StatusConflict, "index.disabled",
		"the search index is disabled on this server (index.enabled)", nil)
}

// linkToAPI renders one stored edge.
func linkToAPI(spaceId string, doc indexer.LinkDoc) api.Link {
	t := doc.Target
	return api.Link{
		Source: api.LinkSource{SpaceId: spaceId, ObjectId: doc.ObjectId, Dataset: doc.Dataset, RecordId: doc.RecordId, TypeId: doc.TypeId, Field: doc.Field},
		Kind:   doc.Kind,
		Target: api.LinkTarget{
			Uri:      t.String(),
			Kind:     string(t.Kind),
			SpaceId:  t.SpaceId,
			ObjectId: t.ObjectId,
			Dataset:  t.Dataset,
			RecordId: t.RecordId,
			PropId:   t.PropId,
			Identity: t.Identity,
			FileId:   t.FileId,
		},
	}
}
