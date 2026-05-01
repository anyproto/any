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
func (d *deps) typeCreate(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	var req api.TypesCreateRequest
	if err := c.Bind(&req); err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
	}

	typeId, err := sp.Types().Create(c.Request().Context(), space.TypeCreateParams{
		Name:        req.Name,
		Description: req.Description,
		IconCID:     req.IconCID,
	})
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusCreated, api.TypesCreateResponse{TypeId: typeId})
}

// typeAddProperty handles POST /v1/spaces/:spaceId/types/:typeId/properties.
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

	propId, err := sp.Types().AddProperty(c.Request().Context(), typeId, space.PropertyDraft{
		Name:        req.Name,
		Description: req.Description,
		XKey:        req.XKey,
		Kind:        kind,
	})
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "typeId": typeId})
	}
	return c.JSON(http.StatusCreated, api.AddPropertyResponse{PropId: propId})
}

// typeList handles GET /v1/spaces/:spaceId/types. The virtual `nav`
// built-in is injected — the SDK doesn't know about it, but every
// space has it conceptually since objectCreate auto-stamps nav rows.
func (d *deps) typeList(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	infos, err := sp.Types().List(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	out := make([]api.TypeInfo, 0, len(infos)+1)
	for _, t := range infos {
		out = append(out, typeInfoToAPI(t))
	}
	out = append(out, nav.TypeInfo())
	return c.JSON(http.StatusOK, api.TypesListResponse{Types: out})
}

// typeGet handles GET /v1/spaces/:spaceId/types/:typeId. Maps
// space.ErrNotFound to 404 sdk.not_found.
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
// For typeId == "any", the SDK returns the hardcoded built-in props;
// for user types, the live `defs` dataset.
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
	return api.TypeInfo{
		Id:          t.Id,
		Name:        t.Name,
		Description: t.Description,
		IconCID:     t.IconCID,
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
