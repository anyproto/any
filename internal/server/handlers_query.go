package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-store/v2/anyenc"
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
	q, opts, shaper, errResp, done := buildSharedQuery(c, sp)
	if done {
		return errResp
	}
	res, err := q.Snapshot(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return writeQueryResponse(c, res, opts.IncludeTotal, shaper)
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
	pq, errResp, done := buildPerObjectQuery(c, sp, d.techIndexVet(c, sp))
	if done {
		return errResp
	}
	details := map[string]any{"spaceId": sp.Id(), "objectId": pq.objectId, "dataset": pq.dataset}
	if pq.includeDeleted {
		return d.spaceQueryWithDeleted(c, pq, details)
	}
	res, err := pq.q.Snapshot(c.Request().Context(), pq.opts)
	if err != nil {
		return sdkOpError(c, err, details)
	}
	return writeQueryResponse(c, res, pq.opts.IncludeTotal, pq.shaper)
}

// spaceQueryWithDeleted answers a per-object snapshot that includes
// the dataset's record-level tombstones. The SDK's Snapshot pins the
// live view — it ANDs a `_deletedAt missing` clause into every filter,
// whatever ProjectionOpts say — so the tombstone-inclusive read goes
// through the find path (Iter / Count honour IncludeDeleted), which
// shares the filter / sort / limit / offset build with Snapshot. The
// reply keeps the snapshot shape; `total` here is the full match
// count, not the page-bounded one Snapshot reports.
func (d *deps) spaceQueryWithDeleted(c echo.Context, pq perObjectQuery, details map[string]any) error {
	ctx := c.Request().Context()
	q := pq.q.Projection(space.ProjectionOpts{IncludeDeleted: true})
	it, err := q.Iter(ctx)
	if err != nil {
		return sdkOpError(c, err, details)
	}
	defer it.Close()
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	records := make([]json.RawMessage, 0, 16)
	for it.Next() {
		doc, err := it.Doc()
		if err != nil {
			return sdkOpError(c, err, details)
		}
		// Doc is valid only until the next Next — render it now.
		records = append(records, renderQueryRecord(fa, doc, pq.shaper))
	}
	if err := it.Err(); err != nil {
		return sdkOpError(c, err, details)
	}
	out := api.QueryResponse{Records: records}
	if pq.opts.IncludeTotal {
		total, err := q.Count(ctx)
		if err != nil {
			return sdkOpError(c, err, details)
		}
		out.Total = &total
		hasNext := pq.offset+len(records) < total
		out.HasNext = &hasNext
	}
	return c.JSON(http.StatusOK, out)
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
func buildBodyQuery(c echo.Context, fields []string, base func(root *fastjson.Value) (space.Query, error, bool)) (space.Query, space.QueryOpts, recordShaper, error, bool) {
	var none recordShaper
	body, err := readBody(c)
	if err != nil {
		return nil, space.QueryOpts{}, none, writeError(c, http.StatusBadRequest, "request.bad_json", "unreadable body", nil), true
	}
	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	var root *fastjson.Value
	if len(body) > 0 {
		root, err = parser.ParseBytes(body)
		if err != nil {
			return nil, space.QueryOpts{}, none, writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil), true
		}
	}
	if errResp, done := checkUnknownFields(c, root, "", fields...); done {
		return nil, space.QueryOpts{}, none, errResp, true
	}
	q, errResp, done := base(root)
	if done {
		return nil, space.QueryOpts{}, none, errResp, true
	}
	if errResp, done := checkFilter(c, root); done {
		return nil, space.QueryOpts{}, none, errResp, true
	}
	if errResp, done := checkSubscribeWindow(c, root); done {
		return nil, space.QueryOpts{}, none, errResp, true
	}
	proj, errResp, done := parseProjection(c, root, false)
	if done {
		return nil, space.QueryOpts{}, none, errResp, true
	}
	q, opts := applyQueryParams(root, q)
	return q, opts, recordShaper{proj: proj}, nil, false
}

// checkSubscribeWindow rejects a windowed subscribe that sets `limit`
// without `sort`. A live window has to be ordered for the SDK to know
// which records fall inside it; its precondition lives in an SDK
// internal package, so an unguarded violation reaches sdkOpError
// unclassified and answers 500 — a client body mistake reading as a
// server fault. Keyed off the registered route so every windowed
// subscribe is covered, including ones added later. Snapshot queries
// are unaffected: an unordered limit there is just an arbitrary page.
func checkSubscribeWindow(c echo.Context, root *fastjson.Value) (error, bool) {
	if root == nil || !strings.HasSuffix(c.Path(), "/subscribe") {
		return nil, false
	}
	if v := root.Get("limit"); v == nil || v.GetInt() <= 0 {
		return nil, false
	}
	if len(root.GetArray("sort")) > 0 {
		return nil, false
	}
	return writeError(c, http.StatusBadRequest, "request.invalid_field",
		`"limit" on a subscribe requires "sort" — a live window has to be ordered`,
		map[string]any{"field": "limit"}), true
}

// buildSharedQuery parses the request body for the QueryObjects (per-
// space `objects` collection) endpoints and assembles the chained
// Query plus its QueryOpts. Returns (q, opts, errResp, done=true) on
// validation failure; the caller returns errResp directly in that
// case. Shared between the snapshot and subscribe handlers so the body
// shape stays in lockstep.
func buildSharedQuery(c echo.Context, sp space.Space) (space.Query, space.QueryOpts, recordShaper, error, bool) {
	return buildBodyQuery(c, queryBodyFields, func(*fastjson.Value) (space.Query, error, bool) {
		return sp.QueryObjects(), nil, false
	})
}

// perObjectVet lets the caller inspect the parsed body once objectId
// and dataset are known — the tech index fence. nil skips it; a
// non-nil strip is applied to the response rows.
type perObjectVet func(root *fastjson.Value, objectId, dataset string) (strip []string, errResp error, done bool)

// perObjectQuery is the parsed form of a per-object query body: the
// windowed builder plus everything the handler needs after the SDK
// call (addressing for error details, the tech-index strip list, and
// the two body knobs the SDK's QueryOpts do not carry).
type perObjectQuery struct {
	q        space.Query
	opts     space.QueryOpts
	objectId string
	dataset  string
	// shaper carries the caller's projection and the tech-index
	// blocklist; every record on this path renders through it.
	shaper recordShaper
	// offset mirrors the body's offset — the find path computes
	// hasNext itself, Snapshot does it inside the SDK.
	offset int
	// includeDeleted routes the snapshot through the tombstone-
	// inclusive find path; refused on subscribe.
	includeDeleted bool
}

// buildPerObjectQuery is the per-object dataset counterpart to
// buildSharedQuery. objectId and dataset are required body fields; a
// missing or empty value short-circuits with 400 request.missing_field.
func buildPerObjectQuery(c echo.Context, sp space.Space, vet perObjectVet) (perObjectQuery, error, bool) {
	var none perObjectQuery
	body, err := readBody(c)
	if err != nil || len(body) == 0 {
		return none, writeError(c, http.StatusBadRequest, "request.bad_json", "missing or unreadable body", nil), true
	}
	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	root, err := parser.ParseBytes(body)
	if err != nil {
		return none, writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil), true
	}
	if errResp, done := checkUnknownFields(c, root, "", perObjectQueryFields...); done {
		return none, errResp, true
	}
	objectId := string(root.GetStringBytes("objectId"))
	dataset := string(root.GetStringBytes("dataset"))
	if objectId == "" {
		return none, writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil), true
	}
	if isSerializedNil(objectId) {
		return none, serializedNilIdError(c, "objectId", objectId), true
	}
	if dataset == "" {
		return none, writeError(c, http.StatusBadRequest, "request.missing_field", "dataset required", nil), true
	}
	if errResp, done := checkFilter(c, root); done {
		return none, errResp, true
	}
	if errResp, done := checkSubscribeWindow(c, root); done {
		return none, errResp, true
	}
	var strip []string
	if vet != nil {
		var errResp error
		var done bool
		strip, errResp, done = vet(root, objectId, dataset)
		if done {
			return none, errResp, true
		}
	}
	proj, errResp, done := parseProjection(c, root, false)
	if done {
		return none, errResp, true
	}
	q, opts := applyQueryParams(root, sp.Query(objectId, dataset))
	pq := perObjectQuery{
		q: q, opts: opts, objectId: objectId, dataset: dataset,
		shaper: recordShaper{proj: proj, strip: strip},
	}
	if v := root.Get("offset"); v != nil && v.GetInt() > 0 {
		pq.offset = v.GetInt()
	}
	if v := root.Get("includeDeleted"); v != nil && v.Type() == fastjson.TypeTrue {
		pq.includeDeleted = true
	}
	return pq, nil, false
}

// The closed top-level vocabularies of the query/subscribe request
// bodies, derived from the api request structs so the strict
// unknown-field gate, the swagger spec, and the error messages'
// accepted-field enumeration are one artifact and cannot drift.
// applyQueryParams' read set is api.QueryBodyParams; `projection` is
// read separately by parseProjection.
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
// into the chained query builder. `projection` is read by
// parseProjection, not here. MailboxCapacity /
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
func writeQueryResponse(c echo.Context, res *space.QueryResult, includeTotal bool, shaper recordShaper) error {
	out := api.QueryResponse{Records: shapeRecords(res.Initial, shaper)}
	if includeTotal {
		t := res.Total
		out.Total = &t
		hm := res.HasNext
		out.HasNext = &hm
	}
	return c.JSON(http.StatusOK, out)
}

// shapeRecords renders a materialised window onto the wire — the one
// implementation shared by the HTTP reply and the SSE snapshot frame,
// so the two cannot drift apart when the shaping rules change.
func shapeRecords(docs []*anyenc.Value, shaper recordShaper) []json.RawMessage {
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	records := make([]json.RawMessage, 0, len(docs))
	for _, doc := range docs {
		records = append(records, renderQueryRecord(fa, doc, shaper))
	}
	return records
}

// renderQueryRecord renders one stored record for the wire through the
// shaper (the caller's projection, then the tech-index withheld
// fields). A nil doc renders as JSON null.
func renderQueryRecord(fa *fastjson.Arena, doc *anyenc.Value, shaper recordShaper) json.RawMessage {
	if doc == nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(shaper.record(doc, fa).MarshalTo(nil))
}
