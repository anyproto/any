package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/nav"
)

// typeCreate handles POST /v1/spaces/:spaceId/types.
//
//	@Summary	Create a type
//	@Tags		types
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string					true	"Space ID"
//	@Param		body	body		api.TypesCreateRequest	true	"Type params"
//	@Success	201		{object}	api.TypesCreateResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types [post]
func (d *deps) typeCreate(c echo.Context) error {
	var req api.TypesCreateRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}

	// xKey is the only human-supplied handle a type can be resolved by
	// (besides its CID) — the display name is NOT a resolution key. The SDK
	// treats xKey as non-unique display metadata, so uniqueness is enforced
	// here. Reject name-only creates and same-space collisions; otherwise a
	// type is reachable only by its content-addressed id (see docs/03-api.md
	// § Types).
	if req.XKey == "" {
		return writeError(c, http.StatusBadRequest, "type.xkey_required",
			"xKey is required: a type needs a stable programmatic handle (derive a slug from the name)", nil)
	}

	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	existing, err := sp.Types().List(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	for _, t := range existing {
		// Built-in types carry xKey "" but resolve by their literal Id
		// ("chat", "nav", …), so a new xKey must dodge both namespaces.
		if t.XKey == req.XKey || t.Id == req.XKey {
			return writeError(c, http.StatusConflict, "type.xkey_conflict",
				"xKey already in use by another type in this space",
				map[string]any{"xKey": req.XKey, "existingTypeId": t.Id})
		}
	}

	typeId, err := sp.Types().Create(c.Request().Context(), space.TypeCreateParams{
		Name:        req.Name,
		Description: req.Description,
		IconCID:     req.IconCID,
		XKey:        req.XKey,
	})
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusCreated, api.TypesCreateResponse{TypeId: typeId})
}

// typeAddProperty handles POST /v1/spaces/:spaceId/types/:typeId/properties.
//
//	@Summary	Add a property to a type
//	@Tags		types
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string					true	"Space ID"
//	@Param		typeId	path		string					true	"Type ID"
//	@Param		body	body		api.AddPropertyRequest	true	"Property draft"
//	@Success	201		{object}	api.AddPropertyResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/properties [post]
func (d *deps) typeAddProperty(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	if typeId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId required", nil)
	}

	var req api.AddPropertyRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}

	kind, ok := propertyKindFromString(req.Kind)
	if !ok {
		return writeError(c, http.StatusBadRequest, "request.schema",
			"unknown property kind",
			map[string]any{"kind": req.Kind})
	}

	var scope space.Scope // zero value = synced (SDK default)
	if req.Scope != "" {
		scope, ok = space.ParseScope(req.Scope)
		if !ok || scope == space.ScopeDerived {
			return writeError(c, http.StatusBadRequest, "request.schema",
				"scope must be one of synced, account, local",
				map[string]any{"scope": req.Scope})
		}
	}

	propId, err := sp.Types().AddProperty(c.Request().Context(), typeId, space.PropertyDraft{
		Name:        req.Name,
		Description: req.Description,
		XKey:        req.XKey,
		Kind:        kind,
		Meta:        req.Meta,
		Scope:       scope,
	})
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "typeId": typeId})
	}
	return c.JSON(http.StatusCreated, api.AddPropertyResponse{PropId: propId})
}

// typeList handles GET /v1/spaces/:spaceId/types.
//
//	@Summary	List types in a space
//	@Tags		types
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	200		{object}	api.TypesListResponse
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types [get]
func (d *deps) typeList(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	infos, err := sp.Types().List(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	// nav is registered with the SDK (config.Config.Types, see sdk.go) as a
	// property-only type, so Types().List already surfaces it with
	// BuiltIn=true — do NOT inject it again here or clients see "nav" twice.
	out := make([]api.TypeInfo, 0, len(infos))
	for _, t := range infos {
		out = append(out, typeInfoToAPI(t))
	}
	return c.JSON(http.StatusOK, api.TypesListResponse{Types: out})
}

// typeGet handles GET /v1/spaces/:spaceId/types/:typeId.
//
//	@Summary	Get a type
//	@Tags		types
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Param		typeId	path		string	true	"Type ID"
//	@Success	200		{object}	api.TypeInfo
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId} [get]
func (d *deps) typeGet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	if typeId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId required", nil)
	}
	if typeId == nav.TypeId {
		return c.JSON(http.StatusOK, nav.TypeInfo())
	}
	info, err := sp.Types().Get(c.Request().Context(), typeId)
	if err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return writeError(c, http.StatusNotFound, "sdk.not_found",
				"type not found",
				map[string]any{"spaceId": sp.Id(), "typeId": typeId})
		}
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "typeId": typeId})
	}
	return c.JSON(http.StatusOK, typeInfoToAPI(info))
}

// typeProperties handles GET /v1/spaces/:spaceId/types/:typeId/properties.
//
//	@Summary	List properties of a type
//	@Tags		types
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Param		typeId	path		string	true	"Type ID"
//	@Success	200		{object}	api.PropertiesListResponse
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/properties [get]
func (d *deps) typeProperties(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	if typeId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId required", nil)
	}
	if typeId == nav.TypeId {
		return c.JSON(http.StatusOK, api.PropertiesListResponse{Properties: nav.PropertyDefs()})
	}
	defs, err := sp.Types().Properties(c.Request().Context(), typeId)
	if err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return writeError(c, http.StatusNotFound, "sdk.not_found",
				"type not found",
				map[string]any{"spaceId": sp.Id(), "typeId": typeId})
		}
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "typeId": typeId})
	}
	out := make([]api.PropertyDef, 0, len(defs))
	for _, p := range defs {
		out = append(out, propertyDefToAPI(p))
	}
	return c.JSON(http.StatusOK, api.PropertiesListResponse{Properties: out})
}

func typeInfoToAPI(t space.TypeInfo) api.TypeInfo {
	// Builtin/registered types have clean literal ids (program, chat, …) and no
	// caller-set xKey; report xKey=id so every type has a stable programmatic
	// handle (user types carry the xKey set at create / derived from name).
	xkey := t.XKey
	if xkey == "" && t.BuiltIn {
		xkey = t.Id
	}
	return api.TypeInfo{
		Id:          t.Id,
		Name:        t.Name,
		Description: t.Description,
		IconCID:     t.IconCID,
		XKey:        xkey,
		BuiltIn:     t.BuiltIn,
	}
}

func propertyDefToAPI(p space.PropertyDef) api.PropertyDef {
	out := api.PropertyDef{
		Id:          p.Id,
		Name:        p.Name,
		Description: p.Description,
		XKey:        p.XKey,
		XKind:       p.XKind,
		Kind:        propertyKindToString(p.Kind),
		Meta:        p.Meta,
	}
	if p.Scope != 0 {
		out.Scope = p.Scope.String()
	}
	if p.Items != nil {
		nested := propertyDefToAPI(*p.Items)
		out.Items = &nested
	}
	if len(p.Properties) > 0 {
		out.Properties = make([]api.PropertyDef, 0, len(p.Properties))
		for _, np := range p.Properties {
			out.Properties = append(out.Properties, propertyDefToAPI(np))
		}
	}
	if len(p.Required) > 0 {
		out.Required = append([]string(nil), p.Required...)
	}
	return out
}

func propertyKindToString(k space.PropertyKind) string {
	switch k {
	case space.PropertyKindString:
		return api.PropertyKindString
	case space.PropertyKindNumber:
		return api.PropertyKindNumber
	case space.PropertyKindBoolean:
		return api.PropertyKindBoolean
	case space.PropertyKindNull:
		return api.PropertyKindNull
	case space.PropertyKindArray:
		return api.PropertyKindArray
	case space.PropertyKindObject:
		return api.PropertyKindObject
	default:
		return ""
	}
}

func propertyKindFromString(s string) (space.PropertyKind, bool) {
	switch s {
	case api.PropertyKindString:
		return space.PropertyKindString, true
	case api.PropertyKindNumber:
		return space.PropertyKindNumber, true
	case api.PropertyKindBoolean:
		return space.PropertyKindBoolean, true
	case api.PropertyKindNull:
		return space.PropertyKindNull, true
	case api.PropertyKindArray:
		return space.PropertyKindArray, true
	case api.PropertyKindObject:
		return space.PropertyKindObject, true
	default:
		return 0, false
	}
}
