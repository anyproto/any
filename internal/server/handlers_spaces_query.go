package server

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"
)

// SpaceListDataset is the default tech-space system dataset the
// space-list query/subscribe endpoints read. The profile dataset is
// reachable via the optional `dataset` body field.
const SpaceListDataset = "spaces"

// spaceListAllowedDatasets is the allowlist of tech-space system
// datasets reachable through the generic space-list query/subscribe.
// It is deliberately closed: the tech-space index object also hosts the
// `identities` directory dataset, whose rows carry a SYNCED symKey (the
// contact's profile-decryption secret). The public Identities API
// (GET /v1/identities) strips that field; the raw query path would not,
// so `identities` must never be reachable here. Read the directory
// through GET /v1/identities instead.
var spaceListAllowedDatasets = map[string]struct{}{
	"spaces":  {},
	"profile": {},
}

// spaceListQuery handles POST /v1/spaces/query.
//
// The windowed-query counterpart to GET /v1/spaces: a snapshot over the
// account's tech-space `spaces` dataset through the generic
// Service.Query primitive (same Filter/Sort/Limit/offset/includeTotal
// surface as the per-object /query endpoints). Records are the raw
// tech-index rows — GET /v1/spaces remains the mapped convenience for
// the public SpaceInfo shape.
//
//	@Summary	Query the account's space list (windowed)
//	@Tags		spaces
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.SpaceQueryRequest	false	"Query params (objectId fixed; dataset defaults to spaces)"
//	@Success	200		{object}	api.QueryResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/query [post]
func (d *deps) spaceListQuery(c echo.Context) error {
	q, opts, dataset, errResp, done := d.buildSpaceListQuery(c)
	if done {
		return errResp
	}
	res, err := q.Snapshot(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"dataset": dataset})
	}
	return writeQueryResponse(c, res, opts.IncludeTotal)
}

// spaceListQuerySubscribe handles POST /v1/spaces/query/subscribe.
//
// Windowed live view over the account's space list — the SSE
// counterpart to spaceListQuery. Same body; same frame set as the
// per-object /query/subscribe (ready → snapshot → changes → closed).
// Lets a client observe spaces appearing / changing / leaving (e.g.
// joined on another device, head-synced in) without polling
// GET /v1/spaces.
//
//	@Summary	Subscribe to the account's space list (SSE)
//	@Tags		spaces
//	@Accept		json
//	@Produce	text/event-stream
//	@Param		body	body	api.SpaceQueryRequest	false	"Query params (objectId fixed; dataset defaults to spaces)"
//	@Success	200
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/query/subscribe [post]
func (d *deps) spaceListQuerySubscribe(c echo.Context) error {
	q, opts, dataset, errResp, done := d.buildSpaceListQuery(c)
	if done {
		return errResp
	}
	res, err := q.Subscribe(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"dataset": dataset})
	}
	return d.streamQuerySubscribe(c, res, opts.IncludeTotal)
}

// buildSpaceListQuery assembles the chained Query + QueryOpts for the
// space-list endpoints. The body is optional (an empty body is a full
// snapshot); when present it carries the same filter/sort/limit/offset/
// includeTotal/mailboxCapacity/driftBudgetPercent fields as the
// per-object query. objectId is fixed to the tech-space index object;
// `dataset` is an optional override defaulting to `spaces`.
func (d *deps) buildSpaceListQuery(c echo.Context) (space.Query, space.QueryOpts, string, error, bool) {
	body, err := readBody(c)
	if err != nil {
		return nil, space.QueryOpts{}, "", writeError(c, http.StatusBadRequest, "request.bad_json", "unreadable body", nil), true
	}
	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	var root *fastjson.Value
	if len(body) > 0 {
		root, err = parser.ParseBytes(body)
		if err != nil {
			return nil, space.QueryOpts{}, "", writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil), true
		}
	}
	dataset := SpaceListDataset
	if root != nil {
		if ds := string(root.GetStringBytes("dataset")); ds != "" {
			dataset = ds
		}
	}
	if _, ok := spaceListAllowedDatasets[dataset]; !ok {
		return nil, space.QueryOpts{}, dataset, writeError(c, http.StatusBadRequest,
			"request.invalid_field",
			"dataset must be one of: spaces, profile (read identities via GET /v1/identities)",
			map[string]any{"dataset": dataset}), true
	}
	svc := d.sdk.Spaces()
	q, opts := applyQueryParams(root, svc.Query(svc.SpaceIndexObjectId(), dataset))
	return q, opts, dataset, nil, false
}
