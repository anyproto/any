package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/nav"
)

// objectCreate handles POST /v1/spaces/:spaceId/objects. Parses the
// body with a pooled fastjson.Parser and passes property values
// through as *fastjson.Value — anyenc converts them in one walk via
// Arena.NewFromFastJson, no Go-native intermediate.
//
// Every new object also gets a default `nav` row stamped on it: nav
// is auto-appended to types, and nav.{type, parentId, pos} land in
// initialProperties unless the caller supplied them. The folder/item
// flag defaults to item; parent defaults to root (""); pos is a
// jittered fresh lexid so concurrent root-creates don't collide. See
// internal/nav.
//
// Body shape:
//
//	{
//	  "types": ["typeId", ...],
//	  "initialProperties": {
//	    "<typeId>": { "<propId>": <value>, ... }
//	  },
//	  "nav": {                  // optional; per-field overrides
//	    "type":     1|2,
//	    "parentId": "<obj_id>",
//	    "pos":      "<lexid>"
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

	if errResp := injectNavDefaults(c, sp, root, &opts); errResp != nil {
		return errResp
	}

	objectId, err := sp.Objects().Create(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusCreated, api.ObjectsCreateResponse{ObjectId: objectId})
}

// injectNavDefaults seeds the virtual `nav` type onto a creation. Adds
// nav to opts.Types when missing and writes default nav.{type,
// parentId, pos} into opts.InitialProperties — but only fields the
// caller did not already supply, so explicit values from the body win.
//
// pos defaults to the next lexid after the current max pos in the
// target folder (Middle() when the folder is empty), mirroring the
// anytype-heart pattern: Middle on empty, Next(prev) on append. The
// folder lookup runs before the create — the new row lands one step
// past the rightmost sibling. Returns a non-nil echo response on
// caller-input error (e.g. invalid nav.type).
func injectNavDefaults(c echo.Context, sp space.Space, root *fastjson.Value, opts *space.CreateObjectOpts) error {
	if !slices.Contains(opts.Types, nav.TypeId) {
		opts.Types = append(opts.Types, nav.TypeId)
	}

	if opts.InitialProperties == nil {
		opts.InitialProperties = map[string]map[string]any{}
	}
	navProps := opts.InitialProperties[nav.TypeId]
	if navProps == nil {
		navProps = map[string]any{}
	}

	var override *fastjson.Value
	if root != nil {
		override = root.Get("nav")
	}

	// nav.type — default 1 (item). Validate caller value if present.
	if _, set := navProps[nav.PropType]; !set {
		t := nav.TypeItem
		if override != nil {
			if v := override.Get("type"); v != nil && v.Type() == fastjson.TypeNumber {
				t = v.GetInt()
			}
		}
		if t != nav.TypeItem && t != nav.TypeFolder {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"nav.type must be 1 (item) or 2 (folder)",
				map[string]any{"got": t})
		}
		navProps[nav.PropType] = t
	}

	// nav.parentId — default "" (root). Caller-supplied values can
	// arrive either as a Go string (in-process) or *fastjson.Value
	// (from the request body via handler parsing); both shapes need
	// to land in `parentId` so the per-folder max-pos lookup queries
	// the right folder.
	parentId := nav.RootParentId
	if existing, set := navProps[nav.PropParentId]; set {
		switch x := existing.(type) {
		case string:
			parentId = x
		case *fastjson.Value:
			if x != nil && x.Type() == fastjson.TypeString {
				parentId = string(x.GetStringBytes())
			}
		}
	} else {
		if override != nil {
			if v := override.Get("parentId"); v != nil && v.Type() == fastjson.TypeString {
				parentId = string(v.GetStringBytes())
			}
		}
		navProps[nav.PropParentId] = parentId
	}

	// nav.pos — caller wins; otherwise derive from the folder's
	// current max. Cheap one-row query: filter by parentId, sort
	// descending, limit 1.
	if _, set := navProps[nav.PropPos]; !set {
		var pos string
		if override != nil {
			if v := override.Get("pos"); v != nil && v.Type() == fastjson.TypeString {
				pos = string(v.GetStringBytes())
			}
		}
		if pos == "" {
			lastPos, err := lookupMaxNavPos(c.Request().Context(), sp, parentId)
			if err != nil {
				return writeError(c, http.StatusInternalServerError, "internal",
					"nav.pos lookup: "+err.Error(),
					map[string]any{"spaceId": sp.Id(), "parentId": parentId})
			}
			pos = nav.NextPos(lastPos)
		}
		navProps[nav.PropPos] = pos
	}

	opts.InitialProperties[nav.TypeId] = navProps
	return nil
}

// lookupMaxNavPos returns the highest nav.pos string among objects in
// the given folder, or "" when the folder is empty. Errors propagate
// to the caller — typically transient SDK / store conditions, treated
// as a 500 by injectNavDefaults rather than silently picking an
// arbitrary pos.
func lookupMaxNavPos(ctx context.Context, sp space.Space, parentId string) (string, error) {
	doc, err := sp.QueryObjects().
		Filter(map[string]any{"nav.parentId": parentId}).
		Sort("-nav.pos").
		Limit(1).
		One(ctx)
	if err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("query siblings of %q: %w", parentId, err)
	}
	if doc == nil {
		return "", nil
	}
	posVal := doc.Get("nav", "pos")
	if posVal == nil {
		return "", nil
	}
	return string(posVal.GetStringBytes()), nil
}

// objectDelete handles DELETE /v1/spaces/:spaceId/objects/:objectId.
// SDK errors (including "already deleted" on a second call) map to
// 404 sdk.not_found per docs/06-errors.md.
//
// Two-step delete: first tombstone the row in the per-space
// `objects` collection (so queries stop returning it — the SDK's
// query iterator skips rows with _deletedAt), then drop the
// any-sync tree. Order matters: once the tree is gone the per-
// object Modify path can't write the tombstone, so the row would
// linger in queries forever. See docs/03-api.md § "Object
// deletion".
func (d *deps) objectDelete(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	objectId := c.Param("objectId")
	if objectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}

	// Tombstone the per-space row first. Errors here are not fatal —
	// the row may already be tombstoned (idempotent re-delete) or the
	// system handler may reject; we continue to the tree delete either
	// way and log the situation.
	if _, err := sp.Delete(c.Request().Context(), space.DeleteBatch{
		ObjectId:  objectId,
		Dataset:   "objects",
		RecordIds: []string{objectId},
	}); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
		}
		// Soft-fail: a missing or already-tombstoned row shouldn't
		// stop the tree delete. Anything else surfaces in the log.
		c.Logger().Warnf("objectDelete: tombstone row %s: %v", objectId, err)
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
