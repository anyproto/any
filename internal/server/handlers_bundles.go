package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync/commonspace/object/tree/objecttree"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bundles"
)

// Input caps. Bundle records are PERMANENT — the registry refuses
// record deletes, so an id is spent forever and its row rides the
// eagerly-loaded spaceIndex on every device. Bounds keep one client
// from bloating that object for everyone.
const (
	maxBundleIdBytes    = 256
	maxBundleNameBytes  = 1024
	maxBundleSeedBytes  = 256
	maxBundleTypes      = 32
	maxBundlePropsBytes = 64 * 1024
)

// bundleCreateTimeout bounds the detached create-and-register section.
const bundleCreateTimeout = 2 * time.Minute

// bundleEnsureFields is the closed Ensure vocabulary, derived from the
// api request struct — the same source the swagger spec is generated
// from, so spec and enforcement cannot drift.
var bundleEnsureFields = jsonFieldNames(reflect.TypeFor[api.BundleEnsureRequest]())

// The bundles registry over HTTP — what a client has installed into a
// space (any-sync-sdk docs/bundles.md). The server keeps no catalog:
// clients declare their own bundles, and the only thing it enforces is
// what is decidable without knowing the content.
//
// Bundle ids carry a version suffix and therefore a slash, so in a
// path segment they are percent-encoded (`general-chat%2Fv1`); bodies
// take them verbatim.

func registerBundleRoutes(g *echo.Group, d *deps) {
	g.POST("/spaces/:spaceId/bundles", d.bundleEnsure)
	g.GET("/spaces/:spaceId/bundles", d.bundleList)
	g.GET("/spaces/:spaceId/bundles/:bundleId", d.bundleGet)
	g.POST("/spaces/:spaceId/bundles/:bundleId/resolve", d.bundleResolve)
	g.POST("/spaces/:spaceId/bundles/:bundleId/children", d.bundleChild)
}

// bundleEnsure handles POST /v1/spaces/:spaceId/bundles — adopt or
// install. With a winner already registered the call is a local read
// and writes nothing (`installed: false`); otherwise the server
// creates the non-derived root with the requested types/properties and
// registers it in one change.
//
// Offline-capable: nothing here waits on the network. Two devices
// ensuring while apart each register a root, the registry converges on
// one winner, and the other surfaces in `losers` — so treat `rootId`
// as provisional until the space has synced, and re-read after.
//
//	@Summary	Install or adopt a bundle
//	@Tags		bundles
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string						true	"Space ID"
//	@Param		body	body		api.BundleEnsureRequest		true	"Bundle to register"
//	@Success	200		{object}	api.BundleEnsureResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/bundles [post]
func (d *deps) bundleEnsure(c echo.Context) error {
	body, err := readBody(c)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "read body: "+err.Error(), nil)
	}
	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	root, err := parser.ParseBytes(body)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil)
	}
	if errResp, done := checkUnknownFields(c, root, "", bundleEnsureFields...); done {
		return errResp
	}
	inst, errResp, done := bundleInstallFromBody(c, root)
	if done {
		return errResp
	}

	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	ctx := c.Request().Context()
	// Validate the root's types and property values BEFORE anything is
	// created: objects.Create swallows a bad type attachment, and a
	// rejected property value would otherwise leave an orphan root
	// nothing references.
	if errResp, done := d.checkBundleRoot(c, sp, inst); done {
		return errResp
	}

	// Detached from the request: a client that disconnects between
	// minting the root and registering it would otherwise leave an
	// object nothing references. Bounded by shutdown and a timeout.
	createCtx, cancel := context.WithTimeout(d.backgroundCtx(), bundleCreateTimeout)
	defer cancel()

	b, installed, err := d.bundleResolver().Ensure(ctx, createCtx, sp, inst)
	if err != nil {
		return bundleError(c, err, sp.Id(), inst.Id)
	}
	return c.JSON(http.StatusOK, api.BundleEnsureResponse{
		Bundle:    bundleToAPI(b),
		Installed: installed,
	})
}

// bundleInstallFromBody extracts and bounds the Ensure request. Shape
// checks come before positive extraction so a `rootTypes` object or a
// `rootProperties` array is rejected rather than silently skipped.
func bundleInstallFromBody(c echo.Context, root *fastjson.Value) (bundles.Install, error, bool) {
	var inst bundles.Install
	if v := root.Get("id"); v != nil && v.Type() != fastjson.TypeString {
		return inst, writeError(c, http.StatusBadRequest, "request.schema", "id must be a string", nil), true
	}
	if v := root.Get("name"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeString {
		return inst, writeError(c, http.StatusBadRequest, "request.schema", "name must be a string", nil), true
	}
	if v := root.Get("rootTypes"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeArray {
		return inst, writeError(c, http.StatusBadRequest, "request.schema",
			"rootTypes must be an array of type ids", nil), true
	}
	if v := root.Get("rootProperties"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeObject {
		return inst, writeError(c, http.StatusBadRequest, "request.schema",
			`rootProperties must be an object keyed by type id, e.g. {"any": {"description": "…"}}`, nil), true
	}

	inst.Id = string(root.GetStringBytes("id"))
	inst.Name = string(root.GetStringBytes("name"))
	if inst.Id == "" {
		return inst, writeError(c, http.StatusBadRequest, "request.missing_field", "id required", nil), true
	}
	if len(inst.Id) > maxBundleIdBytes {
		return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"id too long", map[string]any{"max_bytes": maxBundleIdBytes}), true
	}
	if len(inst.Name) > maxBundleNameBytes {
		return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"name too long", map[string]any{"max_bytes": maxBundleNameBytes}), true
	}

	types := root.GetArray("rootTypes")
	if len(types) > maxBundleTypes {
		return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"too many rootTypes", map[string]any{"max": maxBundleTypes}), true
	}
	for _, v := range types {
		if v.Type() != fastjson.TypeString {
			return inst, writeError(c, http.StatusBadRequest, "request.schema",
				"rootTypes must be an array of type ids", nil), true
		}
		inst.RootTypes = append(inst.RootTypes, string(v.GetStringBytes()))
	}

	if props := root.Get("rootProperties"); props != nil && props.Type() == fastjson.TypeObject {
		if len(props.MarshalTo(nil)) > maxBundlePropsBytes {
			return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
				"rootProperties too large", map[string]any{"max_bytes": maxBundlePropsBytes}), true
		}
		inst.RootProperties = map[string]map[string]any{}
		var badGroup string
		props.GetObject().Visit(func(typeKey []byte, group *fastjson.Value) {
			obj, err := group.Object()
			if err != nil {
				badGroup = string(typeKey)
				return
			}
			kv := map[string]any{}
			obj.Visit(func(propKey []byte, val *fastjson.Value) { kv[string(propKey)] = val })
			inst.RootProperties[string(typeKey)] = kv
		})
		if badGroup != "" {
			return inst, writeError(c, http.StatusBadRequest, "request.schema",
				fmt.Sprintf("rootProperties.%s must be an object of {propertyId: value}", badGroup),
				map[string]any{"typeKey": badGroup}), true
		}
	}
	return inst, nil, false
}

// checkBundleRoot pre-flights everything that would otherwise fail
// silently or too late: a type id the space does not know (the create
// path drops the attachment and reports success), and property values
// that violate their declared format (the same gate propertiesSet and
// objectCreate run).
func (d *deps) checkBundleRoot(c echo.Context, sp space.Space, inst bundles.Install) (error, bool) {
	ctx := c.Request().Context()
	for _, typeId := range inst.RootTypes {
		if _, err := sp.Types().Get(ctx, typeId); err != nil {
			return writeError(c, http.StatusBadRequest, "type.not_found",
				"rootTypes names a type this space does not have",
				map[string]any{"typeId": typeId, "spaceId": sp.Id()}), true
		}
	}
	for typeId, patch := range inst.RootProperties {
		if _, err := sp.Types().Get(ctx, typeId); err != nil {
			return writeError(c, http.StatusBadRequest, "type.not_found",
				"rootProperties names a type this space does not have",
				map[string]any{"typeId": typeId, "spaceId": sp.Id()}), true
		}
		defs, err := sp.Types().Properties(ctx, typeId)
		if err != nil {
			return sdkOpError(c, err, map[string]any{"typeId": typeId, "spaceId": sp.Id()}), true
		}
		if v := validateFormatValues(defs, patch); v != nil {
			return writeError(c, http.StatusBadRequest, "property.format_violation",
				"initial property value does not match the property's declared format",
				v.details()), true
		}
	}
	return nil, false
}

// bundleList handles GET /v1/spaces/:spaceId/bundles — every live row
// as of local state. A restored device sees what has synced so far;
// rows appear as the space catches up.
//
//	@Summary	List the space's bundles
//	@Tags		bundles
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	200		{object}	api.BundleListResponse
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/bundles [get]
func (d *deps) bundleList(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	rows, err := d.bundleResolver().List(c.Request().Context(), sp)
	if err != nil {
		return bundleError(c, err, sp.Id(), "")
	}
	out := api.BundleListResponse{Bundles: make([]api.Bundle, 0, len(rows))}
	for _, b := range rows {
		out.Bundles = append(out.Bundles, bundleToAPI(b))
	}
	return c.JSON(http.StatusOK, out)
}

// bundleGet handles GET /v1/spaces/:spaceId/bundles/:bundleId — one
// row, the id percent-encoded in the path.
//
//	@Summary	Read one bundle
//	@Tags		bundles
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		bundleId	path		string	true	"Bundle ID, percent-encoded (general-chat%2Fv1)"
//	@Success	200			{object}	api.Bundle
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/bundles/{bundleId} [get]
func (d *deps) bundleGet(c echo.Context) error {
	bundleId, errResp, done := bundleIdParam(c)
	if done {
		return errResp
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	b, err := d.bundleResolver().Get(c.Request().Context(), sp, bundleId)
	if err != nil {
		return bundleError(c, err, sp.Id(), bundleId)
	}
	return c.JSON(http.StatusOK, bundleToAPI(b))
}

// bundleResolve handles POST /v1/spaces/:spaceId/bundles/:bundleId/resolve
// — delete a losing root, cascading to its derived children.
//
// The client calls this AFTER merging whatever mattered out of the
// loser: the server never merges, because only the client knows what
// its content means. What the server does enforce is timing — a loser
// whose tree is still arriving reads incomplete, so a merge made from
// it would be incomplete too. Until it settles the call returns 409
// `bundle.loser_not_ready` and the server keeps retrying in the
// background (dropped on restart, so clients retry too).
//
// Idempotent: a root already deleted returns 204.
//
//	@Summary	Resolve a losing root
//	@Tags		bundles
//	@Accept		json
//	@Param		spaceId		path	string						true	"Space ID"
//	@Param		bundleId	path	string						true	"Bundle ID, percent-encoded"
//	@Param		body		body	api.BundleResolveRequest	true	"Losing root to delete"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/bundles/{bundleId}/resolve [post]
func (d *deps) bundleResolve(c echo.Context) error {
	bundleId, errResp, done := bundleIdParam(c)
	if done {
		return errResp
	}
	req, ok := bindBodyStrict[api.BundleResolveRequest](c, "")
	if !ok {
		return nil
	}
	if req.LoserRootId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "loserRootId required", nil)
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	err := d.bundleResolver().Resolve(c.Request().Context(), sp, bundleId, req.LoserRootId)
	if err != nil {
		// The merge decision is already made — only the timing is
		// missing, so keep trying without the client having to. Armed
		// ONLY for the timing refusal: a verdict retrying cannot change
		// must not leave a loop that outlives the client's intent. Runs
		// on the background context, which outlives the response; the
		// resolver dedups so a polling client cannot stack loops.
		if errors.Is(err, bundles.ErrLoserNotReady) {
			go d.bundleResolver().ResolveRetry(d.backgroundCtx(), sp, bundleId, req.LoserRootId)
		}
		return bundleError(c, err, sp.Id(), bundleId)
	}
	return c.NoContent(http.StatusNoContent)
}

// bundleChild handles POST /v1/spaces/:spaceId/bundles/:bundleId/children
// — derive a setup object under the bundle's current winner. Same
// semantics as the objects derive: deterministic per (space, root,
// seed), materialized on first call, the same id on every device, and
// cascade-deleted with the root.
//
//	@Summary	Derive a setup object under the bundle root
//	@Tags		bundles
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string					true	"Space ID"
//	@Param		bundleId	path		string					true	"Bundle ID, percent-encoded"
//	@Param		body		body		api.BundleChildRequest	true	"Child seed and types"
//	@Success	200			{object}	api.BundleChildResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	409			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/bundles/{bundleId}/children [post]
func (d *deps) bundleChild(c echo.Context) error {
	bundleId, errResp, done := bundleIdParam(c)
	if done {
		return errResp
	}
	req, ok := bindBodyStrict[api.BundleChildRequest](c, "")
	if !ok {
		return nil
	}
	if req.Seed == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "seed required", nil)
	}
	if len(req.Seed) > maxBundleSeedBytes {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"seed too long", map[string]any{"max_bytes": maxBundleSeedBytes})
	}
	if len(req.Types) > maxBundleTypes {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"too many types", map[string]any{"max": maxBundleTypes})
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	ctx := c.Request().Context()
	b, err := d.bundleResolver().Get(ctx, sp, bundleId)
	if err != nil {
		return bundleError(c, err, sp.Id(), bundleId)
	}
	objectId, err := bundles.Child(ctx, sp, b.RootId, req.Seed, req.Types...)
	if err != nil {
		// A child is built on its parent's tree, so a missing parent
		// means the winner has not reached this device yet — the same
		// retryable state Ensure reports, not a bad request.
		if errors.Is(err, objecttree.ErrParentNotFound) {
			return bundleError(c, bundles.ErrRootNotLocal, sp.Id(), bundleId)
		}
		return bundleError(c, err, sp.Id(), bundleId)
	}
	return c.JSON(http.StatusOK, api.BundleChildResponse{ObjectId: objectId})
}

// bundleIdParam decodes the percent-encoded :bundleId path segment.
// Ids carry a slash, which a path segment cannot hold raw.
func bundleIdParam(c echo.Context) (string, error, bool) {
	raw := c.Param("bundleId")
	if raw == "" {
		return "", writeError(c, http.StatusBadRequest, "request.missing_field", "bundleId required", nil), true
	}
	id, err := url.PathUnescape(raw)
	if err != nil || id == "" {
		return "", writeError(c, http.StatusBadRequest, "request.invalid_field",
			"bundleId must be percent-encoded", map[string]any{"bundleId": raw}), true
	}
	return id, nil, false
}

func bundleToAPI(b space.Bundle) api.Bundle {
	return api.Bundle{
		Id:     b.Id,
		Name:   b.Name,
		RootId: b.RootId,
		Roots:  b.Roots,
		Losers: b.Losers,
	}
}

// bundleError maps the registry sentinels onto the canonical envelope;
// anything else falls through to the shared SDK mapping.
func bundleError(c echo.Context, err error, spaceId, bundleId string) error {
	details := map[string]any{"spaceId": spaceId}
	if bundleId != "" {
		details["bundleId"] = bundleId
	}
	switch {
	case errors.Is(err, bundles.ErrNotInstalled), errors.Is(err, space.ErrBundleUnknown):
		return writeError(c, http.StatusNotFound, api.ErrBundleNotFound,
			"bundle not installed in this space", details)
	case errors.Is(err, bundles.ErrRootNotLocal):
		return writeError(c, http.StatusConflict, api.ErrBundleNotReady,
			"the bundle's root has not synced to this device yet; retry", details)
	case errors.Is(err, bundles.ErrRegistryNotSynced):
		return writeError(c, http.StatusConflict, api.ErrBundleNotReady,
			"the space's bundles registry has not synced to this device yet; retry", details)
	case errors.Is(err, bundles.ErrLoserNotReady), errors.Is(err, space.ErrLoserNotSynced):
		return writeError(c, http.StatusConflict, api.ErrBundleLoserNotReady,
			"the losing root is still syncing; retry once it has settled", details)
	case errors.Is(err, space.ErrBundleNotLoser):
		return writeError(c, http.StatusConflict, api.ErrBundleNotLoser,
			"root is not a loser of this bundle", details)
	case errors.Is(err, space.ErrBundleBadRequest):
		details["reason"] = err.Error()
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"invalid bundle request", details)
	}
	return sdkOpError(c, err, details)
}
