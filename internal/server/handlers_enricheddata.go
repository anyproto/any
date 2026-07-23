package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/enricheddata"
)

// enrichedDataCreate handles
// POST /v1/spaces/:spaceId/objects/:objectId/enriched-data — write one sourced
// enrichment record onto the target object's enriched_data collection,
// attaching the enriched_data type to the object on first write.
//
//	@Summary	Write one sourced enrichment record onto an object
//	@Tags		enrich
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string							true	"Space ID"
//	@Param		objectId	path		string							true	"Target object ID"
//	@Param		body		body		api.EnrichedDataCreateRequest	true	"Enrichment record"
//	@Success	201			{object}	api.ModifyResult
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/enriched-data [post]
func (d *deps) enrichedDataCreate(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}

	req, ok := bindBody[api.EnrichedDataCreateRequest](c)
	if !ok {
		return nil
	}
	if req.Text == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "text required", nil)
	}
	if len(req.Text) > enricheddata.MaxTextBytes {
		return writeError(c, http.StatusBadRequest, api.ErrEnrichedDataTextTooLong, "text too long",
			map[string]any{"max_bytes": enricheddata.MaxTextBytes, "got_bytes": len(req.Text)})
	}

	res, err := enricheddata.Create(c.Request().Context(), sp, objectId, req.Text, req.Source, req.Target, req.Value)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	return c.JSON(http.StatusCreated, modifyResultToAPI(res))
}
