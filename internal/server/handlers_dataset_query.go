package server

import (
	"net/http"
	"reflect"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// datasetQueryFields and datasetAggFields are the closed top-level
// vocabularies of the dataset-scope bodies, derived from the api
// structs like the other query surfaces. The aggregate body is closed
// too: a stray `objectId` would otherwise read as a pipeline over one
// object and run over every object's records.
var (
	datasetQueryFields = jsonFieldNames(reflect.TypeFor[api.SpaceDatasetQueryRequest]())
	datasetAggFields   = jsonFieldNames(reflect.TypeFor[api.SpaceDatasetAggregateRequest]())
)

// spaceQueryDataset handles POST /v1/spaces/:spaceId/datasets/query.
//
//	@Summary	Query a shared dataset across objects
//	@Tags		data
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string							true	"Space ID"
//	@Param		body	body		api.SpaceDatasetQueryRequest	true	"Query params (dataset required)"
//	@Success	200		{object}	api.QueryResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/datasets/query [post]
func (d *deps) spaceQueryDataset(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	q, opts, shaper, dataset, errResp, done := buildDatasetQuery(c, sp)
	if done {
		return errResp
	}
	res, err := q.Snapshot(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "dataset": dataset})
	}
	return writeQueryResponse(c, res, opts.IncludeTotal, shaper)
}

// spaceQueryDatasetSubscribe handles POST /v1/spaces/:spaceId/datasets/query/subscribe.
//
// Windowed live view over a shared dataset across every object that
// holds it. Same wire as /query/subscribe; the body names the dataset
// and no object.
//
//	@Summary	Subscribe to a windowed query over a shared dataset (SSE)
//	@Tags		data
//	@Accept		json
//	@Produce	text/event-stream
//	@Param		spaceId	path	string							true	"Space ID"
//	@Param		body	body	api.SpaceDatasetQueryRequest	true	"Query params (dataset required)"
//	@Success	200
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/datasets/query/subscribe [post]
func (d *deps) spaceQueryDatasetSubscribe(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	q, opts, shaper, dataset, errResp, done := buildDatasetQuery(c, sp)
	if done {
		return errResp
	}
	res, err := q.Subscribe(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "dataset": dataset})
	}
	return d.streamQuerySubscribe(c, res, opts.IncludeTotal, shaper)
}

// spaceAggregateDataset handles POST /v1/spaces/:spaceId/datasets/aggregate.
//
//	@Summary	Aggregate over a shared dataset across objects
//	@Tags		data
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string								true	"Space ID"
//	@Param		body	body		api.SpaceDatasetAggregateRequest	true	"Aggregation params (dataset required)"
//	@Success	200		{object}	api.AggregateResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/datasets/aggregate [post]
func (d *deps) spaceAggregateDataset(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	root, errResp, done := parseAggBody(c)
	if done {
		return errResp
	}
	if errResp, done := checkUnknownFields(c, root, "", datasetAggFields...); done {
		return errResp
	}
	dataset, errResp, done := requireSharedDataset(c, root)
	if done {
		return errResp
	}
	pipeline, errResp, done := requirePipeline(c, root)
	if done {
		return errResp
	}
	agg, explain := applyAggParams(root, sp.AggregateDataset(dataset, pipeline))
	return runAggregate(c, agg, explain, map[string]any{"spaceId": sp.Id(), "dataset": dataset})
}

// buildDatasetQuery is the dataset-scope counterpart to
// buildPerObjectQuery: the body names a shared dataset and no object.
func buildDatasetQuery(c echo.Context, sp space.Space) (space.Query, space.QueryOpts, recordShaper, string, error, bool) {
	var dataset string
	q, opts, shaper, errResp, done := buildBodyQuery(c, datasetQueryFields, func(root *fastjson.Value) (space.Query, error, bool) {
		var errResp error
		var done bool
		if dataset, errResp, done = requireSharedDataset(c, root); done {
			return nil, errResp, true
		}
		return sp.QueryDataset(dataset), nil, false
	})
	return q, opts, shaper, dataset, errResp, done
}

// requireSharedDataset reads the mandatory `dataset` of a dataset-scope
// read and applies the key-exchange refusal every read naming a
// dataset carries. Whether the dataset is shared is the SDK's answer
// (400 dataset.not_shared).
func requireSharedDataset(c echo.Context, root *fastjson.Value) (string, error, bool) {
	var dataset string
	if root != nil {
		dataset = string(root.GetStringBytes("dataset"))
	}
	if dataset == "" {
		return "", writeError(c, http.StatusBadRequest, "request.missing_field", "dataset required", nil), true
	}
	if errResp, done := identityKeysReadRefused(c, "", dataset); done {
		return "", errResp, true
	}
	return dataset, nil, false
}
