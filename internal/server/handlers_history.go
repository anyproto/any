package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// Version-history surface over SDK Space.History() (SDK
// docs/version-history-proposal.md). Read-only; versions are ChangeIds
// (returned by every write as `changeId`). Semantic validation (param
// coupling, limit caps, error mapping) lives here — the SDK owns
// structure and the engines.

// historyMaxListLimit caps one listing page server-side (the SDK's own
// cap is higher; the API keeps pages UI-sized).
const (
	historyDefaultListLimit  = 50
	historyMaxListLimit      = 200
	historyMaxCoalesceWindow = 24 * time.Hour
)

// historyList handles GET /v1/spaces/:spaceId/objects/:objectId/history.
//
//	@Summary	List an object's change history
//	@Tags		history
//	@Produce	json
//	@Param		spaceId			path		string	true	"Space ID"
//	@Param		objectId		path		string	true	"Object ID"
//	@Param		dataset			query		string	false	"Only changes of this dataset"
//	@Param		recordId		query		string	false	"Only changes touching this record (requires dataset)"
//	@Param		traceId			query		string	false	"Only changes carrying this trace id"
//	@Param		author			query		string	false	"Only changes by this identity"
//	@Param		limit			query		int		false	"Page size (default 50, max 200)"
//	@Param		cursor			query		string	false	"Opaque cursor from the previous page"
//	@Param		coalesce		query		bool	false	"Group consecutive same-author changes"
//	@Param		coalesceWindow	query		int		false	"Coalesce window in seconds (default 300, max 86400)"
//	@Success	200				{object}	api.HistoryListResponse
//	@Failure	400				{object}	api.ErrorEnvelope
//	@Failure	500				{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/history [get]
func (d *deps) historyList(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}

	f := space.HistoryFilter{
		Dataset:  c.QueryParam("dataset"),
		RecordId: c.QueryParam("recordId"),
		TraceId:  c.QueryParam("traceId"),
		Author:   c.QueryParam("author"),
	}
	if f.RecordId != "" && f.Dataset == "" {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"recordId filter requires dataset", nil)
	}

	limit := historyDefaultListLimit
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"limit must be a positive integer", nil)
		}
		if n > historyMaxListLimit {
			n = historyMaxListLimit
		}
		limit = n
	}

	if coalesce, _ := strconv.ParseBool(c.QueryParam("coalesce")); coalesce {
		opts := &space.CoalesceOpts{}
		if raw := c.QueryParam("coalesceWindow"); raw != "" {
			secs, err := strconv.Atoi(raw)
			if err != nil || secs <= 0 || time.Duration(secs)*time.Second > historyMaxCoalesceWindow {
				return writeError(c, http.StatusBadRequest, "request.invalid_field",
					"coalesceWindow must be 1..86400 seconds", nil)
			}
			opts.Window = time.Duration(secs) * time.Second
		}
		f.Coalesce = opts
	}

	list, err := sp.History().ListChanges(c.Request().Context(), objectId, f, limit, c.QueryParam("cursor"))
	if err != nil {
		return historyError(c, err)
	}

	out := api.HistoryListResponse{Changes: make([]api.HistoryChange, 0, len(list.Changes)), Cursor: list.Cursor}
	for _, ch := range list.Changes {
		hc := api.HistoryChange{
			Version:   ch.Version,
			Author:    ch.Author,
			Timestamp: ch.Timestamp,
			Dataset:   ch.Dataset,
			TraceIds:  ch.TraceIds,
			Truncated: ch.Truncated,
			GroupSize: ch.GroupSize,
		}
		for _, tr := range ch.Touched {
			hc.Touched = append(hc.Touched, api.HistoryTouchedRecord{
				Dataset: tr.Dataset, RecordId: tr.RecordId, Ops: tr.Ops,
			})
		}
		out.Changes = append(out.Changes, hc)
	}
	return c.JSON(http.StatusOK, out)
}

// historyViewAt handles GET /v1/spaces/:spaceId/objects/:objectId/history/:version.
//
//	@Summary	View an object at a past version
//	@Tags		history
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Param		version		path		string	true	"Version (ChangeId)"
//	@Param		dataset		query		string	false	"Only this dataset"
//	@Success	200			{object}	api.HistoryViewResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	413			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/history/{version} [get]
func (d *deps) historyViewAt(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	version := c.Param("version")
	if version == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "version required", nil)
	}

	view, err := sp.History().ViewAt(c.Request().Context(), objectId, version)
	if err != nil {
		return historyError(c, err)
	}
	defer view.Close()

	datasets := view.Datasets()
	if only := c.QueryParam("dataset"); only != "" {
		datasets = []string{only}
	}

	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	out := api.HistoryViewResponse{Version: version, Datasets: make([]api.HistoryViewDataset, 0, len(datasets))}
	for _, ds := range datasets {
		records, rerr := view.Records(c.Request().Context(), ds)
		if rerr != nil {
			return historyError(c, rerr)
		}
		vd := api.HistoryViewDataset{Dataset: ds, Records: make([]json.RawMessage, 0, len(records))}
		for _, rec := range records {
			vd.Records = append(vd.Records, json.RawMessage(rec.FastJson(fa).MarshalTo(nil)))
			// MarshalTo copied the bytes out; reset per record so the
			// arena stays O(one record) instead of growing to the
			// whole materialized view.
			fa.Reset()
		}
		// Skip empty system datasets so the payload stays readable;
		// the explicitly-requested dataset is always present.
		if len(vd.Records) > 0 || c.QueryParam("dataset") != "" {
			out.Datasets = append(out.Datasets, vd)
		}
	}
	return c.JSON(http.StatusOK, out)
}

// historyRecordAt handles GET
// /v1/spaces/:spaceId/objects/:objectId/history/:version/datasets/:dataset/records/:recordId.
//
//	@Summary	View one record at a past version (fast path)
//	@Tags		history
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Param		version		path		string	true	"Version (ChangeId)"
//	@Param		dataset		path		string	true	"Dataset"
//	@Param		recordId	path		string	true	"Record ID"
//	@Success	200			{object}	api.HistoryRecordResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/history/{version}/datasets/{dataset}/records/{recordId} [get]
func (d *deps) historyRecordAt(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	version := c.Param("version")
	dataset, recordId := c.Param("dataset"), c.Param("recordId")
	if version == "" || dataset == "" || recordId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"version, dataset and recordId required", nil)
	}

	rec, err := sp.History().RecordAt(c.Request().Context(), objectId, dataset, recordId, version)
	if err != nil {
		return historyError(c, err)
	}

	out := api.HistoryRecordResponse{Version: version, Dataset: dataset, RecordId: recordId}
	if rec != nil {
		out.Exists = true
		out.Deleted = rec.Get("_deletedAt") != nil
		fa := getFastjsonArena()
		defer putFastjsonArena(fa)
		out.Record = json.RawMessage(rec.FastJson(fa).MarshalTo(nil))
	}
	return c.JSON(http.StatusOK, out)
}

// historyDiff handles GET /v1/spaces/:spaceId/objects/:objectId/history/diff.
//
//	@Summary	Diff two versions of an object
//	@Tags		history
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Param		version		query		string	true	"Newer version (ChangeId)"
//	@Param		base		query		string	false	"Older version; empty = version's parents (per-change effect diff)"
//	@Param		dataset		query		string	false	"Only this dataset"
//	@Param		recordIds	query		string	false	"Comma-separated record ids (requires dataset)"
//	@Success	200			{object}	api.HistoryDiffResponse
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	413			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/history/diff [get]
func (d *deps) historyDiff(c echo.Context) error {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	version := c.QueryParam("version")
	if version == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "version required", nil)
	}

	f := space.DiffFilter{Dataset: c.QueryParam("dataset")}
	if raw := c.QueryParam("recordIds"); raw != "" {
		if f.Dataset == "" {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"recordIds filter requires dataset", nil)
		}
		f.RecordIds = splitNonEmpty(raw, ',')
	}

	res, err := sp.History().Diff(c.Request().Context(), objectId, c.QueryParam("base"), version, f)
	if err != nil {
		return historyError(c, err)
	}

	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	rawVal := func(v *anyenc.Value) json.RawMessage {
		if v == nil {
			return nil
		}
		out := json.RawMessage(v.FastJson(fa).MarshalTo(nil))
		fa.Reset() // bytes copied; keep the arena O(one value)
		return out
	}

	out := api.HistoryDiffResponse{Base: res.Base, Version: res.Version, Datasets: make([]api.HistoryDatasetDiff, 0, len(res.Datasets))}
	for _, dd := range res.Datasets {
		pd := api.HistoryDatasetDiff{Dataset: dd.Dataset, Records: make([]api.HistoryRecordDiff, 0, len(dd.Records))}
		for _, rd := range dd.Records {
			pr := api.HistoryRecordDiff{Id: rd.Id, Kind: string(rd.Kind)}
			for _, fd := range rd.Fields {
				pr.Fields = append(pr.Fields, api.HistoryFieldDiff{
					Path:   fd.Path,
					Before: rawVal(fd.Before),
					After:  rawVal(fd.After),
				})
			}
			pd.Records = append(pd.Records, pr)
		}
		out.Datasets = append(out.Datasets, pd)
	}
	return c.JSON(http.StatusOK, out)
}

// historyError maps SDK history errors onto the API error envelope:
// unknown versions are 404s, an over-large view asks the client to
// narrow scope (413), a truncated causal past is 404 with its own code
// ("earlier changes not on this device" — best-effort-depth contract).
func historyError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, space.ErrVersionNotFound):
		return writeError(c, http.StatusNotFound, "history.version_not_found", err.Error(), nil)
	case errors.Is(err, space.ErrViewTooLarge):
		return writeError(c, http.StatusRequestEntityTooLarge, "history.view_too_large",
			"view too large — narrow the scope with dataset/record filters", nil)
	case errors.Is(err, space.ErrHistoryTruncated):
		return writeError(c, http.StatusNotFound, "history.truncated",
			"earlier changes are not available on this device", nil)
	default:
		return writeError(c, http.StatusInternalServerError, "internal", err.Error(), nil)
	}
}

// splitNonEmpty splits s on sep, dropping empty segments.
func splitNonEmpty(s string, sep rune) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == sep })
}
