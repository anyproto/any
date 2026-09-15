package server

import (
	"bytes"
	"context"
	"encoding/json"
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
	maxBundleIdBytes         = 256
	maxBundleNameBytes       = 1024
	maxBundleSeedBytes       = 256
	maxBundleTypes           = 32
	maxBundleParts           = 32
	maxBundlePartsBytes      = 64 * 1024
	maxBundlePropsBytes      = 64 * 1024
	maxBundleProperties      = 64
	maxBundlePropertiesBytes = 64 * 1024
	maxTypeXKeyBytes         = 256
)

// bundleCreateTimeout bounds the detached create-and-register section
// — one install here, the whole ordered walk of a catalog setup.
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
// and writes nothing (`installed: false`); otherwise the server mints
// the root with the requested types/properties and registers it.
//
// Two root strategies. A CREATED root (the default) gets a fresh id,
// so two devices installing while apart each register one, the
// registry converges on a winner and the other surfaces in `losers` —
// treat `rootId` as provisional until the space has synced. A DERIVED
// root (`"derived": true`) is computed from the bundle id, so every
// device lands on the same one: no fork, no convergence wait, no
// refusal — at the price of never being uninstallable.
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
	// The server's own prefix: refused before any wait. The embedded
	// catalog is its only writer, through the resolver directly.
	if bundles.ReservedId(inst.Id) {
		return writeError(c, http.StatusConflict, api.ErrBundleReserved,
			"bundle ids under "+bundles.ReservedIdPrefix+" are the server's — installed by its catalog, not by clients",
			map[string]any{"bundleId": inst.Id})
	}

	// The tech-space rules fail fast, BEFORE the space resolve and the
	// registry-convergence wait the resolver runs: a declaration
	// required, no root membership or properties (the SDK enforces the
	// same; this spares an invalid request the wait).
	if d.isTechSpace(c.Param("spaceId")) {
		switch {
		case !inst.Declares():
			return writeError(c, http.StatusBadRequest, "request.missing_field",
				"tech-space bundles must declare a type or collection — parts, properties or an xKey", nil)
		case inst.RootType != "" || len(inst.RootCollections) > 0 || len(inst.RootProperties) > 0:
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"rootType/rootCollections/rootProperties are not available on the tech space — a tech bundle root is its own definition", nil)
		}
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

	// One install through the same walk a catalog setup runs, so the
	// pre-install rule (a listed type already holding the xKey) is one
	// rule in one place.
	results, err := d.bundleResolver().Setup(ctx, createCtx, sp, []bundles.Install{inst}, catalogBeforeInstall)
	if err != nil {
		var se *bundles.SetupError
		if errors.As(err, &se) {
			err = se.Err
		}
		var xc *xKeyConflictError
		if errors.As(err, &xc) {
			return writeError(c, http.StatusConflict, "type.xkey_conflict",
				"xKey already in use by another type in this space",
				map[string]any{"xKey": xc.XKey, "existingTypeId": xc.ExistingTypeId, "spaceId": sp.Id(), "bundleId": inst.Id})
		}
		return bundleError(c, err, sp.Id(), inst.Id)
	}
	return c.JSON(http.StatusOK, api.BundleEnsureResponse{
		Bundle:    bundleToAPI(results[0].Bundle),
		Installed: results[0].Installed,
	})
}

// bundleInstallFromBody extracts and bounds the Ensure request. Shape
// checks come before positive extraction so a `rootCollections` object or a
// `rootProperties` array is rejected rather than silently skipped.
func bundleInstallFromBody(c echo.Context, root *fastjson.Value) (bundles.Install, error, bool) {
	var inst bundles.Install
	if v := root.Get("id"); v != nil && v.Type() != fastjson.TypeString {
		return inst, writeError(c, http.StatusBadRequest, "request.schema", "id must be a string", nil), true
	}
	if v := root.Get("name"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeString {
		return inst, writeError(c, http.StatusBadRequest, "request.schema", "name must be a string", nil), true
	}
	if v := root.Get("rootCollections"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeArray {
		return inst, writeError(c, http.StatusBadRequest, "request.schema",
			"rootCollections must be an array of collection ids", nil), true
	}
	if v := root.Get("rootProperties"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeObject {
		return inst, writeError(c, http.StatusBadRequest, "request.schema",
			`rootProperties must be an object keyed by owner id, e.g. {"any": {"description": "…"}}`, nil), true
	}
	if v := root.Get("derived"); v != nil && v.Type() != fastjson.TypeNull &&
		v.Type() != fastjson.TypeTrue && v.Type() != fastjson.TypeFalse {
		return inst, writeError(c, http.StatusBadRequest, "request.schema", "derived must be a boolean", nil), true
	}
	inst.Derived = root.GetBool("derived")
	if v := root.Get("parts"); v != nil && v.Type() != fastjson.TypeNull {
		if v.Type() != fastjson.TypeArray {
			return inst, writeError(c, http.StatusBadRequest, "request.schema",
				"parts must be an array of part drafts", nil), true
		}
		buf := v.MarshalTo(nil)
		if len(buf) > maxBundlePartsBytes {
			return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
				"parts too large", map[string]any{"max_bytes": maxBundlePartsBytes}), true
		}
		drafts, errResp, done := bundlePartsFromBody(c, buf)
		if done {
			return inst, errResp, true
		}
		inst.Parts = drafts
	}
	if v := root.Get("properties"); v != nil && v.Type() != fastjson.TypeNull {
		if v.Type() != fastjson.TypeArray {
			return inst, writeError(c, http.StatusBadRequest, "request.schema",
				"properties must be an array of property drafts", nil), true
		}
		buf := v.MarshalTo(nil)
		if len(buf) > maxBundlePropertiesBytes {
			return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
				"properties too large", map[string]any{"max_bytes": maxBundlePropertiesBytes}), true
		}
		drafts, errResp, done := bundlePropertiesFromBody(c, buf)
		if done {
			return inst, errResp, true
		}
		inst.Properties = drafts
	}
	if v := root.Get("layout"); v != nil && v.Type() != fastjson.TypeNull {
		layout, code, reason := layoutFromWire(v.MarshalTo(nil))
		if code != "" {
			return inst, writeError(c, http.StatusBadRequest, code, reason, map[string]any{"path": "layout"}), true
		}
		inst.Layout = layout
	}
	if v := root.Get("hidden"); v != nil && v.Type() != fastjson.TypeNull &&
		v.Type() != fastjson.TypeTrue && v.Type() != fastjson.TypeFalse {
		return inst, writeError(c, http.StatusBadRequest, "request.schema", "hidden must be a boolean", nil), true
	}
	inst.Hidden = root.GetBool("hidden")
	if v := root.Get("collection"); v != nil && v.Type() != fastjson.TypeNull &&
		v.Type() != fastjson.TypeTrue && v.Type() != fastjson.TypeFalse {
		return inst, writeError(c, http.StatusBadRequest, "request.schema", "collection must be a boolean", nil), true
	}
	inst.Collection = root.GetBool("collection")
	if v := root.Get("xKey"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeString {
		return inst, writeError(c, http.StatusBadRequest, "request.schema", "xKey must be a string", nil), true
	}
	inst.XKey = string(root.GetStringBytes("xKey"))
	if len(inst.XKey) > maxTypeXKeyBytes {
		return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"xKey too long", map[string]any{"max_bytes": maxTypeXKeyBytes}), true
	}
	if inst.Collection && len(inst.Layout) > 0 {
		return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"a collection has no layout", nil), true
	}
	if !inst.Declares() && (len(inst.Layout) > 0 || inst.Hidden) {
		return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"layout/hidden describe a definition — declare parts, properties or an xKey with them", nil), true
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

	if v := root.Get("rootType"); v != nil && v.Type() != fastjson.TypeNull && v.Type() != fastjson.TypeString {
		return inst, writeError(c, http.StatusBadRequest, "request.schema", "rootType must be a type id", nil), true
	}
	inst.RootType = string(root.GetStringBytes("rootType"))
	if inst.RootType != "" && inst.Declares() {
		return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"rootType and a declaration are exclusive — a declaring root carries its marker in any.type", nil), true
	}
	colls := root.GetArray("rootCollections")
	if len(colls) > maxBundleTypes {
		return inst, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"too many rootCollections", map[string]any{"max": maxBundleTypes}), true
	}
	for _, v := range colls {
		if v.Type() != fastjson.TypeString {
			return inst, writeError(c, http.StatusBadRequest, "request.schema",
				"rootCollections must be an array of collection ids", nil), true
		}
		inst.RootCollections = append(inst.RootCollections, string(v.GetStringBytes()))
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

// bundleDatasetsFromBody decodes the `datasets` drafts with the same
// strict decoder and converter POST …/types/:typeId/datasets uses, and
// applies the search indexer's reserved-name rule. Name conflicts with
// what the space already hosts are the SDK's verdict (400 via
// ErrBundleBadRequest) — on adopt the names legitimately exist.
func bundlePartsFromBody(c echo.Context, body []byte) ([]space.PartDraft, error, bool) {
	var reqs []api.PartDraftRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&reqs); err != nil {
		// Same envelope as the strict binder on the types route.
		return nil, strictDecodeFailed(c, err, reflect.TypeFor[api.PartDraftRequest](),
			"parts", "parts: ", bindErrorMessage[[]api.PartDraftRequest]), true
	}
	if len(reqs) > maxBundleParts {
		return nil, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"too many parts", map[string]any{"max": maxBundleParts}), true
	}
	out := make([]space.PartDraft, 0, len(reqs))
	for i := range reqs {
		draft, code, reason, details := partDraftFromAPI(reqs[i])
		if code != "" {
			if details == nil {
				details = map[string]any{}
			}
			details["part"] = i
			return nil, writeError(c, http.StatusBadRequest, code,
				fmt.Sprintf("parts[%d]: %s", i, reason), details), true
		}
		out = append(out, draft)
	}
	return out, nil, false
}

// bundlePropertiesFromBody decodes the `properties` drafts with the
// same strict decoder and gate POST …/types/:typeId/properties uses,
// plus the bundle rule: every draft carries an xKey, unique in the
// body — the property id derives from it.
func bundlePropertiesFromBody(c echo.Context, body []byte) ([]space.PropertyDraft, error, bool) {
	var reqs []api.AddPropertyRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&reqs); err != nil {
		return nil, strictDecodeFailed(c, err, reflect.TypeFor[api.AddPropertyRequest](),
			"properties", "properties: ", bindErrorMessage[[]api.AddPropertyRequest]), true
	}
	if len(reqs) > maxBundleProperties {
		return nil, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"too many properties", map[string]any{"max": maxBundleProperties}), true
	}
	out := make([]space.PropertyDraft, 0, len(reqs))
	seen := make(map[string]struct{}, len(reqs))
	for i := range reqs {
		details := map[string]any{"property": i}
		if reqs[i].XKey == "" {
			return nil, writeError(c, http.StatusBadRequest, "request.missing_field",
				fmt.Sprintf("properties[%d]: xKey required — a bundle property's id derives from it", i), details), true
		}
		if _, dup := seen[reqs[i].XKey]; dup {
			details["xKey"] = reqs[i].XKey
			return nil, writeError(c, http.StatusBadRequest, "request.invalid_field",
				fmt.Sprintf("properties[%d]: xKey declared twice", i), details), true
		}
		seen[reqs[i].XKey] = struct{}{}
		draft, code, reason, more := propertyDraftFromAPI(reqs[i])
		if code != "" {
			for k, v := range more {
				details[k] = v
			}
			return nil, writeError(c, http.StatusBadRequest, code,
				fmt.Sprintf("properties[%d]: %s", i, reason), details), true
		}
		out = append(out, draft)
	}
	return out, nil, false
}

// checkBundleRoot pre-flights everything that would otherwise fail
// silently or too late: a type or collection id the space does not
// know (the create path drops the membership and reports success), and
// property values that do not fit their descriptor slug (the same gate
// propertiesSet and objectCreate run).
func (d *deps) checkBundleRoot(c echo.Context, sp space.Space, inst bundles.Install) (error, bool) {
	ctx := c.Request().Context()
	if inst.RootType != "" {
		if _, err := sp.Types().Get(ctx, inst.RootType); err != nil {
			return writeError(c, http.StatusBadRequest, "type.not_found",
				"rootType names a type this space does not have",
				map[string]any{"typeId": inst.RootType, "spaceId": sp.Id()}), true
		}
		// Refused here: the root's tree is minted before the write the
		// SDK would reject, and a row-less tree cannot be named to delete.
		if reservedCarrierType(ctx, sp, inst.RootType) {
			return reservedCarrierError(c, sp.Id(), inst.RootType), true
		}
	}
	for _, id := range inst.RootCollections {
		if _, err := sp.Collections().Get(ctx, id); err != nil {
			return writeError(c, http.StatusBadRequest, "collection.not_found",
				"rootCollections names a collection this space does not have",
				map[string]any{"collectionId": id, "spaceId": sp.Id()}), true
		}
	}
	for ownerId, patch := range inst.RootProperties {
		// An owner that is neither the root's type nor `any` is filed
		// as a collection, so it must be one — the preflight's 400s.
		if ownerId != inst.RootType && ownerId != "any" {
			if _, err := sp.Collections().Get(ctx, ownerId); err != nil {
				details := map[string]any{"ownerId": ownerId, "spaceId": sp.Id()}
				if errors.Is(err, space.ErrNotACollection) {
					return writeError(c, http.StatusBadRequest, "collection.not_a_collection",
						"rootProperties keyed by a type that is not rootType — a type goes in rootType, a collection in rootCollections", details), true
				}
				if errors.Is(err, space.ErrNotFound) {
					return writeError(c, http.StatusBadRequest, "collection.not_found",
						"rootProperties names a collection this space does not have", details), true
				}
				return sdkOpError(c, err, details), true
			}
		}
		defs, err := ownerProperties(ctx, sp, ownerId)
		if err != nil {
			return writeError(c, http.StatusBadRequest, "type.not_found",
				"rootProperties names a type or collection this space does not have",
				map[string]any{"ownerId": ownerId, "spaceId": sp.Id()}), true
		}
		if v := validateDescriptorValues(defs, patch); v != nil {
			return writeError(c, http.StatusBadRequest, "property.format_violation",
				"initial property value does not fit the property's descriptor",
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
	// The read-side lock: answer from a converged registry so absence
	// is definitive; when the wait expires (cold offline device) the
	// reply says so instead of lying.
	synced := d.bundleResolver().WaitConverged(c.Request().Context(), sp)
	rows, err := d.bundleResolver().List(c.Request().Context(), sp)
	if err != nil {
		return bundleError(c, err, sp.Id(), "")
	}
	out := api.BundleListResponse{Bundles: make([]api.Bundle, 0, len(rows)), Synced: synced}
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
//	@Success	200			{object}	api.BundleGetResponse
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
	synced := d.bundleResolver().WaitConverged(c.Request().Context(), sp)
	b, err := d.bundleResolver().Get(c.Request().Context(), sp, bundleId)
	if err != nil {
		return bundleError(c, err, sp.Id(), bundleId)
	}
	return c.JSON(http.StatusOK, api.BundleGetResponse{Bundle: bundleToAPI(b), Synced: synced})
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
		// as an engine goroutine, which outlives the response but not
		// the account (it holds a space handle); the resolver dedups so
		// a polling client cannot stack loops.
		if errors.Is(err, bundles.ErrLoserNotReady) {
			d.spawnEngine(func(ctx context.Context) {
				d.bundleResolver().ResolveRetry(ctx, sp, bundleId, req.LoserRootId)
			})
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
	if len(req.Collections) > maxBundleTypes {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"too many collections", map[string]any{"max": maxBundleTypes})
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
	// Same pre-check as the root's: the child is derived before its
	// type is written.
	if req.Type != "" && reservedCarrierType(ctx, sp, req.Type) {
		return reservedCarrierError(c, sp.Id(), req.Type)
	}
	objectId, err := bundles.Child(ctx, sp, b, req.Seed, req.Type, req.Collections...)
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
		Id:      b.Id,
		Name:    b.Name,
		RootId:  b.RootId,
		Roots:   b.Roots,
		Losers:  b.Losers,
		Derived: b.Derived,
	}
}

// bundleError maps the registry sentinels onto the canonical envelope;
// anything else falls through to the shared SDK mapping.
func bundleError(c echo.Context, err error, spaceId, bundleId string) error {
	details := map[string]any{"spaceId": spaceId}
	if bundleId != "" {
		details["bundleId"] = bundleId
	}
	return bundleErrorWith(c, err, details)
}

// bundleErrorWith is bundleError with caller-built details (a catalog
// setup adds the usecase the failing bundle belongs to).
func bundleErrorWith(c echo.Context, err error, details map[string]any) error {
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
	case errors.Is(err, space.ErrBundleRootNotSynced):
		return writeError(c, http.StatusConflict, api.ErrBundleNotReady,
			"the bundle root has not synced to this device yet; retry", details)
	case errors.Is(err, space.ErrObjectNotFound):
		// A tree this device does not hold yet — a joiner's first call
		// right after the accept lands. On the bundle paths that is the
		// same retryable state as an unsynced root, not a missing
		// object, so the poll-after-join recipe never meets a 404.
		return writeError(c, http.StatusConflict, api.ErrBundleNotReady,
			"the bundle's root has not reached this device yet; retry", details)
	case errors.Is(err, bundles.ErrLoserNotReady), errors.Is(err, space.ErrLoserNotSynced):
		return writeError(c, http.StatusConflict, api.ErrBundleLoserNotReady,
			"the losing root is still syncing; retry once it has settled", details)
	case errors.Is(err, space.ErrBundleNotLoser):
		return writeError(c, http.StatusConflict, api.ErrBundleNotLoser,
			"root is not a loser of this bundle", details)
	case errors.Is(err, space.ErrModuleReserved):
		return writeError(c, http.StatusBadRequest, api.ErrDatasetModuleReserved,
			"a part names a module reserved to the server's own installs", details)
	case errors.Is(err, space.ErrBundleBadRequest):
		details["reason"] = err.Error()
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"invalid bundle request", details)
	}
	return sdkOpError(c, err, details)
}
