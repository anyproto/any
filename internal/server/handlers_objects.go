package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// objectCreate handles POST /v1/spaces/:spaceId/objects. Parses the
// body with a pooled fastjson.Parser and passes property values
// through as *fastjson.Value — anyenc converts them in one walk via
// Arena.NewFromFastJson, no Go-native intermediate.
//
// Body shape:
//
//	{
//	  "types": ["typeId", ...],
//	  "initialProperties": {
//	    "<typeId>": { "<propId>": <value>, ... }
//	  }
//	}
func (d *deps) objectCreate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	body, err := readBody(c)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "read body: "+err.Error(), nil)
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

	opts := space.CreateObjectOpts{}
	if root != nil {
		if types := root.GetArray("types"); len(types) > 0 {
			opts.Types = make([]string, 0, len(types))
			for _, v := range types {
				opts.Types = append(opts.Types, string(v.GetStringBytes()))
			}
		}
		if ip := root.GetObject("initialProperties"); ip != nil {
			opts.InitialProperties = map[string]map[string]any{}
			ip.Visit(func(typeKey []byte, props *fastjson.Value) {
				obj, err := props.Object()
				if err != nil {
					return
				}
				kv := map[string]any{}
				obj.Visit(func(propKey []byte, val *fastjson.Value) {
					kv[string(propKey)] = val
				})
				opts.InitialProperties[string(typeKey)] = kv
			})
		}
	}

	objectId, err := sp.Objects().Create(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusCreated, api.ObjectsCreateResponse{ObjectId: objectId})
}

// objectDelete handles DELETE /v1/spaces/:spaceId/objects/:objectId.
// SDK errors (including "already deleted" on a second call) map to
// 404 sdk.not_found per docs/06-errors.md.
func (d *deps) objectDelete(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	if err := sp.Objects().Delete(c.Request().Context(), objectId); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
		}
		return writeError(c, http.StatusNotFound, "sdk.not_found",
			"object not found or already deleted",
			map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	return c.NoContent(http.StatusNoContent)
}

// objectDerive handles POST /v1/spaces/:spaceId/objects/derive. Body:
//
//	{ "seed": "<base64>", "types": [...] }
//
// `seed` is decoded by encoding/json as a []byte (base64-standard).
func (d *deps) objectDerive(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	var req struct {
		Seed  []byte   `json:"seed"`
		Types []string `json:"types"`
	}
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}

	objectId, err := sp.Objects().Derive(c.Request().Context(), space.DeriveObjectOpts{
		Seed:  req.Seed,
		Types: req.Types,
	})
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusCreated, api.ObjectsDeriveResponse{ObjectId: objectId})
}
