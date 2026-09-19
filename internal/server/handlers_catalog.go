package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/anyproto/any-store/v2/query"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bundles"
	"github.com/anyproto/any/internal/catalog"
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
// Which bundles the walk covers depends on the space: a bundle another
// one supersedes is walked only where it is installed, and the bundles
// superseding it only where it is not (see supersededInstalled). A
// collection's declared meta is written where the definition lacks a
// key, after an install as well as on adopt.
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

	have, err := d.supersededInstalled(ctx, sp, order)
	if err != nil {
		return bundleErrorWith(c, err, map[string]any{"spaceId": sp.Id(), "usecaseId": usecaseId})
	}
	var (
		installs []bundles.Install
		byId     = map[string]*compiledBundle{}
	)
	for _, cu := range order {
		for i := range cu.bundles {
			cb := &cu.bundles[i]
			if !walksBundle(cb, have) {
				continue
			}
			installs = append(installs, cb.install)
			byId[cb.install.Id] = cb
		}
	}

	// Detached from the request, as the single-bundle ensure is: a
	// client that disconnects mid-walk must not leave a root nothing
	// references. Bounded by shutdown and the create timeout, which
	// here covers the whole walk. No checkBundleRoot: the only root
	// type a catalog install attaches is the registered `miniapp`,
	// whose values the compile step already gated.
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
		if r.Install.Declares() {
			if r.Install.Collection {
				row.CollectionId = r.Bundle.RootId
			} else {
				row.TypeId = r.Bundle.RootId
			}
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
			// The heals are idempotent and rerun at the next setup: a
			// failure is logged, never a 500 on a walk whose installs
			// all succeeded.
			if !r.Installed {
				if err := healOptions(ctx, sp, r.Bundle.RootId, r.Install.Properties, props); err != nil {
					handlerLog.Warn("catalog: option heal deferred",
						zap.String("spaceId", sp.Id()), zap.String("bundleId", r.Install.Id), zap.Error(err))
				}
			}
		}
		// On install as well as on adopt: the install carries no meta, so
		// a fresh root lacks every declared key.
		if len(cb.meta) > 0 {
			if err := healCollectionMeta(ctx, sp, r.Bundle.RootId, cb.meta); err != nil {
				handlerLog.Warn("catalog: collection meta heal deferred",
					zap.String("spaceId", sp.Id()), zap.String("bundleId", r.Install.Id), zap.Error(err))
			}
		}
		if cb.miniapp != nil {
			row.Miniapp = cb.miniapp
			if !r.Installed {
				if err := healMiniapp(ctx, sp, r.Bundle.RootId, cb.miniapp); err != nil {
					handlerLog.Warn("catalog: miniapp heal deferred",
						zap.String("spaceId", sp.Id()), zap.String("bundleId", r.Install.Id), zap.Error(err))
				}
			}
		}
		out.Bundles = append(out.Bundles, row)
	}
	return c.JSON(http.StatusOK, out)
}

// walksBundle reports whether setup ensures the bundle in a space whose
// installed bundles are `have`. A bundle no `supersedes` edge touches
// is always walked. Within a supersede group the space is on the old
// shape while any bundle of the old set is installed: setup then walks
// the old set — a deleted old root is minted again like any bundle —
// and skips the new set. Otherwise it walks the new set and skips the
// old, which never reaches a space on the new shape.
func walksBundle(cb *compiledBundle, have map[string]bool) bool {
	if cb.group == nil {
		return true
	}
	return cb.superseded == cb.group.OldKept(have)
}

// supersededInstalled reads which bundles the space has installed, for
// the supersede groups of the walk. A group is settled when the space
// is on its old shape (any old bundle present) or already on the new
// one (any new bundle present). Otherwise "not installed" is only true of a converged
// registry: a member that reads an unsynced one would install the new
// set next to the bundle it stands in for, and the two share a handle.
// The wait is the install gate's own, and it fails the same way — the
// owner proceeds, any other member is refused.
func (d *deps) supersededInstalled(ctx context.Context, sp space.Space, order []*compiledUsecase) (map[string]bool, error) {
	var groups []*catalog.SupersedeGroup
	seen := map[*catalog.SupersedeGroup]bool{}
	for _, cu := range order {
		for i := range cu.bundles {
			if g := cu.bundles[i].group; g != nil && !seen[g] {
				seen[g] = true
				groups = append(groups, g)
			}
		}
	}
	if len(groups) == 0 {
		return nil, nil
	}
	resolver := d.bundleResolver()
	read := func() (map[string]bool, error) {
		rows, err := resolver.List(ctx, sp)
		if err != nil {
			return nil, err
		}
		have := make(map[string]bool, len(rows))
		for _, b := range rows {
			have[b.Id] = true
		}
		return have, nil
	}
	settled := func(have map[string]bool) bool {
		for _, g := range groups {
			if !g.OldKept(have) && !g.NewAny(have) {
				return false
			}
		}
		return true
	}
	have, err := read()
	if err != nil || settled(have) {
		return have, err
	}
	if !resolver.WaitConverged(ctx, sp) && sp.Info().OwnRole != space.PermissionOwner {
		return nil, bundles.ErrRegistryNotSynced
	}
	return read()
}

// healCollectionMeta writes the declared flags an installed collection
// lacks. Always a read first, on install too: a declaring install
// reports Installed from the SDK's registered bit, which an inbound
// install landing mid-write can leave naming someone else's winner,
// and that root may carry values the space set. Never overwrites a key
// the definition carries — a cleared one included (a PATCH clear keeps
// the key as ""). Writers only; a reader's setup leaves the definition
// as it is.
func healCollectionMeta(ctx context.Context, sp space.Space, rootId string, meta map[string]any) error {
	switch sp.Info().OwnRole {
	case space.PermissionOwner, space.PermissionAdmin, space.PermissionWriter:
	default:
		return nil
	}
	info, err := sp.Collections().Get(ctx, rootId)
	if err != nil {
		return err
	}
	missing := map[string]any{}
	for k, v := range meta {
		if _, present := info.Meta[k]; !present {
			missing[k] = v
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return sp.Collections().Patch(ctx, rootId, space.CollectionPatch{Meta: missing})
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
// Runs only when no live winner exists, so an adopted root — which
// carries the handle by design — never conflicts with itself; the
// space's registered built-ins were checked against the catalog at
// boot.
//
// A root of THIS bundle is never a conflict even when the registry
// reads it as uninstalled: a deleted winner leaves its losers listed
// as types (the registry says "install again", and the loser holds
// the handle), and an owner installing past an unconverged registry
// may already hold its own root's tree. Both are the registry's to
// settle, not a user type in the way. Readers are left to the SDK's
// write gate, so a member without write permission hears about the
// permission, not the handle.
func catalogBeforeInstall(ctx context.Context, sp space.Space, inst bundles.Install) error {
	if inst.XKey == "" {
		return nil
	}
	switch sp.Info().OwnRole {
	case space.PermissionOwner, space.PermissionAdmin, space.PermissionWriter:
	default:
		return nil
	}
	type handle struct{ id, xkey string }
	var handles []handle
	infos, err := sp.Types().List(ctx)
	if err != nil {
		return err
	}
	for _, t := range infos {
		handles = append(handles, handle{t.Id, t.XKey})
	}
	colls, err := sp.Collections().List(ctx)
	if err != nil {
		return err
	}
	for _, col := range colls {
		handles = append(handles, handle{col.Id, col.XKey})
	}
	var own map[string]bool
	for _, h := range handles {
		if h.xkey != inst.XKey && h.id != inst.XKey {
			continue
		}
		if own == nil {
			if own, err = bundleRoots(ctx, sp, inst.Id); err != nil {
				return err
			}
		}
		if own[h.id] {
			continue
		}
		return &xKeyConflictError{XKey: inst.XKey, ExistingTypeId: h.id}
	}
	return nil
}

// bundleRoots reads every root ever claimed for a bundle id straight
// off the registry row — the raw `roots` set, which the typed
// Bundles().Get hides behind its live-winner verdict.
func bundleRoots(ctx context.Context, sp space.Space, bundleId string) (map[string]bool, error) {
	row, err := sp.Query(sp.SpaceIndexObjectId(), "bundles").
		Filter(query.Key{Path: []string{"id"}, Filter: query.NewComp(query.CompOpEq, bundleId)}).
		One(ctx)
	if errors.Is(err, space.ErrNotFound) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	roots := map[string]bool{}
	for _, v := range row.GetArray("roots") {
		roots[string(v.GetStringBytes())] = true
	}
	if id := string(row.GetStringBytes("rootId")); id != "" {
		roots[id] = true
	}
	return roots, nil
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
	for _, t := range row.GetArray("any", "collections") {
		if string(t.GetStringBytes()) == miniapp.Id {
			carries = true
			break
		}
	}
	if !carries {
		if _, err := sp.Properties().AttachCollection(ctx, rootId, miniapp.Id); err != nil {
			return err
		}
	}
	missing := map[string]any{}
	for k, v := range values {
		if row.Get(miniapp.Id, k) == nil {
			missing[k] = v
		}
	}
	if len(missing) == 0 {
		return nil
	}
	_, err = sp.Properties().Set(ctx, rootId, miniapp.Id, missing)
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
				// `meta` is a container: one leaf per key under it.
				paths := map[string]any{wireXFormat + "." + xfOptions + "." + key + "." + leaf: v}
				if leaf == "meta" {
					paths = map[string]any{}
					for mk, mv := range asMap(v) {
						paths[wireXFormat+"."+xfOptions+"."+key+".meta."+mk] = mv
					}
				}
				for wirePath, lv := range paths {
					raw, err := json.Marshal(lv)
					if err != nil {
						continue
					}
					storagePath, code, _ := patchPathToStorage(wirePath, true)
					if code != "" {
						continue // the catalog gate admitted it; the patch grammar has no leaf for it
					}
					val, vcode, _ := patchSetValue(storagePath, raw)
					if vcode != "" {
						continue
					}
					patch.Set[storagePath] = val
				}
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

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
