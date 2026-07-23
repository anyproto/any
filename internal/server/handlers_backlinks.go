package server

import (
	"context"
	"net/http"

	"github.com/anyproto/any-store/v2/query"
	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// Backlinks — "which objects reference X?" — the reverse direction of
// links-format property values. The SDK exposes no reverse index, so
// this is a consumer-side read built from what it does expose: object
// references are properties with format "links" (arrays of
// "any://<objectId>" URI strings, the in-space fragment-less form —
// see propformat.go), stored at record[typeId][propId] in the shared
// `objects` collection. Backlinks of X = rows whose links arrays
// contain "any://<X>".

// linkProp is one catalog row: a links-format property and the type
// that declares it.
type linkProp struct {
	typeId string
	propId string
}

// linkPropCatalog resolves the space's links-format properties by
// walking every user type's definitions. Built-in types are skipped —
// none declare a links format (nav.parentId is a plain string; the
// parent/child tree is queried directly by nav.parentId, not through
// backlinks).
func linkPropCatalog(ctx context.Context, sp space.Space) ([]linkProp, error) {
	types, err := sp.Types().List(ctx)
	if err != nil {
		return nil, err
	}
	var props []linkProp
	for _, t := range types {
		if t.BuiltIn {
			continue
		}
		defs, err := sp.Types().Properties(ctx, t.Id)
		if err != nil {
			return nil, err
		}
		for _, d := range defs {
			if d.Format != nil && d.Format.Type == space.FormatLinks {
				props = append(props, linkProp{typeId: t.Id, propId: d.Id})
			}
		}
	}
	return props, nil
}

// objectBacklinks handles GET /v1/spaces/:spaceId/objects/:objectId/backlinks.
//
// No existence check on objectId — backlinks of an unknown (or
// deleted) object is an empty list, not a 404.
//
//	@Summary	List objects that reference an object (links-format property values)
//	@Tags		objects
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Success	200			{object}	api.BacklinksResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/backlinks [get]
func (d *deps) objectBacklinks(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	ctx := c.Request().Context()

	props, err := linkPropCatalog(ctx, sp)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	backlinks := []api.Backlink{}
	if len(props) == 0 {
		return c.JSON(http.StatusOK, api.BacklinksResponse{Backlinks: backlinks})
	}

	link := "any://" + objectId

	// Scalar equality against an array field means "contains" (see
	// docs/09-query.md) — one disjunct per catalog property. Link
	// values carry no index, so this is a scan over the objects
	// collection; acceptable at v1 scale, a reverse index is the
	// follow-up.
	or := make(query.Or, 0, len(props))
	for _, p := range props {
		or = append(or, query.Key{Path: []string{p.typeId, p.propId}, Filter: query.NewComp(query.CompOpEq, link)})
	}

	iter, err := sp.QueryObjects().Filter(or).Sort("id").Iter(ctx)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	defer iter.Close()
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
		}
		srcId := string(doc.GetStringBytes("id"))
		attached := map[string]bool{}
		for _, v := range doc.GetArray("any", "types") {
			attached[string(v.GetStringBytes())] = true
		}
		// The filter says "some disjunct matched"; recover WHICH
		// properties reference the target. Values under a detached
		// type are stale, not live references — skipped, matching the
		// prop chunker's convention (internal/index/prop.go).
		for _, p := range props {
			if !attached[p.typeId] {
				continue
			}
			for _, el := range doc.GetArray(p.typeId, p.propId) {
				if string(el.GetStringBytes()) == link {
					backlinks = append(backlinks, api.Backlink{ObjectId: srcId, TypeId: p.typeId, PropId: p.propId})
					break
				}
			}
		}
	}
	if err := iter.Err(); err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	return c.JSON(http.StatusOK, api.BacklinksResponse{Backlinks: backlinks})
}
