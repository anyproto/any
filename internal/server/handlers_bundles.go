package server

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync/commonspace/object/tree/treestorage"
	"github.com/anyproto/any-sync/commonspace/spacestorage"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bundles"
)

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
	req, ok := bindBodyStrict[api.BundleEnsureRequest](c, "")
	if !ok {
		return nil
	}
	if req.Id == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "id required", nil)
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	b, installed, err := d.bundleResolver().Ensure(c.Request().Context(), sp, bundles.Install{
		Id:             req.Id,
		Name:           req.Name,
		RootTypes:      req.RootTypes,
		RootProperties: req.RootProperties,
	})
	if err != nil {
		return bundleError(c, err, sp.Id(), req.Id)
	}
	return c.JSON(http.StatusOK, api.BundleEnsureResponse{
		Bundle:    bundleToAPI(b),
		Installed: installed,
	})
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
		// missing, so keep trying without the client having to.
		// shutdownCtx, not the request's: this outlives the response.
		if !errors.Is(err, space.ErrBundleNotLoser) && !errors.Is(err, space.ErrBundleUnknown) {
			go d.bundleResolver().ResolveRetry(d.shutdownCtx, sp, bundleId, req.LoserRootId)
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
		// A child binds to its parent's tree, so an unknown-tree error
		// here means the winner has not reached this device yet — the
		// same retryable state Ensure reports, not a bad request.
		if errors.Is(err, spacestorage.ErrTreeStorageAlreadyDeleted) || errors.Is(err, treestorage.ErrUnknownTreeId) {
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
	case errors.Is(err, bundles.ErrLoserNotReady):
		return writeError(c, http.StatusConflict, api.ErrBundleLoserNotReady,
			"the losing root is still syncing; retry once it has settled", details)
	case errors.Is(err, space.ErrBundleNotLoser):
		return writeError(c, http.StatusConflict, api.ErrBundleNotLoser,
			"root is not a loser of this bundle", details)
	case errors.Is(err, space.ErrBundleBadRequest):
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"invalid bundle request", details)
	}
	return sdkOpError(c, err, details)
}
