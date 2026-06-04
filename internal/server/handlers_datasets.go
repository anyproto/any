package server

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// spaceDatasets handles GET /v1/spaces/:spaceId/datasets.
//
// Returns the JSON-Schema description of every dataset the space hosts —
// the built-in `objects` namespace plus every registered handler dataset
// (chat_messages, editor_blocks, …) — with per-field x-scope (synced /
// derived / local). Pure discovery; mirrors Space.Datasets().
//
//	@Summary	List a space's dataset schemas
//	@Tags		data
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	200		{object}	api.DatasetsResponse
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/datasets [get]
func (d *deps) spaceDatasets(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	return c.JSON(http.StatusOK, datasetsToAPI(sp.Datasets()))
}

// systemDatasets handles GET /v1/datasets.
//
// Returns the JSON-Schema description of the account's tech-space system
// datasets (spaces, profile) — the schemas behind the generic space-list
// query/subscribe (POST /v1/spaces/query[/subscribe]). Account-scoped, so
// it sits outside the per-space group. Mirrors Service.Datasets().
//
//	@Summary	List the account's system dataset schemas
//	@Tags		data
//	@Produce	json
//	@Success	200	{object}	api.DatasetsResponse
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/datasets [get]
func (d *deps) systemDatasets(c echo.Context) error {
	return c.JSON(http.StatusOK, datasetsToAPI(d.sdk.Spaces().Datasets()))
}

// datasetsToAPI renders the SDK's []space.DatasetSchema onto the wire
// shape. JSONSchema is already a marshaled JSON Schema document; carry
// it through verbatim as RawMessage.
func datasetsToAPI(in []space.DatasetSchema) api.DatasetsResponse {
	out := make([]api.DatasetSchema, 0, len(in))
	for _, ds := range in {
		out = append(out, api.DatasetSchema{Name: ds.Name, Schema: json.RawMessage(ds.JSONSchema)})
	}
	return api.DatasetsResponse{Datasets: out}
}
