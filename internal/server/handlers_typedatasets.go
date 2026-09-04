package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/handler"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
)

// Runtime dataset schemas on user types (SDK TypesAPI dataset CRUD).
// A declaration alone gives a dataset enforced semantics — required
// fields, write-once vs author-mutable fields, author-only delete,
// derived creator/time stamps, user-supplied record ids — applied by
// the SDK's generic schema handler on every peer as the definition
// syncs. Behavioral parts are pinned first-write; display parts patch.

// typeDatasets handles GET /v1/spaces/:spaceId/types/:typeId/datasets.
//
//	@Summary	List a type's runtime dataset definitions
//	@Tags		types
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Param		typeId	path		string	true	"Type ID"
//	@Success	200		{object}	api.TypeDatasetsListResponse
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/datasets [get]
//
// reservedIndexDatasetName reports whether name collides with the
// search indexer's virtual chunker doc-id namespaces — reserved on
// every dataset-declaration path (types route and bundle ensure).
func reservedIndexDatasetName(name string) bool {
	return name == index.DatasetProp || name == index.DatasetSchemaVirtual
}

func (d *deps) typeDatasets(c echo.Context) error {
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
	defs, err := sp.Types().Datasets(c.Request().Context(), typeId)
	if err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId})
	}
	out := make([]api.DatasetDefResponse, 0, len(defs))
	for _, def := range defs {
		out = append(out, datasetDefToAPI(def))
	}
	return c.JSON(http.StatusOK, api.TypeDatasetsListResponse{Datasets: out})
}

// typeAddDataset handles POST /v1/spaces/:spaceId/types/:typeId/datasets.
//
//	@Summary	Define a dataset on a type
//	@Tags		types
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string					true	"Space ID"
//	@Param		typeId	path		string					true	"Type ID"
//	@Param		body	body		api.DatasetDraftRequest	true	"Dataset draft"
//	@Success	201		{object}	api.AddDatasetResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/datasets [post]
func (d *deps) typeAddDataset(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	if typeId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId required", nil)
	}
	req, ok := bindBodyStrict[api.DatasetDraftRequest](c, "")
	if !ok {
		return nil
	}
	// Existence preflight: the SDK writes to whatever object :typeId
	// names, so without it a non-type objectId gets a 201 and a
	// definition nothing can read back.
	if errResp, done := requireType(c, sp, typeId); done {
		return errResp
	}
	if req.Name == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "name required", nil)
	}
	// The index store keys documents objectId:<dataset>:<recordId>
	// under the search indexer's virtual chunker names — a user dataset
	// claiming one would collide with their doc-id namespaces.
	if reservedIndexDatasetName(req.Name) {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"dataset name is reserved by the search indexer",
			map[string]any{"name": req.Name})
	}
	// Name-conflict preflight against everything this space already
	// hosts (built-ins, handler datasets, other runtime definitions) —
	// the SDK rejects these too but without an errors.Is-able sentinel.
	for _, ds := range sp.Datasets() {
		if ds.Name == req.Name {
			return writeError(c, http.StatusConflict, "dataset.name_conflict",
				"dataset name already in use in this space",
				map[string]any{"name": req.Name, "typeId": ds.TypeId})
		}
	}

	draft, code, reason := datasetDraftFromAPI(*req)
	if code != "" {
		return writeError(c, http.StatusBadRequest, code, reason, nil)
	}
	defId, err := sp.Types().AddDataset(c.Request().Context(), typeId, draft)
	if err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId})
	}
	return c.JSON(http.StatusCreated, api.AddDatasetResponse{DatasetDefId: defId})
}

// typeAddDatasetField handles POST /v1/spaces/:spaceId/types/:typeId/datasets/:defId/fields.
//
//	@Summary	Add a field to a dataset definition (additive evolution)
//	@Tags		types
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string					true	"Space ID"
//	@Param		typeId	path		string					true	"Type ID"
//	@Param		defId	path		string					true	"Dataset definition ID"
//	@Param		body	body		api.DatasetFieldDraft	true	"Field draft"
//	@Success	201		{object}	api.AddDatasetFieldResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/datasets/{defId}/fields [post]
func (d *deps) typeAddDatasetField(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	defId := c.Param("defId")
	if typeId == "" || defId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId and defId required", nil)
	}
	req, ok := bindBodyStrict[api.DatasetFieldDraft](c, "")
	if !ok {
		return nil
	}
	fieldDraft, code, reason := datasetFieldDraftFromAPI(*req)
	if code != "" {
		return writeError(c, http.StatusBadRequest, code, reason, map[string]any{"key": req.Key})
	}
	if errResp, done := requireType(c, sp, typeId); done {
		return errResp
	}
	fieldId, err := sp.Types().AddDatasetField(c.Request().Context(), typeId, defId, fieldDraft)
	if err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId, "defId": defId})
	}
	return c.JSON(http.StatusCreated, api.AddDatasetFieldResponse{FieldDefId: fieldId})
}

// typePatchDatasetField handles PATCH /v1/spaces/:spaceId/types/:typeId/datasets/:defId/fields/:fieldId.
// Mutable paths: name, description, and every path under xFormat (the
// property PATCH rules — a set targets a leaf, containers are
// unset-only). The behavioral declaration is pinned → 400
// dataset.immutable.
//
//	@Summary	Patch a dataset field's display fields and descriptor
//	@Tags		types
//	@Accept		json
//	@Param		spaceId	path	string							true	"Space ID"
//	@Param		typeId	path	string							true	"Type ID"
//	@Param		defId	path	string							true	"Dataset definition ID"
//	@Param		fieldId	path	string							true	"Field definition ID"
//	@Param		body	body	api.DatasetFieldPatchRequest	true	"set/unset paths"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/datasets/{defId}/fields/{fieldId} [patch]
func (d *deps) typePatchDatasetField(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	defId := c.Param("defId")
	fieldId := c.Param("fieldId")
	if typeId == "" || defId == "" || fieldId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId, defId and fieldId required", nil)
	}
	req, ok := bindBodyStrict[api.DatasetFieldPatchRequest](c, "")
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
	var newSlug string
	for path, raw := range req.Set {
		storagePath, code, reason := fieldPatchPathToStorage(path, true)
		if code != "" {
			return writeError(c, http.StatusBadRequest, code, reason, map[string]any{"path": path})
		}
		val, vcode, reason := patchSetValue(storagePath, raw)
		if vcode != "" {
			return writeError(c, http.StatusBadRequest, vcode, reason, map[string]any{"path": path})
		}
		patch.Set[storagePath] = val
		if storagePath == propFieldXFormat+"."+xfType {
			newSlug, _ = val.(string)
		}
	}
	for _, path := range req.Unset {
		storagePath, code, reason := fieldPatchPathToStorage(path, false)
		if code != "" {
			return writeError(c, http.StatusBadRequest, code, reason, map[string]any{"path": path})
		}
		patch.Unset = append(patch.Unset, storagePath)
	}

	// Existence preflight — the field must belong to THIS definition
	// (the SDK checks the type only) — and the slug ↔ kind rule when the
	// slug moves.
	def, errResp, done := findDatasetDef(c, d, sp, typeId, defId)
	if done {
		return errResp
	}
	var field *space.DatasetFieldDef
	for i := range def.Fields {
		if def.Fields[i].Id == fieldId {
			field = &def.Fields[i]
		}
	}
	if field == nil {
		return writeError(c, http.StatusNotFound, "sdk.not_found",
			"field definition not found on this dataset",
			map[string]any{"typeId": typeId, "defId": defId, "fieldId": fieldId})
	}
	if newSlug != "" {
		if reason := slugKindMismatch(newSlug, propertyKindToString(field.Kind)); reason != "" {
			return writeError(c, http.StatusBadRequest, "property.format_invalid", reason,
				map[string]any{"path": wireXFormat + "." + xfType})
		}
	}
	if err := sp.Types().PatchDatasetField(c.Request().Context(), typeId, fieldId, patch); err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId, "defId": defId, "fieldId": fieldId})
	}
	return c.NoContent(http.StatusNoContent)
}

// datasetDefMutablePaths are the wire (== storage) paths PATCH accepts;
// every mutable leaf is a plain string except search.text, which is
// string-or-array (parseSearchTextLeaf). Everything else on a dataset
// definition is pinned — remove and re-add to change it. The wire
// `name` (the collection name) is NOT here: it lives under the pinned
// storage field `collection`, and the head record's storage `name`
// slot is a field-record display label nothing reads back — accepting
// it would be a silent no-op.
var datasetDefMutablePaths = map[string]struct{}{
	"description":  {},
	"displayName":  {},
	"search.title": {},
	"search.text":  {},
	"search.scope": {},
}

const datasetDefMutableHint = "path is pinned; mutable paths: description, displayName, search.title, search.text, search.scope"

// typePatchDataset handles PATCH /v1/spaces/:spaceId/types/:typeId/datasets/:defId.
// Mutable paths: description, displayName, search.title, search.text,
// search.scope. Pinned paths return 400 dataset.immutable.
//
//	@Summary	Patch a dataset definition's display fields
//	@Tags		types
//	@Accept		json
//	@Param		spaceId	path	string					true	"Space ID"
//	@Param		typeId	path	string					true	"Type ID"
//	@Param		defId	path	string					true	"Dataset definition ID"
//	@Param		body	body	api.DatasetPatchRequest	true	"set/unset paths"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/datasets/{defId} [patch]
func (d *deps) typePatchDataset(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	defId := c.Param("defId")
	if typeId == "" || defId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId and defId required", nil)
	}
	req, ok := bindBodyStrict[api.DatasetPatchRequest](c, "")
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
		if _, mutable := datasetDefMutablePaths[path]; !mutable {
			return writeError(c, http.StatusBadRequest, "dataset.immutable",
				datasetDefMutableHint, map[string]any{"path": path})
		}
		if path == "search.text" {
			val, code, reason := parseSearchTextLeaf(raw)
			if code != "" {
				return writeError(c, http.StatusBadRequest, code, reason, map[string]any{"path": path})
			}
			patch.Set[path] = val
			continue
		}
		var val string
		if err := json.Unmarshal(raw, &val); err != nil {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"value must be a JSON string", map[string]any{"path": path})
		}
		if path == "search.scope" && !index.ValidScope(val) {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"search.scope must be a slug (lowercase letters, digits, _ or -; max 64)",
				map[string]any{"path": path})
		}
		patch.Set[path] = val
	}
	for _, path := range req.Unset {
		if _, mutable := datasetDefMutablePaths[path]; !mutable {
			return writeError(c, http.StatusBadRequest, "dataset.immutable",
				datasetDefMutableHint, map[string]any{"path": path})
		}
		patch.Unset = append(patch.Unset, path)
	}

	// Existence preflight: the SDK's PatchDataset silently no-ops on an
	// unknown defId (the strict-modify miss rides a discarded
	// rejection), so without it a stale defId gets a 204 and nothing
	// applied.
	if errResp, done := requireDatasetDef(c, d, sp, typeId, defId); done {
		return errResp
	}
	if err := sp.Types().PatchDataset(c.Request().Context(), typeId, defId, patch); err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId, "defId": defId})
	}
	return c.NoContent(http.StatusNoContent)
}

// parseSearchTextLeaf parses PATCH's `search.text` value — the one
// string-or-array leaf. A bare string passes verbatim; an
// array must name at least one field key, none empty, no duplicates,
// and a single-element array canonicalizes to the bare string so the
// stored leaf keeps the scalar shape wherever possible. Returns
// ("", "") code/reason on success.
func parseSearchTextLeaf(raw json.RawMessage) (val any, code, reason string) {
	var text api.SearchText
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, "request.invalid_field", "search.text must be a string or an array of field keys"
	}
	if text == nil {
		// An empty string decodes to nil — stored verbatim as the
		// explicit clear the single-field form always allowed.
		return "", "", ""
	}
	if len(text) == 0 {
		return nil, "request.invalid_field", "search.text array must name at least one field key"
	}
	seen := make(map[string]struct{}, len(text))
	for _, k := range text {
		if k == "" {
			return nil, "request.invalid_field", "search.text has an empty field key"
		}
		if _, dup := seen[k]; dup {
			return nil, "request.invalid_field", "search.text names a field key twice"
		}
		seen[k] = struct{}{}
	}
	if len(text) == 1 {
		return text[0], "", ""
	}
	return []string(text), "", ""
}

// typeRemoveDataset handles DELETE /v1/spaces/:spaceId/types/:typeId/datasets/:defId.
// Tombstones the definition; existing record data is NOT cleaned up
// (the property-removal stance) — subsequent writes drop once peers
// apply the removal.
//
//	@Summary	Remove a dataset definition
//	@Tags		types
//	@Param		spaceId	path	string	true	"Space ID"
//	@Param		typeId	path	string	true	"Type ID"
//	@Param		defId	path	string	true	"Dataset definition ID"
//	@Success	204
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/datasets/{defId} [delete]
func (d *deps) typeRemoveDataset(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	defId := c.Param("defId")
	if typeId == "" || defId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId and defId required", nil)
	}
	// Existence preflight: the SDK's RemoveDataset would otherwise mint
	// a synced tombstone + removal row for an id that never existed and
	// answer 204.
	if errResp, done := requireDatasetDef(c, d, sp, typeId, defId); done {
		return errResp
	}
	if err := sp.Types().RemoveDataset(c.Request().Context(), typeId, defId); err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId, "defId": defId})
	}
	return c.NoContent(http.StatusNoContent)
}

// typeRemoveDatasetField handles DELETE /v1/spaces/:spaceId/types/:typeId/datasets/:defId/fields/:fieldId.
// The SDK keys field definitions by (typeId, fieldId); defId rides the
// URI for hierarchy only. Existing values stay stored; subsequent
// writes to the field are rejected as undeclared (non-dynamic datasets).
//
//	@Summary	Remove a dataset field definition
//	@Tags		types
//	@Param		spaceId	path	string	true	"Space ID"
//	@Param		typeId	path	string	true	"Type ID"
//	@Param		defId	path	string	true	"Dataset definition ID"
//	@Param		fieldId	path	string	true	"Field definition ID"
//	@Success	204
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/types/{typeId}/datasets/{defId}/fields/{fieldId} [delete]
func (d *deps) typeRemoveDatasetField(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	typeId := c.Param("typeId")
	fieldId := c.Param("fieldId")
	if typeId == "" || fieldId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "typeId and fieldId required", nil)
	}
	if errResp, done := requireType(c, sp, typeId); done {
		return errResp
	}
	if err := sp.Types().RemoveDatasetField(c.Request().Context(), typeId, fieldId); err != nil {
		return d.datasetWriteError(c, err, map[string]any{"typeId": typeId, "fieldId": fieldId})
	}
	return c.NoContent(http.StatusNoContent)
}

// requireType 404s when :typeId doesn't resolve to a type in the space
// (the SDK's dataset CRUD writes to whatever object the id names, so a
// bad id would otherwise succeed and be unreadable). done=true means
// the response was written.
func requireType(c echo.Context, sp space.Space, typeId string) (errResp error, done bool) {
	if _, err := sp.Types().Get(c.Request().Context(), typeId); err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return writeError(c, http.StatusNotFound, "type.not_found",
				"type not found",
				map[string]any{"spaceId": sp.Id(), "typeId": typeId}), true
		}
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "typeId": typeId}), true
	}
	return nil, false
}

// requireDatasetDef 404s when defId isn't a dataset definition on the
// type — the SDK's PatchDataset/RemoveDataset don't check existence
// themselves (a patch no-ops, a remove mints a tombstone for the
// garbage id). Subsumes requireType: an unknown type has no defs.
func requireDatasetDef(c echo.Context, d *deps, sp space.Space, typeId, defId string) (errResp error, done bool) {
	_, errResp, done = findDatasetDef(c, d, sp, typeId, defId)
	return errResp, done
}

// findDatasetDef is requireDatasetDef returning the compiled definition.
func findDatasetDef(c echo.Context, d *deps, sp space.Space, typeId, defId string) (def space.DatasetDef, errResp error, done bool) {
	if errResp, done := requireType(c, sp, typeId); done {
		return def, errResp, true
	}
	defs, err := sp.Types().Datasets(c.Request().Context(), typeId)
	if err != nil {
		return def, d.datasetWriteError(c, err, map[string]any{"typeId": typeId, "defId": defId}), true
	}
	for _, def := range defs {
		if def.Id == defId {
			return def, nil, false
		}
	}
	return def, writeError(c, http.StatusNotFound, "sdk.not_found",
		"dataset definition not found on this type",
		map[string]any{"typeId": typeId, "defId": defId}), true
}

// spaceUpsert handles POST /v1/spaces/:spaceId/upsert — schema-driven
// batch ingest into an id:user dataset (Space.Upsert). Partial success
// is 200 with rejections[] populated, same stance as /modify.
//
//	@Summary	Upsert records into an id:user dataset
//	@Tags		data
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string				true	"Space ID"
//	@Param		body	body		api.UpsertRequest	true	"Upsert batch"
//	@Success	200		{object}	api.UpsertResult
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/upsert [post]
func (d *deps) spaceUpsert(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	req, ok := bindBodyStrict[api.UpsertRequest](c, "")
	if !ok {
		return nil
	}
	if req.ObjectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}
	if req.Dataset == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "dataset required", nil)
	}
	if len(req.Records) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "records required", nil)
	}

	batch := space.UpsertBatch{
		ObjectId: req.ObjectId,
		Dataset:  req.Dataset,
		PageSize: req.PageSize,
		TraceIds: req.TraceIds,
		Records:  make([]space.UpsertRecord, 0, len(req.Records)),
	}
	for _, r := range req.Records {
		batch.Records = append(batch.Records, space.UpsertRecord{Id: r.Id, Fields: r.Fields})
	}

	res, err := sp.Upsert(c.Request().Context(), batch)
	if err != nil {
		details := map[string]any{"spaceId": sp.Id(), "objectId": req.ObjectId, "dataset": req.Dataset}
		if errors.Is(err, space.ErrUpsertRequiresUserIds) {
			return writeError(c, http.StatusBadRequest, "upsert.requires_user_ids",
				"upsert serves only datasets declared with id rule \"user\" — the record id is the idempotency key",
				details)
		}
		// dataset.unknown is mapped inside sdkOpError (unknownDatasetError).
		return sdkOpError(c, err, details)
	}
	return c.JSON(http.StatusOK, upsertResultToAPI(res))
}

func upsertResultToAPI(r space.UpsertResult) api.UpsertResult {
	out := api.UpsertResult{
		Pages:   make([]api.ModifyResult, 0, len(r.Pages)),
		Created: r.Created,
		Updated: r.Updated,
		Skipped: r.Skipped,
	}
	for _, p := range r.Pages {
		out.Pages = append(out.Pages, modifyResultToAPI(p))
	}
	for _, rej := range r.Rejections {
		code := "upsert.rejected"
		switch {
		case errors.Is(rej.Err, space.ErrImmutableFieldChanged):
			code = "upsert.immutable_field"
		case errors.Is(rej.Err, space.ErrUpsertNotAuthor):
			code = "upsert.not_author"
		case errors.Is(rej.Err, space.ErrRecordDeleted):
			code = "upsert.record_deleted"
		}
		out.Rejections = append(out.Rejections, api.UpsertRejection{
			Index:  rej.Index,
			Id:     rej.Id,
			Code:   code,
			Reason: sanitizeSDKMessage(rej.Err),
		})
	}
	return out
}

// datasetWriteError maps TypesAPI dataset-CRUD errors onto clean 4xx
// envelopes. Sentinels first; declaration errors are STOPGAP-matched on
// message text until the SDK exports errors.Is-able sentinels for them
// (the oneToOneError pattern). details carries the caller's ids
// (typeId, defId or fieldId).
func (d *deps) datasetWriteError(c echo.Context, err error, details map[string]any) error {
	msg := err.Error()
	switch {
	case errors.Is(err, space.ErrTypeRegistered):
		return writeError(c, http.StatusBadRequest, "type.registered",
			"type is a registered built-in; its datasets are statically declared", details)
	case errors.Is(err, space.ErrInvalidFieldValue):
		// A mutable path carrying a malformed value (e.g. a bad
		// search.text form the local pre-validation didn't cover) —
		// same code the handler's own leaf checks use.
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			sanitizeSDKMessage(err), details)
	case errors.Is(err, space.ErrPinnedField):
		return writeError(c, http.StatusBadRequest, "dataset.immutable", "a patched path is pinned", details)
	case errors.Is(err, space.ErrNotFound):
		return writeError(c, http.StatusNotFound, "sdk.not_found", "type or dataset definition not found", details)
	case errors.Is(err, handler.ErrValidation):
		return sdkValidationError(c, err, details)
	case strings.Contains(msg, "already registered"), strings.Contains(msg, "already defined on type"):
		return writeError(c, http.StatusConflict, "dataset.name_conflict",
			"dataset name already in use in this space", details)
	case strings.Contains(msg, "not found on type"):
		return writeError(c, http.StatusNotFound, "sdk.not_found", "dataset definition not found on this type", details)
	case strings.Contains(msg, "invalid dataset declaration"),
		strings.Contains(msg, "invalid dataset definition"),
		strings.Contains(msg, "cannot be required"),
		strings.Contains(msg, "already declares field"),
		strings.Contains(msg, "would invalidate dataset"),
		strings.Contains(msg, "Kind required"),
		strings.Contains(msg, "Kind and Shape.Kind disagree"):
		return writeError(c, http.StatusBadRequest, "dataset.decl_invalid",
			sanitizeSDKMessage(err), details)
	default:
		return sdkOpError(c, err, details)
	}
}

// sdkMessagePrefixes are the SDK package prefixes stripped off error
// messages before they ride an envelope. Stripped iteratively from the
// front only (wrap chains stack several), so mid-message text — field
// names, quoted values — is never touched.
var sdkMessagePrefixes = []string{
	"typesAPI: ", "spaceimpl: ", "Upsert: ", "schema: ", "crdt: ",
	"upsert: ", "space: ", "typetype: ",
}

// sanitizeSDKMessage strips SDK package prefixes from an error message.
// The remaining text names only caller-supplied fields and declaration
// rules.
func sanitizeSDKMessage(err error) string {
	msg := err.Error()
	for stripped := true; stripped; {
		stripped = false
		for _, p := range sdkMessagePrefixes {
			if rest, ok := strings.CutPrefix(msg, p); ok {
				msg = rest
				stripped = true
			}
		}
	}
	return msg
}

// datasetDraftFromAPI converts the wire draft to space.DatasetDraft,
// parsing enum labels. Returns ("", "") code/reason on success.
func datasetDraftFromAPI(req api.DatasetDraftRequest) (space.DatasetDraft, string, string) {
	draft := space.DatasetDraft{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Dynamic:     req.Dynamic,
		IdPattern:   req.IdPattern,
		IdMaxLen:    req.IdMaxLen,
		SkipHistory: req.SkipHistory,
	}
	var ok bool
	if draft.IdRule, ok = parseIdRule(req.IdRule); !ok {
		return draft, "request.invalid_field", `idRule must be "auto" or "user"`
	}
	if draft.DeleteBy, ok = parseDeletePolicy(req.DeleteBy); !ok {
		return draft, "request.invalid_field", `deleteBy must be "anyone" or "author"`
	}
	if req.Search != nil {
		if req.Search.Scope != "" && !index.ValidScope(req.Search.Scope) {
			return draft, "request.invalid_field", "search.scope must be a slug (lowercase letters, digits, _ or -; max 64)"
		}
		draft.Search = &space.SearchFields{Title: req.Search.Title, Text: []string(req.Search.Text), Scope: req.Search.Scope}
	}
	for _, f := range req.Fields {
		fd, code, reason := datasetFieldDraftFromAPI(f)
		if code != "" {
			return draft, code, "field " + f.Key + ": " + reason
		}
		draft.Fields = append(draft.Fields, fd)
	}
	return draft, "", ""
}

func datasetFieldDraftFromAPI(req api.DatasetFieldDraft) (space.DatasetFieldDraft, string, string) {
	if req.Key == "" {
		return space.DatasetFieldDraft{}, "request.missing_field", "key required"
	}
	draft := space.DatasetFieldDraft{
		Key:         req.Key,
		Name:        req.Name,
		Description: req.Description,
		Required:    req.Required,
	}
	var ok bool
	if req.Kind != "" {
		if draft.Kind, ok = propertyKindFromString(req.Kind); !ok {
			return draft, "request.invalid_field", "unknown kind " + req.Kind
		}
	}
	if draft.Shape, ok = datasetShapeFromAPI(req.Shape); !ok {
		return draft, "request.invalid_field", "shape has an unknown kind"
	}
	if req.Scope != "" {
		if draft.Scope, ok = space.ParseScope(req.Scope); !ok {
			return draft, "request.invalid_field", "unknown scope " + req.Scope
		}
	}
	if draft.MutableBy, ok = parseMutability(req.MutableBy); !ok {
		return draft, "request.invalid_field", `mutableBy must be "never", "author" or "any"`
	}
	if draft.Stamp, ok = parseStamp(req.Stamp); !ok {
		return draft, "request.invalid_field", `stamp must be "creator", "createTime" or "modifyTime"`
	}
	// The descriptor is validated against the field's declared kind —
	// the wire kind, or the shape's top-level kind; a stamp-implied kind
	// is left to the SDK (a stamped field has no slug to check).
	kind := req.Kind
	if kind == "" && req.Shape != nil {
		kind = req.Shape.Kind
	}
	xf, code, reason := validateDescriptor(req.XFormat, kind)
	if code != "" {
		return draft, code, reason
	}
	draft.XFormat = xf
	return draft, "", ""
}

// datasetShapeToAPI renders a declared value shape for the wire. Kind
// enums track 1:1 across handler / space / schema.
func datasetShapeToAPI(sh *handler.FieldShape) *api.DatasetFieldShape {
	if sh == nil {
		return nil
	}
	out := &api.DatasetFieldShape{Kind: propertyKindToString(space.PropertyKind(sh.Kind))}
	if sh.Items != nil {
		out.Items = datasetShapeToAPI(sh.Items)
	}
	if len(sh.Properties) > 0 {
		out.Properties = make(map[string]*api.DatasetFieldShape, len(sh.Properties))
		for k, sub := range sh.Properties {
			out.Properties[k] = datasetShapeToAPI(sub)
		}
	}
	return out
}

// datasetShapeFromAPI converts the recursive wire shape. nil is valid
// (no shape declared).
func datasetShapeFromAPI(s *api.DatasetFieldShape) (*handler.FieldShape, bool) {
	if s == nil {
		return nil, true
	}
	kind, ok := propertyKindFromString(s.Kind)
	if !ok {
		return nil, false
	}
	sh := handler.Leaf(handler.PropertyKind(kind))
	if s.Items != nil {
		child, ok := datasetShapeFromAPI(s.Items)
		if !ok {
			return nil, false
		}
		sh.Items = child
	}
	if len(s.Properties) > 0 {
		props := make(map[string]*handler.FieldShape, len(s.Properties))
		for k, sub := range s.Properties {
			child, ok := datasetShapeFromAPI(sub)
			if !ok {
				return nil, false
			}
			props[k] = child
		}
		sh.Properties = props
	}
	return sh, true
}

func datasetDefToAPI(def space.DatasetDef) api.DatasetDefResponse {
	out := api.DatasetDefResponse{
		Id:            def.Id,
		Name:          def.Name,
		DisplayName:   def.DisplayName,
		Description:   def.Description,
		Dynamic:       def.Dynamic,
		IdRule:        def.IdRule.String(),
		IdPattern:     def.IdPattern,
		IdMaxLen:      def.IdMaxLen,
		DeleteBy:      def.DeleteBy.String(),
		SkipHistory:   def.SkipHistory,
		Fields:        make([]api.DatasetFieldDef, 0, len(def.Fields)),
		Invalid:       def.Invalid,
		InvalidReason: def.InvalidReason,
	}
	if def.Search != nil {
		out.Search = &api.DatasetSearchFields{Title: def.Search.Title, Text: api.SearchText(def.Search.Text), Scope: def.Search.Scope}
	}
	for _, f := range def.Fields {
		scope := f.Scope
		if scope == 0 {
			scope = space.ScopeSynced
		}
		fd := api.DatasetFieldDef{
			Id:          f.Id,
			Key:         f.Key,
			Name:        f.Name,
			Description: f.Description,
			Kind:        propertyKindToString(f.Kind),
			Scope:       scope.String(),
			Required:    f.Required,
			MutableBy:   f.MutableBy.String(),
			XFormat:     xformatToWire(f.XFormat),
		}
		// A bare kind round-trips as `kind` alone; a refined shape
		// (items / properties) reads back whole.
		if f.Shape != nil && (f.Shape.Items != nil || len(f.Shape.Properties) > 0) {
			fd.Shape = datasetShapeToAPI(f.Shape)
		}
		if f.Stamp != space.StampNone {
			fd.Stamp = f.Stamp.String()
		}
		out.Fields = append(out.Fields, fd)
	}
	return out
}

func parseMutability(s string) (space.Mutability, bool) {
	switch s {
	case "", api.MutableNever:
		return space.MutableNever, true
	case api.MutableByAuthor:
		return space.MutableByAuthor, true
	case api.MutableByAnyone:
		return space.MutableByAnyone, true
	default:
		return 0, false
	}
}

func parseStamp(s string) (space.Stamp, bool) {
	switch s {
	case "":
		return space.StampNone, true
	case api.StampCreator:
		return space.StampCreator, true
	case api.StampCreateTime:
		return space.StampCreateTime, true
	case api.StampModifyTime:
		return space.StampModifyTime, true
	default:
		return 0, false
	}
}

func parseIdRule(s string) (space.IdRule, bool) {
	switch s {
	case "", api.IdRuleAuto:
		return space.IdAuto, true
	case api.IdRuleUser:
		return space.IdUser, true
	default:
		return 0, false
	}
}

func parseDeletePolicy(s string) (space.DeletePolicy, bool) {
	switch s {
	case "", api.DeleteByAnyone:
		return space.DeleteByAnyone, true
	case api.DeleteByAuthor:
		return space.DeleteByAuthor, true
	default:
		return 0, false
	}
}
