package server

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// spaceQueryObjects handles POST /v1/spaces/:spaceId/objects/query.
//
//	@Summary	Query objects in a space
//	@Tags		objects
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string							true	"Space ID"
//	@Param		body	body		api.SpaceQueryObjectsRequest	true	"Query params"
//	@Success	200		{object}	api.QueryResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/query [post]
func (d *deps) spaceQueryObjects(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	body, err := readBody(c)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "unreadable body", nil)
	}

	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	var root *fastjson.Value
	if len(body) > 0 {
		root, err = parser.ParseBytes(body)
		if err != nil {
			return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil)
		}
	}

	q := sp.QueryObjects()
	if root != nil {
		if filter := root.Get("filter"); filter != nil && filter.Type() != fastjson.TypeNull {
			q = q.Filter(filter)
		}
		if sortArr := root.GetArray("sort"); len(sortArr) > 0 {
			keys := make([]any, 0, len(sortArr))
			for _, s := range sortArr {
				keys = append(keys, string(s.GetStringBytes()))
			}
			q = q.Sort(keys...)
		}
		if v := root.Get("limit"); v != nil {
			if n := v.GetInt(); n > 0 {
				q = q.Limit(n)
			}
		}
		if v := root.Get("offset"); v != nil {
			if n := v.GetInt(); n > 0 {
				q = q.Offset(n)
			}
		}
		// `projection` accepted but ignored — see roadmap.
	}

	docs, err := q.All(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}

	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	records := make([]json.RawMessage, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			records = append(records, json.RawMessage("null"))
			continue
		}
		records = append(records, json.RawMessage(doc.FastJson(fa).MarshalTo(nil)))
	}
	return c.JSON(http.StatusOK, api.QueryResponse{Records: records})
}

// spaceQuery handles POST /v1/spaces/:spaceId/query.
//
//	@Summary	Query a per-object dataset
//	@Tags		data
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string				true	"Space ID"
//	@Param		body	body		api.SpaceQueryRequest	true	"Query params (objectId+dataset required)"
//	@Success	200		{object}	api.QueryResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/query [post]
func (d *deps) spaceQuery(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	body, err := readBody(c)
	if err != nil || len(body) == 0 {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "missing or unreadable body", nil)
	}

	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	root, err := parser.ParseBytes(body)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil)
	}

	var (
		objectId string
		dataset  string
		limit    int
		offset   int
	)
	if v := root.Get("objectId"); v != nil {
		objectId = string(v.GetStringBytes())
	}
	if v := root.Get("dataset"); v != nil {
		dataset = string(v.GetStringBytes())
	}
	if v := root.Get("limit"); v != nil {
		limit = v.GetInt()
	}
	if v := root.Get("offset"); v != nil {
		offset = v.GetInt()
	}
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}
	if dataset == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "dataset required", nil)
	}

	q := sp.Query(objectId, dataset)
	if filter := root.Get("filter"); filter != nil && filter.Type() != fastjson.TypeNull {
		q = q.Filter(filter)
	}
	if sortArr := root.GetArray("sort"); len(sortArr) > 0 {
		keys := make([]any, 0, len(sortArr))
		for _, s := range sortArr {
			keys = append(keys, string(s.GetStringBytes()))
		}
		q = q.Sort(keys...)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	// `projection` accepted but ignored — see roadmap.
	_ = space.ProjectionOpts{}

	docs, err := q.All(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, map[string]any{
			"spaceId": sp.Id(), "objectId": objectId, "dataset": dataset,
		})
	}

	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	records := make([]json.RawMessage, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			records = append(records, json.RawMessage("null"))
			continue
		}
		records = append(records, json.RawMessage(doc.FastJson(fa).MarshalTo(nil)))
	}
	return c.JSON(http.StatusOK, api.QueryResponse{Records: records})
}
