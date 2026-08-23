package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-store/v2/query"
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
	q, opts, errResp, done := buildSharedQuery(c, sp)
	if done {
		return errResp
	}
	res, err := q.Snapshot(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return writeQueryResponse(c, res, opts.IncludeTotal)
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
	q, opts, objectId, dataset, errResp, done := buildPerObjectQuery(c, sp)
	if done {
		return errResp
	}
	strip, errResp, done := d.techIndexFence(c, sp, objectId, dataset)
	if done {
		return errResp
	}
	res, err := q.Snapshot(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{
			"spaceId": sp.Id(), "objectId": objectId, "dataset": dataset,
		})
	}
	return writeQueryResponse(c, res, opts.IncludeTotal, strip...)
}

// buildBodyQuery is the one implementation of the OPTIONAL windowed-
// query body pipeline (an empty body is a full snapshot): read → parse
// → strict unknown-field gate against fields → boundary filter check →
// applyQueryParams. Every builder over an optional body goes through it
// so the 400 surface cannot drift between endpoints. base receives the
// parsed root (nil for an empty body) and returns the base Query to
// window — or a written error response with done=true (e.g. the
// space-list dataset allowlist); it runs before checkFilter, keeping
// each builder's historical validation order. buildPerObjectQuery
// stays separate: its body is required, not optional.
func buildBodyQuery(c echo.Context, fields []string, base func(root *fastjson.Value) (space.Query, error, bool)) (space.Query, space.QueryOpts, error, bool) {
	body, err := readBody(c)
	if err != nil {
		return nil, space.QueryOpts{}, writeError(c, http.StatusBadRequest, "request.bad_json", "unreadable body", nil), true
	}
	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	var root *fastjson.Value
	if len(body) > 0 {
		root, err = parser.ParseBytes(body)
		if err != nil {
			return nil, space.QueryOpts{}, writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil), true
		}
	}
	if errResp, done := checkUnknownFields(c, root, "", fields...); done {
		return nil, space.QueryOpts{}, errResp, true
	}
	q, errResp, done := base(root)
	if done {
		return nil, space.QueryOpts{}, errResp, true
	}
	if errResp, done := checkFilter(c, root); done {
		return nil, space.QueryOpts{}, errResp, true
	}
	q, opts := applyQueryParams(root, q)
	return q, opts, nil, false
}

// buildSharedQuery parses the request body for the QueryObjects (per-
// space `objects` collection) endpoints and assembles the chained
// Query plus its QueryOpts. Returns (q, opts, errResp, done=true) on
// validation failure; the caller returns errResp directly in that
// case. Shared between the snapshot and subscribe handlers so the body
// shape stays in lockstep.
func buildSharedQuery(c echo.Context, sp space.Space) (space.Query, space.QueryOpts, error, bool) {
	return buildBodyQuery(c, queryBodyFields, func(*fastjson.Value) (space.Query, error, bool) {
		return sp.QueryObjects(), nil, false
	})
}

// buildPerObjectQuery is the per-object dataset counterpart to
// buildSharedQuery. objectId and dataset are required body fields; a
// missing or empty value short-circuits with 400 request.missing_field.
func buildPerObjectQuery(c echo.Context, sp space.Space) (space.Query, space.QueryOpts, string, string, error, bool) {
	body, err := readBody(c)
	if err != nil || len(body) == 0 {
		return nil, space.QueryOpts{}, "", "", writeError(c, http.StatusBadRequest, "request.bad_json", "missing or unreadable body", nil), true
	}
	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	root, err := parser.ParseBytes(body)
	if err != nil {
		return nil, space.QueryOpts{}, "", "", writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil), true
	}
	if errResp, done := checkUnknownFields(c, root, "", perObjectQueryFields...); done {
		return nil, space.QueryOpts{}, "", "", errResp, true
	}
	objectId := string(root.GetStringBytes("objectId"))
	dataset := string(root.GetStringBytes("dataset"))
	if objectId == "" {
		return nil, space.QueryOpts{}, "", "", writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil), true
	}
	if isSerializedNil(objectId) {
		return nil, space.QueryOpts{}, "", "", serializedNilIdError(c, "objectId", objectId), true
	}
	if dataset == "" {
		return nil, space.QueryOpts{}, "", "", writeError(c, http.StatusBadRequest, "request.missing_field", "dataset required", nil), true
	}
	if errResp, done := checkFilter(c, root); done {
		return nil, space.QueryOpts{}, "", "", errResp, true
	}
	q, opts := applyQueryParams(root, sp.Query(objectId, dataset))
	return q, opts, objectId, dataset, nil, false
}

// The closed top-level vocabularies of the query/subscribe request
// bodies, derived from the api request structs so the strict
// unknown-field gate, the swagger spec, and the error messages'
// accepted-field enumeration are one artifact and cannot drift.
// applyQueryParams' read set is api.QueryBodyParams (which also
// carries the accepted-but-ignored `projection` — docs/07-roadmap.md).
var (
	queryBodyFields      = jsonFieldNames(reflect.TypeFor[api.SpaceQueryObjectsRequest]())
	perObjectQueryFields = jsonFieldNames(reflect.TypeFor[api.SpaceQueryRequest]())
	spaceListQueryFields = jsonFieldNames(reflect.TypeFor[api.SpaceListQueryRequest]())
)

// checkFilter parses the body's filter at the request boundary, so a
// bad filter is a clean 400 tied to the request — never surfacing out
// of Snapshot, or worst case mid-Subscribe after the SSE stream
// committed. The parsed form is discarded; the SDK re-parses on
// Filter() (one extra parse of caller-supplied input, and the builder
// chain stays 1:1). Same (errResp, done) convention as
// checkUnknownFields.
func checkFilter(c echo.Context, root *fastjson.Value) (error, bool) {
	if root == nil {
		return nil, false
	}
	filter := root.Get("filter")
	if filter == nil || filter.Type() == fastjson.TypeNull {
		return nil, false
	}
	if _, err := query.ParseCondition(filter); err != nil {
		var pe *query.ParseError
		if errors.As(err, &pe) {
			return filterParseError(c, pe, nil), true
		}
		return writeError(c, http.StatusBadRequest, "filter.invalid",
			"invalid filter: "+err.Error(), nil), true
	}
	return nil, false
}

// applyQueryParams reads filter / sort / limit / offset / includeTotal
// / mailboxCapacity / driftBudgetPercent off root and threads them
// into the chained query builder. `projection` is accepted but
// ignored — see docs/07-roadmap.md. MailboxCapacity /
// DriftBudgetPercent only matter on the Subscribe terminal; Snapshot
// ignores them.
func applyQueryParams(root *fastjson.Value, q space.Query) (space.Query, space.QueryOpts) {
	opts := space.QueryOpts{}
	if root == nil {
		return q, opts
	}
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
	if v := root.Get("includeTotal"); v != nil && v.Type() == fastjson.TypeTrue {
		opts.IncludeTotal = true
	}
	if v := root.Get("mailboxCapacity"); v != nil {
		opts.MailboxCapacity = v.GetInt()
	}
	if v := root.Get("driftBudgetPercent"); v != nil {
		opts.DriftBudgetPercent = v.GetInt()
	}
	return q, opts
}

// writeQueryResponse renders a *space.QueryResult into the HTTP wire
// shape. includeTotal mirrors the body flag — when false, Total is
// nil-pointer and omitted from the JSON; when true, the SDK populates
// res.Total (-1 only if it failed to count, which currently never
// happens — we surface the SDK's value verbatim). strip lists top-level
// record fields withheld from the wire (key material on tech-space
// rows — see spaceListStrippedFields).
func writeQueryResponse(c echo.Context, res *space.QueryResult, includeTotal bool, strip ...string) error {
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	records := make([]json.RawMessage, 0, len(res.Initial))
	for _, doc := range res.Initial {
		if doc == nil {
			records = append(records, json.RawMessage("null"))
			continue
		}
		v := doc.FastJson(fa)
		for _, key := range strip {
			v.Del(key)
		}
		records = append(records, json.RawMessage(v.MarshalTo(nil)))
	}
	out := api.QueryResponse{Records: records}
	if includeTotal {
		t := res.Total
		out.Total = &t
		hm := res.HasNext
		out.HasNext = &hm
	}
	return c.JSON(http.StatusOK, out)
}
