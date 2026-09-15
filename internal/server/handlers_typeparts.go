package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// Parts on user types (SDK TypesAPI part CRUD). A part is a display
// unit of a type owning one or more datasets — each served by a module
// (`records`, `editor`, `chat`). The key is pinned; the display slice
// patches; datasets are added under the part and evolve through the
// dataset routes (handlers_typedatasets.go).

// Bounds on a part declaration.
const (
	maxPartKeyBytes = 64
	maxPartUses     = 32
	maxPartUIBytes  = 16 * 1024
)

// typeParts handles GET /v1/spaces/:spaceId/types/:typeId/parts.
//
//	@Summary	List a type's parts with their datasets
//	@Tags		types
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Param		typeId	path		string	true	"Type ID"
//	@Success	200		{object}	api.TypePartsListResponse
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/parts [get]
func (d *deps) typeParts(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	if typeId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId required", nil)
	}
	if errResp, done := requireType(c, sp, typeId); done {
		return errResp
	}
	parts, err := sp.Types().Parts(c.Request().Context(), typeId)
	if err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId})
	}
	out := make([]api.PartDefResponse, 0, len(parts))
	for _, p := range parts {
		out = append(out, partDefToAPI(p))
	}
	return c.JSON(http.StatusOK, api.TypePartsListResponse{Parts: out})
}

// typeAddPart handles POST /v1/spaces/:spaceId/types/:typeId/parts —
// the part and its datasets land in one change.
//
//	@Summary	Declare a part (with its datasets) on a type
//	@Tags		types
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string					true	"Space ID"
//	@Param		typeId	path		string					true	"Type ID"
//	@Param		body	body		api.PartDraftRequest	true	"Part draft"
//	@Success	201		{object}	api.AddPartResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/parts [post]
func (d *deps) typeAddPart(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	if typeId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId required", nil)
	}
	req, ok := bindBodyStrict[api.PartDraftRequest](c, "")
	if !ok {
		return nil
	}
	draft, code, reason, details := partDraftFromAPI(*req)
	if code != "" {
		return writeError(c, http.StatusBadRequest, code, reason, details)
	}
	// Existence preflight: the SDK writes to whatever object :typeId
	// names, so without it a non-type objectId gets a 201 and a
	// declaration nothing can read back.
	if errResp, done := requireType(c, sp, typeId); done {
		return errResp
	}
	partId, err := sp.Types().AddPart(c.Request().Context(), typeId, draft)
	if err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId, "key": draft.Key})
	}
	return c.JSON(http.StatusCreated, api.AddPartResponse{PartId: partId})
}

// partMutablePaths are the wire (== storage) paths PATCH accepts on a
// part. `ui` is replaced whole (an object); `uses` is an array of
// dataset keys; `hidden` a boolean; the rest strings.
var partMutablePaths = map[string]struct{}{
	"name": {}, "icon": {}, "pos": {}, "hidden": {}, "ui": {}, "uses": {},
}

const partMutableHint = "path is pinned; mutable paths: name, icon, pos, hidden, ui, uses"

// typePatchPart handles PATCH /v1/spaces/:spaceId/types/:typeId/parts/:partId.
//
//	@Summary	Patch a part's display slice
//	@Tags		types
//	@Accept		json
//	@Param		spaceId	path	string					true	"Space ID"
//	@Param		typeId	path	string					true	"Type ID"
//	@Param		partId	path	string					true	"Part ID"
//	@Param		body	body	api.PartPatchRequest	true	"set/unset paths"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/parts/{partId} [patch]
func (d *deps) typePatchPart(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	partId := c.Param("partId")
	if typeId == "" || partId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId and partId required", nil)
	}
	req, ok := bindBodyStrict[api.PartPatchRequest](c, "")
	if !ok {
		return nil
	}
	if len(req.Set) == 0 && len(req.Unset) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one of set/unset is required", nil)
	}
	patch := space.DatasetDefPatch{}
	if len(req.Set) > 0 {
		patch.Set = make(map[string]any, len(req.Set))
	}
	for path, raw := range req.Set {
		if _, mutable := partMutablePaths[path]; !mutable {
			return writeError(c, http.StatusBadRequest, "dataset.immutable", partMutableHint, map[string]any{"path": path})
		}
		val, code, reason := partPatchValue(path, raw)
		if code != "" {
			return writeError(c, http.StatusBadRequest, code, reason, map[string]any{"path": path})
		}
		patch.Set[path] = val
	}
	for _, path := range req.Unset {
		if _, mutable := partMutablePaths[path]; !mutable {
			return writeError(c, http.StatusBadRequest, "dataset.immutable", partMutableHint, map[string]any{"path": path})
		}
		patch.Unset = append(patch.Unset, path)
	}
	if errResp, done := requireType(c, sp, typeId); done {
		return errResp
	}
	if err := sp.Types().PatchPart(c.Request().Context(), typeId, partId, patch); err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId, "partId": partId})
	}
	return c.NoContent(http.StatusNoContent)
}

// partPatchValue decodes one PATCH value by path: ui an object (the
// widget descriptor, replaced whole), uses an array of dataset keys,
// hidden a boolean, everything else a string.
func partPatchValue(path string, raw json.RawMessage) (any, string, string) {
	switch path {
	case "ui":
		ui, code, reason := partUIFromWire(raw)
		if code != "" {
			return nil, code, reason
		}
		return ui, "", ""
	case "uses":
		var uses []string
		if err := json.Unmarshal(raw, &uses); err != nil {
			return nil, "request.invalid_field", "uses must be an array of dataset keys"
		}
		if len(uses) > maxPartUses {
			return nil, "request.invalid_field", "too many uses"
		}
		for _, u := range uses {
			if u == "" || len(u) > maxPartKeyBytes {
				return nil, "request.invalid_field", "uses entries must be dataset keys"
			}
		}
		return uses, "", ""
	case "hidden":
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, "request.invalid_field", "hidden must be a boolean"
		}
		return b, "", ""
	default:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, "request.invalid_field", "value must be a JSON string"
		}
		return s, "", ""
	}
}

// partUIFromWire validates the widget descriptor: an object whose
// `type` (when present) is a slug string and whose optional `config` is
// an object — the x-format shape. The slug vocabulary is open.
func partUIFromWire(raw json.RawMessage) (map[string]any, string, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, "", ""
	}
	if len(raw) > maxPartUIBytes {
		return nil, "request.invalid_field", "ui too large"
	}
	var ui map[string]any
	if err := json.Unmarshal(raw, &ui); err != nil {
		return nil, "request.invalid_field", "ui must be an object of the form {type, config?}"
	}
	if t, present := ui["type"]; present {
		slug, ok := t.(string)
		if !ok || slug == "" || len(slug) > maxPartKeyBytes {
			return nil, "request.invalid_field", "ui.type must be a non-empty slug"
		}
	}
	if cfg, present := ui["config"]; present {
		if _, ok := cfg.(map[string]any); !ok {
			return nil, "request.invalid_field", "ui.config must be an object"
		}
	}
	return ui, "", ""
}

// typeRemovePart handles DELETE /v1/spaces/:spaceId/types/:typeId/parts/:partId.
// Tombstones the part and every dataset declared under it; record data
// is NOT cleaned up (the property-removal stance).
//
//	@Summary	Remove a part and its datasets
//	@Tags		types
//	@Param		spaceId	path	string	true	"Space ID"
//	@Param		typeId	path	string	true	"Type ID"
//	@Param		partId	path	string	true	"Part ID"
//	@Success	204
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/parts/{partId} [delete]
func (d *deps) typeRemovePart(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	partId := c.Param("partId")
	if typeId == "" || partId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId and partId required", nil)
	}
	if errResp, done := requireType(c, sp, typeId); done {
		return errResp
	}
	if err := sp.Types().RemovePart(c.Request().Context(), typeId, partId); err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId, "partId": partId})
	}
	return c.NoContent(http.StatusNoContent)
}

// partDraftFromAPI converts the wire draft to space.PartDraft. Returns
// ("", "", nil) code/reason/details on success.
func partDraftFromAPI(req api.PartDraftRequest) (space.PartDraft, string, string, map[string]any) {
	return partDraftFrom(req, datasetDraftFromAPI)
}

// systemPartDraftFromAPI is partDraftFromAPI for the server's own
// catalog installs: a part may name a reserved module.
func systemPartDraftFromAPI(req api.PartDraftRequest) (space.PartDraft, string, string, map[string]any) {
	return partDraftFrom(req, systemDatasetDraftFromAPI)
}

func partDraftFrom(req api.PartDraftRequest, dataset func(api.DatasetDraftRequest) (space.DatasetDraft, string, string)) (space.PartDraft, string, string, map[string]any) {
	draft := space.PartDraft{
		Key:    req.Key,
		Name:   req.Name,
		Icon:   req.Icon,
		Pos:    req.Pos,
		Hidden: req.Hidden,
		Uses:   req.Uses,
	}
	if req.Key == "" {
		return draft, "request.missing_field", "key required", nil
	}
	if len(req.Key) > maxPartKeyBytes {
		return draft, "request.invalid_field", "key too long", map[string]any{"key": req.Key}
	}
	if len(req.Uses) > maxPartUses {
		return draft, "request.invalid_field", "too many uses", map[string]any{"key": req.Key}
	}
	ui, code, reason := partUIFromWire(req.UI)
	if code != "" {
		return draft, code, reason, map[string]any{"key": req.Key}
	}
	draft.UI = ui
	for i, ds := range req.Datasets {
		dsDraft, code, reason := dataset(ds)
		if code != "" {
			return draft, code, reason, map[string]any{"key": req.Key, "dataset": i}
		}
		draft.Datasets = append(draft.Datasets, dsDraft)
	}
	return draft, "", "", nil
}

func partDefToAPI(p space.PartDef) api.PartDefResponse {
	out := api.PartDefResponse{
		Id:       p.Id,
		Key:      p.Key,
		Name:     p.Name,
		Icon:     p.Icon,
		Pos:      p.Pos,
		Hidden:   p.Hidden,
		Uses:     p.Uses,
		Datasets: make([]api.DatasetDefResponse, 0, len(p.Datasets)),
	}
	if len(p.UI) > 0 {
		if raw, err := json.Marshal(p.UI); err == nil {
			out.UI = raw
		}
	}
	for _, ds := range p.Datasets {
		out.Datasets = append(out.Datasets, datasetDefToAPI(ds))
	}
	return out
}

// typePatch handles PATCH /v1/spaces/:spaceId/types/:typeId — the
// display and rendering metadata of a user type: name, description,
// iconCid, weight, layout. Absent fields keep their value; an empty
// string clears a text field; `"layout": null` clears the layout.
//
//	@Summary	Patch a type's display and rendering metadata
//	@Tags		types
//	@Accept		json
//	@Param		spaceId	path	string					true	"Space ID"
//	@Param		typeId	path	string					true	"Type ID"
//	@Param		body	body	api.TypePatchRequest	true	"Fields to change"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId} [patch]
func (d *deps) typePatch(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	if typeId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId required", nil)
	}
	req, ok := bindBodyStrict[api.TypePatchRequest](c, "")
	if !ok {
		return nil
	}
	patch := space.TypePatch{
		Name:        req.Name,
		Description: req.Description,
		IconCID:     req.IconCID,
		Hidden:      req.Hidden,
	}
	if len(req.Meta) > 0 {
		patch.Meta = make(map[string]any, len(req.Meta))
		for k, v := range req.Meta {
			if code, reason := checkTypeMetaEntry(k, v); code != "" {
				return writeError(c, http.StatusBadRequest, code, reason, map[string]any{"path": "meta." + k})
			}
			patch.Meta[k] = v // nil = unset
		}
	}
	if len(req.Layout) > 0 {
		if string(req.Layout) == "null" {
			patch.ClearLayout = true
		} else {
			layout, code, reason := layoutFromWire(req.Layout)
			if code != "" {
				return writeError(c, http.StatusBadRequest, code, reason, map[string]any{"path": "layout"})
			}
			patch.Layout = layout
		}
	}
	if patch.Name == nil && patch.Description == nil && patch.IconCID == nil &&
		patch.Layout == nil && !patch.ClearLayout && patch.Hidden == nil && len(patch.Meta) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one of name, description, iconCid, layout, hidden, meta is required", nil)
	}
	if errResp, done := requireType(c, sp, typeId); done {
		return errResp
	}
	if err := sp.Types().Patch(c.Request().Context(), typeId, patch); err != nil {
		if errors.Is(err, space.ErrTypeRegistered) {
			return writeError(c, http.StatusBadRequest, "type.registered",
				"type is a registered built-in; its metadata is statically declared", map[string]any{"typeId": typeId})
		}
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "typeId": typeId})
	}
	return c.NoContent(http.StatusNoContent)
}

// checkTypeMetaEntry validates one meta entry: a single-level key
// (no '.', no '$', ≤64 bytes) and a scalar value — string, bool,
// number, or nil (an unset on PATCH). Returns ("", "") when fine.
func checkTypeMetaEntry(key string, v any) (code, reason string) {
	if key == "" || len(key) > 64 || strings.ContainsAny(key, ".$") {
		return "request.invalid_field", "meta keys are single-level: no '.', no '$', at most 64 bytes"
	}
	switch v.(type) {
	case nil, string, bool, float64, int, int64:
		return "", ""
	}
	return "request.invalid_field", "meta values are strings, booleans or numbers"
}

// layoutFromWire validates a type's layout descriptor: an object whose
// `type` is a slug string and whose optional `config` is an object (the
// x-format shape; v1 slugs page, tabs, chat, profile — open set).
func layoutFromWire(raw json.RawMessage) (map[string]any, string, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, "", ""
	}
	if len(raw) > maxPartUIBytes {
		return nil, "request.invalid_field", "layout too large"
	}
	var layout map[string]any
	if err := json.Unmarshal(raw, &layout); err != nil {
		return nil, "request.invalid_field", "layout must be an object of the form {type, config?}"
	}
	slug, ok := layout["type"].(string)
	if !ok || slug == "" || len(slug) > maxPartKeyBytes || strings.ContainsAny(slug, " .") {
		return nil, "request.invalid_field", "layout.type must be a non-empty slug"
	}
	if cfg, present := layout["config"]; present {
		if _, ok := cfg.(map[string]any); !ok {
			return nil, "request.invalid_field", "layout.config must be an object"
		}
	}
	return layout, "", ""
}
