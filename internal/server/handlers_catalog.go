package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bundles"
	"github.com/anyproto/any/internal/miniapp"
)

// The usecase catalog over HTTP — the server's embedded set of
// well-known bundles (docs/28-well-known-bundles.md). Account-level:
// the catalog belongs to the server, not to a space, so the routes sit
// outside the space group like /v1/devices. Setup installs a usecase
// into a space with its dependencies, through the same bundles
// resolver the per-space ensure uses, under the system-install option
// that lifts the `system:` id and reserved-module refusals.

func registerCatalogRoutes(g *echo.Group, d *deps) {
	g.GET("/catalog", d.catalogList)
	g.GET("/catalog/:usecaseId", d.catalogGet)
	g.POST("/catalog/:usecaseId/setup", d.catalogSetup)
}

// catalogList handles GET /v1/catalog — every usecase as declared.
//
//	@Summary	List the usecase catalog
//	@Tags		catalog
//	@Produce	json
//	@Success	200	{object}	api.CatalogListResponse
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/catalog [get]
func (d *deps) catalogList(c echo.Context) error {
	cc, err := d.usecaseCatalog()
	if err != nil {
		return writeError(c, http.StatusInternalServerError, "server.internal", "usecase catalog failed to compile", nil)
	}
	out := api.CatalogListResponse{Usecases: make([]api.CatalogUsecase, 0, len(cc.cat.Usecases))}
	out.Usecases = append(out.Usecases, cc.cat.Usecases...)
	return c.JSON(http.StatusOK, out)
}

// catalogGet handles GET /v1/catalog/:usecaseId — one usecase.
//
//	@Summary	Read one usecase
//	@Tags		catalog
//	@Produce	json
//	@Param		usecaseId	path		string	true	"Usecase id (a slug)"
//	@Success	200			{object}	api.CatalogUsecase
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/catalog/{usecaseId} [get]
func (d *deps) catalogGet(c echo.Context) error {
	cc, err := d.usecaseCatalog()
	if err != nil {
		return writeError(c, http.StatusInternalServerError, "server.internal", "usecase catalog failed to compile", nil)
	}
	u, ok := cc.cat.Get(c.Param("usecaseId"))
	if !ok {
		return catalogNotFound(c, c.Param("usecaseId"))
	}
	return c.JSON(http.StatusOK, u)
}

func catalogNotFound(c echo.Context, id string) error {
	return writeError(c, http.StatusNotFound, api.ErrCatalogNotFound,
		"no such usecase in the catalog", map[string]any{"usecaseId": id})
}

// catalogSetup handles POST /v1/catalog/:usecaseId/setup — set a
// usecase up in a space: its dependency closure first, then its own
// bundles, every step adopt-or-install and idempotent, against one
// registry-convergence wait. The reply lists every bundle the call
// touched with what a client needs to use it — the root as the type
// id, the property handles resolved to ids, the miniapp values.
//
// Two things happen only on the install path: the type-handle check
// (a listed type already holding the bundle's xKey is `409
// type.xkey_conflict` — a user type minted before the catalog knew the
// handle), before the root is minted; and, on an adopt by a writer,
// the `miniapp` values the root lacks are written — the catalog may
// gain one in a later release, and adopt never re-seeds on its own.
//
// A failure mid-walk leaves the dependencies it installed and names
// the failing usecase and bundle; the next call resumes.
//
//	@Summary	Set a usecase up in a space, dependencies included
//	@Tags		catalog
//	@Accept		json
//	@Produce	json
//	@Param		usecaseId	path		string					true	"Usecase id (a slug)"
//	@Param		body		body		api.CatalogSetupRequest	true	"Target space"
//	@Success	200			{object}	api.CatalogSetupResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	403			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	405			{object}	api.ErrorEnvelope
//	@Failure	409			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/catalog/{usecaseId}/setup [post]
func (d *deps) catalogSetup(c echo.Context) error {
	cc, err := d.usecaseCatalog()
	if err != nil {
		return writeError(c, http.StatusInternalServerError, "server.internal", "usecase catalog failed to compile", nil)
	}
	usecaseId := c.Param("usecaseId")
	order, ok := cc.order(usecaseId)
	if !ok {
		return catalogNotFound(c, usecaseId)
	}
	req, ok := bindBodyStrict[api.CatalogSetupRequest](c, "")
	if !ok {
		return nil
	}
	if req.SpaceId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "spaceId required", nil)
	}
	if d.isTechSpace(req.SpaceId) {
		return unsupportedOnTechSpace(c, req.SpaceId)
	}
	ctx := c.Request().Context()
	sp, err := d.sdk.Spaces().Get(ctx, req.SpaceId)
	if err != nil {
		return spaceError(c, err, req.SpaceId)
	}

	var (
		installs []bundles.Install
		byId     = map[string]*compiledBundle{}
	)
	for _, cu := range order {
		for i := range cu.bundles {
			cb := &cu.bundles[i]
			installs = append(installs, cb.install)
			byId[cb.install.Id] = cb
		}
	}

	// Detached from the request, as the single-bundle ensure is: a
	// client that disconnects mid-walk must not leave a root nothing
	// references. Bounded by shutdown and the create timeout.
	createCtx, cancel := context.WithTimeout(d.backgroundCtx(), bundleCreateTimeout)
	defer cancel()

	results, err := d.bundleResolver().Setup(ctx, createCtx, sp, installs, catalogBeforeInstall)
	if err != nil {
		var se *bundles.SetupError
		details := map[string]any{"spaceId": sp.Id(), "usecaseId": usecaseId}
		if errors.As(err, &se) {
			details["bundleId"] = se.Install.Id
			if cb := byId[se.Install.Id]; cb != nil {
				details["usecase"] = cb.usecase
			}
			var xc *xKeyConflictError
			if errors.As(se.Err, &xc) {
				details["xKey"] = xc.XKey
				details["existingTypeId"] = xc.ExistingTypeId
				return writeError(c, http.StatusConflict, "type.xkey_conflict",
					"a type in this space already holds the handle the catalog type declares — remove that type, then set up again", details)
			}
			err = se.Err
		}
		return bundleErrorWith(c, err, details)
	}

	out := api.CatalogSetupResponse{Usecase: usecaseId, Bundles: make([]api.CatalogSetupBundle, 0, len(results))}
	for _, r := range results {
		cb := byId[r.Install.Id]
		row := api.CatalogSetupBundle{
			Usecase: cb.usecase, Id: r.Install.Id,
			Bundle: bundleToAPI(r.Bundle), Installed: r.Installed,
		}
		if r.Install.DeclaresType() {
			row.TypeId = r.Bundle.RootId
			props, err := sp.Types().Properties(ctx, r.Bundle.RootId)
			if err != nil {
				return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "bundleId": r.Install.Id})
			}
			row.Properties = make(map[string]string, len(props))
			for _, p := range props {
				if p.XKey != "" {
					row.Properties[p.XKey] = p.Id
				}
			}
			if !r.Installed {
				if err := healOptions(ctx, sp, r.Bundle.RootId, r.Install.Properties, props); err != nil {
					return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "bundleId": r.Install.Id})
				}
			}
		}
		if cb.miniapp != nil {
			row.Miniapp = cb.miniapp
			if !r.Installed {
				if err := healMiniapp(ctx, sp, r.Bundle.RootId, cb.miniapp); err != nil {
					return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "bundleId": r.Install.Id})
				}
			}
		}
		out.Bundles = append(out.Bundles, row)
	}
	return c.JSON(http.StatusOK, out)
}

// xKeyConflictError reports a listed type already holding a catalog
// type's handle. Raised before the root is minted, so a refused setup
// leaves nothing behind.
type xKeyConflictError struct {
	XKey           string
	ExistingTypeId string
}

func (e *xKeyConflictError) Error() string {
	return "xKey " + e.XKey + " already held by type " + e.ExistingTypeId
}

// catalogBeforeInstall is the pre-install rule the resolver runs for a
// catalog install: no type in the space may already hold the handle.
// Runs only when no winner exists, so an adopted root — which carries
// the handle by design — never conflicts with itself; the space's
// registered built-ins were checked against the catalog at boot.
func catalogBeforeInstall(ctx context.Context, sp space.Space, inst bundles.Install) error {
	if inst.XKey == "" {
		return nil
	}
	infos, err := sp.Types().List(ctx)
	if err != nil {
		return err
	}
	for _, t := range infos {
		if t.XKey == inst.XKey || t.Id == inst.XKey {
			return &xKeyConflictError{XKey: inst.XKey, ExistingTypeId: t.Id}
		}
	}
	return nil
}

// healMiniapp writes the `miniapp` values an adopted root lacks — a
// value the catalog gained after the install — attaching the built-in
// first when the root predates it. Never overwrites, and only a
// member who may write does it; a reader's setup leaves the root as
// it is.
func healMiniapp(ctx context.Context, sp space.Space, rootId string, values map[string]any) error {
	switch sp.Info().OwnRole {
	case space.PermissionOwner, space.PermissionAdmin, space.PermissionWriter:
	default:
		return nil
	}
	row, err := sp.Objects().Get(ctx, rootId)
	if err != nil {
		return err
	}
	carries := false
	for _, t := range row.GetArray("any", "types") {
		if string(t.GetStringBytes()) == miniapp.TypeId {
			carries = true
			break
		}
	}
	if !carries {
		if _, err := sp.Properties().AttachType(ctx, rootId, miniapp.TypeId); err != nil {
			return err
		}
	}
	missing := map[string]any{}
	for k, v := range values {
		if row.Get(miniapp.TypeId, k) == nil {
			missing[k] = v
		}
	}
	if len(missing) == 0 {
		return nil
	}
	_, err = sp.Properties().Set(ctx, rootId, miniapp.TypeId, missing)
	return err
}

// healOptions adds the option KEYS a catalog `choice` property gained
// after the install to the installed definition — additively: a key
// the definition lacks is written with its catalog leaves, an existing
// key is never renamed, recoloured, reordered or removed (those are the
// space's own once installed). Adopt never patches a descriptor on its
// own, so without this a stage added to a later catalog release would
// never reach an existing space. Writers only; a reader's setup leaves
// the definition as it is.
func healOptions(ctx context.Context, sp space.Space, rootId string, drafts []space.PropertyDraft, installed []space.PropertyDef) error {
	switch sp.Info().OwnRole {
	case space.PermissionOwner, space.PermissionAdmin, space.PermissionWriter:
	default:
		return nil
	}
	byXKey := make(map[string]*space.PropertyDef, len(installed))
	for i := range installed {
		if installed[i].XKey != "" {
			byXKey[installed[i].XKey] = &installed[i]
		}
	}
	for i := range drafts {
		want, _ := drafts[i].XFormat[xfOptions].(map[string]any)
		if len(want) == 0 {
			continue
		}
		def := byXKey[drafts[i].XKey]
		if def == nil {
			continue // absent by handle: the SDK's heal writes it whole
		}
		have, _ := def.XFormat[xfOptions].(map[string]any)
		patch := space.PropertyPatch{Set: map[string]any{}}
		for key, entry := range want {
			if _, present := have[key]; present {
				continue
			}
			leaves, _ := entry.(map[string]any)
			for leaf, v := range leaves {
				raw, err := json.Marshal(v)
				if err != nil {
					continue
				}
				storagePath, code, _ := patchPathToStorage(wireXFormat+"."+xfOptions+"."+key+"."+leaf, true)
				if code != "" {
					continue // not a settable leaf (meta and the like): the catalog gate admitted it, the patch grammar does not
				}
				val, vcode, _ := patchSetValue(storagePath, raw)
				if vcode != "" {
					continue
				}
				patch.Set[storagePath] = val
			}
		}
		if len(patch.Set) == 0 {
			continue
		}
		if err := sp.Types().PatchProperty(ctx, rootId, def.Id, patch); err != nil {
			return err
		}
	}
	return nil
}
