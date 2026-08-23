package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// spaceAggregateObjects handles POST /v1/spaces/:spaceId/objects/aggregate.
//
//	@Summary	Aggregate over the objects in a space
//	@Tags		objects
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string								true	"Space ID"
//	@Param		body	body		api.SpaceAggregateObjectsRequest	true	"Aggregation params"
//	@Success	200		{object}	api.AggregateResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/aggregate [post]
func (d *deps) spaceAggregateObjects(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	agg, explain, errResp, done := buildSharedAgg(c, sp)
	if done {
		return errResp
	}
	return runAggregate(c, agg, explain, map[string]any{"spaceId": sp.Id()})
}

// spaceAggregate handles POST /v1/spaces/:spaceId/aggregate.
//
//	@Summary	Aggregate over a per-object dataset
//	@Tags		data
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string						true	"Space ID"
//	@Param		body	body		api.SpaceAggregateRequest	true	"Aggregation params (objectId+dataset required)"
//	@Success	200		{object}	api.AggregateResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/aggregate [post]
func (d *deps) spaceAggregate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	agg, explain, objectId, dataset, errResp, done := buildPerObjectAgg(c, sp)
	if done {
		return errResp
	}
	// Aggregation output is caller-shaped, so withheld fields cannot
	// be stripped from it: a dataset with a strip list (spaces) is
	// query-only on the index object.
	if d.isTechSpace(sp.Id()) && objectId == sp.SpaceIndexObjectId() {
		if stripped, ok := techIndexDatasetPolicy[dataset]; !ok || len(stripped) > 0 {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"aggregate on the tech index object is limited to profile and bundles",
				map[string]any{"objectId": objectId, "dataset": dataset})
		}
	}
	return runAggregate(c, agg, explain, map[string]any{
		"spaceId": sp.Id(), "objectId": objectId, "dataset": dataset,
	})
}

// buildSharedAgg parses the request body for the AggregateObjects
// (per-space `objects` collection) endpoint and assembles the chained
// Agg. Same (value, errResp, done) convention as buildSharedQuery.
func buildSharedAgg(c echo.Context, sp space.Space) (space.Agg, bool, error, bool) {
	root, errResp, done := parseAggBody(c)
	if done {
		return nil, false, errResp, true
	}
	pipeline, errResp, done := requirePipeline(c, root)
	if done {
		return nil, false, errResp, true
	}
	agg, explain := applyAggParams(root, sp.AggregateObjects(pipeline))
	return agg, explain, nil, false
}

// buildPerObjectAgg is the per-object dataset counterpart to
// buildSharedAgg. objectId and dataset are required body fields.
func buildPerObjectAgg(c echo.Context, sp space.Space) (space.Agg, bool, string, string, error, bool) {
	root, errResp, done := parseAggBody(c)
	if done {
		return nil, false, "", "", errResp, true
	}
	objectId := string(root.GetStringBytes("objectId"))
	dataset := string(root.GetStringBytes("dataset"))
	if objectId == "" {
		return nil, false, "", "", writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil), true
	}
	if dataset == "" {
		return nil, false, "", "", writeError(c, http.StatusBadRequest, "request.missing_field", "dataset required", nil), true
	}
	pipeline, errResp, done := requirePipeline(c, root)
	if done {
		return nil, false, "", "", errResp, true
	}
	agg, explain := applyAggParams(root, sp.Aggregate(objectId, dataset, pipeline))
	return agg, explain, objectId, dataset, nil, false
}

// parseAggBody reads and parses the JSON body. Unlike query, an empty
// body is never valid here — the pipeline field is required.
func parseAggBody(c echo.Context) (*fastjson.Value, error, bool) {
	body, err := readBody(c)
	if err != nil || len(body) == 0 {
		return nil, writeError(c, http.StatusBadRequest, "request.bad_json", "missing or unreadable body", nil), true
	}
	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	root, err := parser.ParseBytes(body)
	if err != nil {
		return nil, writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil), true
	}
	return root, nil, false
}

// requirePipeline extracts the mandatory pipeline field. It must be a
// JSON array of stage objects; the stages themselves are validated by
// the SDK/any-store parser (surfacing as aggregate.bad_pipeline).
func requirePipeline(c echo.Context, root *fastjson.Value) (*fastjson.Value, error, bool) {
	pipeline := root.Get("pipeline")
	if pipeline == nil || pipeline.Type() == fastjson.TypeNull {
		return nil, writeError(c, http.StatusBadRequest, "request.missing_field", "pipeline required", nil), true
	}
	if pipeline.Type() != fastjson.TypeArray {
		return nil, writeError(c, http.StatusBadRequest, "request.schema", "pipeline must be an array of stages", nil), true
	}
	return pipeline, nil, false
}

// applyAggParams threads the optional limit overrides and the explain
// flag off root into the chained Agg builder. Limits are applied only
// when present in the body, so the SDK/any-store defaults hold
// otherwise; a negative value means unlimited and passes through
// verbatim (trusted localhost caller).
func applyAggParams(root *fastjson.Value, agg space.Agg) (space.Agg, bool) {
	if v := root.Get("groupLimit"); v != nil {
		agg = agg.GroupLimit(v.GetInt())
	}
	if v := root.Get("accumArrayLimit"); v != nil {
		agg = agg.AccumArrayLimit(v.GetInt())
	}
	if v := root.Get("memoryLimitBytes"); v != nil {
		agg = agg.MemoryLimit(v.GetInt())
	}
	return agg, root.GetBool("explain")
}

// runAggregate executes the assembled Agg — Explain when the request
// asked for it, All otherwise — and renders the response.
func runAggregate(c echo.Context, agg space.Agg, explain bool, details map[string]any) error {
	ctx := c.Request().Context()
	if explain {
		plan, err := agg.Explain(ctx)
		if err != nil {
			return aggOpError(c, err, details)
		}
		return c.JSON(http.StatusOK, api.AggregateResponse{Plan: &plan})
	}
	rows, err := agg.All(ctx)
	if err != nil {
		return aggOpError(c, err, details)
	}
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	records := make([]json.RawMessage, 0, len(rows))
	for _, doc := range rows {
		if doc == nil {
			records = append(records, json.RawMessage("null"))
			continue
		}
		records = append(records, json.RawMessage(doc.FastJson(fa).MarshalTo(nil)))
	}
	return c.JSON(http.StatusOK, api.AggregateResponse{Records: records})
}

// aggOpError maps aggregation errors onto the canonical envelope. A
// malformed pipeline (parse/stage/prefix violations, wrapped by the
// SDK as ErrBadPipeline) and a blown blocking-stage limit are caller
// errors → 400; anything else falls through to sdkOpError. Limit
// errors surface mid-iteration from All, not from the builder.
func aggOpError(c echo.Context, err error, details map[string]any) error {
	if errors.Is(err, space.ErrBadPipeline) {
		return writeError(c, http.StatusBadRequest, "aggregate.bad_pipeline", err.Error(), details)
	}
	var limit string
	switch {
	case errors.Is(err, space.ErrAggGroupLimitExceeded):
		limit = "group"
	case errors.Is(err, space.ErrAggAccumArrayLimitExceeded):
		limit = "accumArray"
	case errors.Is(err, space.ErrAggMemoryLimitExceeded):
		limit = "memory"
	default:
		return sdkOpError(c, err, details)
	}
	d := map[string]any{"limit": limit}
	for k, v := range details {
		d[k] = v
	}
	return writeError(c, http.StatusBadRequest, "aggregate.limit_exceeded", err.Error(), d)
}
