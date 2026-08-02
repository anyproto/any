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

// objectCreateFieldsHint rides every unknown-field rejection on object
// create, naming the right home for the most commonly misplaced keys
// (top-level name / description / a bare type-key group like "any").
const objectCreateFieldsHint = `object properties (name, description, custom fields) go under initialProperties keyed by type, e.g. {"initialProperties": {"any": {"name": "Dune"}}}`

// objectCreate handles POST /v1/spaces/:spaceId/objects.
//
//	@Summary	Create an object
//	@Tags		objects
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string					true	"Space ID"
//	@Param		body	body		api.ObjectCreateRequest	true	"Object params"
//	@Success	201		{object}	api.ObjectsCreateResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects [post]
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

	if errResp, done := checkUnknownFields(c, root, objectCreateFieldsHint, "types", "initialProperties", "nav"); done {
		return errResp
	}

	opts := space.CreateObjectOpts{}
	if root != nil {
		// The three accepted fields are shape-checked before positive
		// extraction: a `types` string or an `initialProperties` array
		// would otherwise be skipped unread — the same silent-drop trap
		// checkUnknownFields closes for misspelled keys.
		if v := root.Get("types"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeArray {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"types must be an array of type ids", nil)
		}
		if v := root.Get("nav"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeObject {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"nav must be an object with optional type / parentId / pos", nil)
		}
		if v := root.Get("initialProperties"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeObject {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"initialProperties must be an object keyed by type id, e.g. {\"any\": {\"name\": \"…\"}}", nil)
		}
		if types := root.GetArray("types"); len(types) > 0 {
			opts.Types = make([]string, 0, len(types))
			for _, v := range types {
				opts.Types = append(opts.Types, string(v.GetStringBytes()))
			}
		}
		if ip := root.GetObject("initialProperties"); ip != nil {
			opts.InitialProperties = map[string]map[string]any{}
			var badGroup string
			ip.Visit(func(typeKey []byte, props *fastjson.Value) {
				obj, err := props.Object()
				if err != nil {
					badGroup = string(typeKey)
					return
				}
				kv := map[string]any{}
				obj.Visit(func(propKey []byte, val *fastjson.Value) {
					kv[string(propKey)] = val
				})
				opts.InitialProperties[string(typeKey)] = kv
			})
			if badGroup != "" {
				return writeError(c, http.StatusBadRequest, "request.schema",
					fmt.Sprintf("initialProperties.%s must be an object of {propertyId: value}", badGroup),
					map[string]any{"typeKey": badGroup})
			}
		}
	}

	if errResp := injectNavDefaults(c, sp, root, &opts); errResp != nil {
		return errResp
	}

	// Same format value-shape gate as propertiesSet, per initial type
	// (see propformat.go). nav injects only its own numeric props and
	// nav declares no formats, so the extra lookups are user types only.
	for typeId, patch := range opts.InitialProperties {
		defs, err := sp.Types().Properties(c.Request().Context(), typeId)
		if err != nil {
			continue // unknown type: the SDK rejects the write itself
		}
		if v := validateFormatValues(defs, patch); v != nil {
			return writeError(c, http.StatusBadRequest, "property.format_violation",
				"initial property value does not match the property's declared format",
				v.details())
		}
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
//
//	@Summary	Delete an object
//	@Tags		objects
//	@Param		spaceId		path	string	true	"Space ID"
//	@Param		objectId	path	string	true	"Object ID"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId} [delete]
func (d *deps) objectDelete(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
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
