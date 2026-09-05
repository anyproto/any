package server

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"
)

// spaceModify handles POST /v1/spaces/:spaceId/modify.
//
//	@Summary	Modify records in a dataset
//	@Tags		data
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string					true	"Space ID"
//	@Param		body	body		api.SpaceModifyRequest	true	"Modify batch"
//	@Success	200		{object}	api.ModifyResult
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/modify [post]
func (d *deps) spaceModify(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	body, err := readBody(c)
	if err != nil || len(body) == 0 {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "missing or unreadable body", nil)
	}

	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	root, err := parser.ParseBytes(body)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil)
	}

	batch, err := buildModifyBatch(root)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.schema", err.Error(), nil)
	}

	res, err := sp.Modify(c.Request().Context(), batch)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": batch.ObjectId, "dataset": batch.Dataset})
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// spaceDeleteRecords handles POST /v1/spaces/:spaceId/delete-records.
//
//	@Summary	Delete records from a dataset
//	@Tags		data
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string						true	"Space ID"
//	@Param		body	body		api.DeleteRecordsRequest	true	"Delete params"
//	@Success	200		{object}	api.ModifyResult
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/delete-records [post]
func (d *deps) spaceDeleteRecords(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}

	req, ok := bindBody[struct {
		ObjectId  string   `json:"objectId"`
		Dataset   string   `json:"dataset"`
		RecordIds []string `json:"recordIds"`
		TraceIds  []string `json:"traceIds"`
	}](c)
	if !ok {
		return nil
	}
	if req.ObjectId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil)
	}
	if len(req.RecordIds) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "recordIds required", nil)
	}

	res, err := sp.Delete(c.Request().Context(), space.DeleteBatch{
		ObjectId:  req.ObjectId,
		Dataset:   req.Dataset,
		RecordIds: req.RecordIds,
		TraceIds:  req.TraceIds,
	})
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": req.ObjectId, "dataset": req.Dataset})
	}
	return c.JSON(http.StatusOK, modifyResultToAPI(res))
}

// buildModifyBatch walks the parsed body and lifts the relevant fields
// onto a space.ModifyBatch. Op values are kept as *fastjson.Value so
// the SDK can hand them off to anyenc.Arena.NewFromFastJson without an
// intermediate map[string]any.
func buildModifyBatch(root *fastjson.Value) (space.ModifyBatch, error) {
	batch := space.ModifyBatch{}
	if v := root.Get("objectId"); v != nil {
		batch.ObjectId = string(v.GetStringBytes())
	}
	if v := root.Get("dataset"); v != nil {
		batch.Dataset = string(v.GetStringBytes())
	}
	if batch.ObjectId == "" {
		return space.ModifyBatch{}, fmt.Errorf("objectId required")
	}
	if batch.Dataset == "" {
		return space.ModifyBatch{}, fmt.Errorf("dataset required")
	}

	for _, t := range root.GetArray("traceIds") {
		batch.TraceIds = append(batch.TraceIds, string(t.GetStringBytes()))
	}

	// scope selects the write route (docs/03-api.md § Modify records):
	// "synced" (default — the object's own DAG change) or "local"
	// (device-only materialization for fields the dataset schema
	// declares local-scope, e.g. chat's unread flags). Account and
	// derived are not writable here.
	if v := root.Get("scope"); v != nil {
		sc, ok := space.ParseScope(string(v.GetStringBytes()))
		if !ok || (sc != space.ScopeSynced && sc != space.ScopeLocal) {
			return space.ModifyBatch{}, fmt.Errorf(`scope must be "synced" or "local"`)
		}
		batch.Scope = sc
	}

	recs := root.GetArray("records")
	batch.Records = make([]space.RecordModify, 0, len(recs))
	for i, rv := range recs {
		rec, err := buildRecordModify(rv)
		if err != nil {
			return space.ModifyBatch{}, fmt.Errorf("records[%d]: %w", i, err)
		}
		batch.Records = append(batch.Records, rec)
	}

	// Local-scope constraints, checked at the boundary so callers get a
	// 400 instead of tripping the SDK guard: local fields annotate
	// records the synced route already created — explicit ids, no
	// upsert — and traceIds ride the any-sync change, which a local
	// write never produces. The shared objects dataset is per-property
	// scoped and its local writer is the properties endpoint, which
	// validates per-prop scope + kind — the generic route must not
	// bypass that.
	if batch.Scope == space.ScopeLocal {
		if batch.Dataset == objectsDataset {
			return space.ModifyBatch{}, fmt.Errorf("local-scope writes to the objects dataset go through POST …/properties/:objectId/set/:typeId")
		}
		if len(batch.TraceIds) > 0 {
			return space.ModifyBatch{}, fmt.Errorf("traceIds are not supported with local scope")
		}
		for i := range batch.Records {
			if batch.Records[i].Id == "" {
				return space.ModifyBatch{}, fmt.Errorf("records[%d]: local scope requires explicit record ids", i)
			}
			if batch.Records[i].Upsert {
				return space.ModifyBatch{}, fmt.Errorf("records[%d]: local scope cannot create records (upsert unsupported)", i)
			}
		}
	}
	return batch, nil
}

func buildRecordModify(rv *fastjson.Value) (space.RecordModify, error) {
	rec := space.RecordModify{}
	if v := rv.Get("id"); v != nil {
		rec.Id = string(v.GetStringBytes())
	}
	if v := rv.Get("upsert"); v != nil {
		t := v.Type()
		if t == fastjson.TypeTrue {
			rec.Upsert = true
		} else if t == fastjson.TypeFalse {
			rec.Upsert = false
		} else {
			return space.RecordModify{}, fmt.Errorf("upsert must be boolean")
		}
	}
	ops := rv.GetArray("ops")
	rec.Ops = make([]space.Op, 0, len(ops))
	for i, ov := range ops {
		op, err := buildOp(ov)
		if err != nil {
			return space.RecordModify{}, fmt.Errorf("ops[%d]: %w", i, err)
		}
		rec.Ops = append(rec.Ops, op)
	}
	return rec, nil
}

func buildOp(ov *fastjson.Value) (space.Op, error) {
	op := space.Op{}
	if v := ov.Get("type"); v != nil {
		op.Type = space.OpType(v.GetStringBytes())
	}
	if op.Type == "" {
		return space.Op{}, fmt.Errorf("type required")
	}
	if v := ov.Get("path"); v != nil {
		op.Path = string(v.GetStringBytes())
	}
	if val := ov.Get("value"); val != nil {
		op.Value = val
	}
	return op, nil
}
