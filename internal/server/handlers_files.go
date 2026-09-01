package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// Files (files v2) — wraps the SDK's Space.Files() plus the SDK-level
// cache ops. The file BYTES ride plain HTTP: attach streams the raw
// request body (the one route exempted from the global BodyLimit),
// download serves a seekable reader through http.ServeContent so
// Content-Length / Range / 206 come for free. Everything else is the
// usual JSON. See docs/17-files.md for the model, docs/03-api.md
// § Files for the wire catalog.

// fileAttach handles POST /v1/spaces/:spaceId/objects/:objectId/files.
//
// The raw request body is the file content, streamed into
// Files().Attach without buffering. Metadata rides outside the body:
// `Content-Type` header → mime (parameters stripped;
// application/octet-stream and empty are treated as "unset"), query
// params `name`, `variant`+`variantOf` (must be set together; the
// variant original must live on the same object).
//
//	@Summary	Attach a file to an object (raw body upload)
//	@Tags		files
//	@Accept		octet-stream
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	path		string	true	"Object ID"
//	@Param		name		query		string	false	"User-facing file name"
//	@Param		variant		query		string	false	"Variant tag (requires variantOf)"
//	@Param		variantOf	query		string	false	"Original fileId this variant belongs to"
//	@Success	201	{object}	api.FileInfo
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/files [post]
func (d *deps) fileAttach(c echo.Context) error {
	variant := c.QueryParam("variant")
	variantOf := c.QueryParam("variantOf")
	if (variant == "") != (variantOf == "") {
		return writeError(c, http.StatusBadRequest, api.ErrFileVariantInvalid,
			"variant and variantOf must be set together", nil)
	}
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return errResp
	}
	var body io.Reader = c.Request().Body
	mimeType := attachMime(c.Request().Header.Get(echo.HeaderContentType))
	if mimeType == "" {
		body, mimeType = sniffMime(body)
	}
	opts := space.AttachOpts{
		Name:      c.QueryParam("name"),
		Mime:      mimeType,
		Variant:   space.Variant(variant),
		VariantOf: variantOf,
	}
	info, err := sp.Files().Attach(c.Request().Context(), objectId, body, opts)
	if err != nil {
		return fileError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId})
	}
	return c.JSON(http.StatusCreated, fileInfoToAPI(info))
}

// attachMime normalises the upload Content-Type into AttachOpts.Mime:
// parameters stripped, the octet-stream default (curl -T and fetch
// without an explicit type) treated as "caller didn't say".
func attachMime(contentType string) string {
	if contentType == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || mt == "application/octet-stream" {
		return ""
	}
	return mt
}

// sniffMaxBytes is what http.DetectContentType looks at.
const sniffMaxBytes = 512

// sniffMime fills the mime the caller left unset from the content
// itself: it peeks the first sniffMaxBytes of r without consuming them
// and returns a reader that still yields the whole body, plus the
// parameter-stripped http.DetectContentType verdict. An empty body or
// a signature the sniffer cannot place stays "" (unset) rather than
// octet-stream, so the stored mime is either a real type or absent —
// never the "caller didn't say" placeholder.
func sniffMime(r io.Reader) (io.Reader, string) {
	br := bufio.NewReaderSize(r, sniffMaxBytes)
	head, _ := br.Peek(sniffMaxBytes) // short read at EOF is fine
	if len(head) == 0 {
		return br, ""
	}
	mt, _, err := mime.ParseMediaType(http.DetectContentType(head))
	if err != nil || mt == "application/octet-stream" {
		return br, ""
	}
	return br, mt
}

// fileList handles GET /v1/spaces/:spaceId/files.
//
//	@Summary	List a space's files
//	@Tags		files
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		objectId	query		string	false	"Restrict to one object's files"
//	@Param		limit		query		int		false	"Cap the result (0 = unlimited)"
//	@Success	200	{object}	api.FileListResponse
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/files [get]
func (d *deps) fileList(c echo.Context) error {
	limit := 0
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"limit must be a non-negative integer", map[string]any{"field": "limit"})
		}
		limit = n
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	infos, err := sp.Files().List(c.Request().Context(), space.FileListOpts{
		ObjectId: c.QueryParam("objectId"),
		Limit:    limit,
	})
	if err != nil {
		return fileError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	out := api.FileListResponse{Files: make([]api.FileInfo, 0, len(infos))}
	for _, info := range infos {
		out.Files = append(out.Files, fileInfoToAPI(info))
	}
	return c.JSON(http.StatusOK, out)
}

// fileGet handles GET /v1/spaces/:spaceId/files/:fileId.
//
//	@Summary	Get one file's info
//	@Tags		files
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Param		fileId	path		string	true	"File ID"
//	@Success	200	{object}	api.FileInfo
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/files/{fileId} [get]
func (d *deps) fileGet(c echo.Context) error {
	sp, fileId, errResp, done := d.resolveSpaceFile(c)
	if done {
		return errResp
	}
	info, err := sp.Files().Get(c.Request().Context(), fileId)
	if err != nil {
		return fileError(c, err, map[string]any{"spaceId": sp.Id(), "fileId": fileId})
	}
	return c.JSON(http.StatusOK, fileInfoToAPI(info))
}

// fileContent handles GET /v1/spaces/:spaceId/files/:fileId/content.
//
// Serves the file's verified plaintext as a regular HTTP resource:
// Content-Type from the stored mime (octet-stream fallback — never
// sniffed), Content-Disposition inline with the stored name, and full
// Range/206 support via http.ServeContent (the SDK reader is
// seekable). Content not yet local streams in on demand; a download in
// flight at server shutdown is cut by the drain deadline.
//
// `?variant=` selects an alternate representation (a sibling file
// attached with variant/variantOf); default is the original.
//
//	@Summary	Download file content (raw bytes, Range supported)
//	@Tags		files
//	@Produce	octet-stream
//	@Param		spaceId	path	string	true	"Space ID"
//	@Param		fileId	path	string	true	"File ID"
//	@Param		variant	query	string	false	"Variant tag (default: original)"
//	@Success	200
//	@Success	206
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/files/{fileId}/content [get]
func (d *deps) fileContent(c echo.Context) error {
	sp, fileId, errResp, done := d.resolveSpaceFile(c)
	if done {
		return errResp
	}
	ctx := c.Request().Context()
	variant := space.Variant(c.QueryParam("variant"))
	details := map[string]any{"spaceId": sp.Id(), "fileId": fileId}
	if variant != space.VariantOriginal {
		details["variant"] = string(variant)
	}

	r, err := sp.Files().Open(ctx, fileId, variant)
	if err != nil {
		return fileError(c, err, details)
	}
	defer r.Close()

	// Headers come from the served row's member meta: the original's
	// info, or the resolved sibling's when a variant was requested.
	info, err := sp.Files().Get(ctx, fileId)
	if err != nil {
		return fileError(c, err, details)
	}
	if variant != space.VariantOriginal {
		if v, ok := findVariantInfo(ctx, sp, info, variant); ok {
			info = v
		}
	}

	h := c.Response().Header()
	contentType := info.Mime
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	h.Set(echo.HeaderContentType, contentType)
	if info.Name != "" {
		h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": info.Name}))
	}
	// Zero modtime: ServeContent skips Last-Modified/If-Modified-Since
	// handling — CRDT rows have no meaningful HTTP mtime.
	http.ServeContent(c.Response(), c.Request(), "", time.Time{}, r)
	return nil
}

// findVariantInfo resolves the FileInfo of the sibling row tagged
// {variant, variantOf: orig}. Best-effort — the Open above already
// proved the variant exists; a lookup miss just means generic headers.
func findVariantInfo(ctx context.Context, sp space.Space, orig space.FileInfo, variant space.Variant) (space.FileInfo, bool) {
	infos, err := sp.Files().List(ctx, space.FileListOpts{ObjectId: orig.ObjectId})
	if err != nil {
		return space.FileInfo{}, false
	}
	for _, info := range infos {
		if info.VariantOf == orig.FileId && info.Variant == variant {
			return info, true
		}
	}
	return space.FileInfo{}, false
}

// fileStatusGet handles GET /v1/spaces/:spaceId/files/:fileId/status.
//
//	@Summary	Get one file's durability status
//	@Tags		files
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Param		fileId	path		string	true	"File ID"
//	@Success	200	{object}	api.FileStatus
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/files/{fileId}/status [get]
func (d *deps) fileStatusGet(c echo.Context) error {
	sp, fileId, errResp, done := d.resolveSpaceFile(c)
	if done {
		return errResp
	}
	st, err := sp.Files().Status(c.Request().Context(), fileId)
	if err != nil {
		return fileError(c, err, map[string]any{"spaceId": sp.Id(), "fileId": fileId})
	}
	return c.JSON(http.StatusOK, fileStatusToAPI(st))
}

// fileStats handles GET /v1/spaces/:spaceId/files/stats.
//
//	@Summary	Get the space's aggregate file durability counts
//	@Tags		files
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	200	{object}	api.FileStats
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/files/stats [get]
func (d *deps) fileStats(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	stats, err := sp.Files().Stats(c.Request().Context())
	if err != nil {
		return fileError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusOK, api.FileStats{
		Total:    stats.Total,
		Durable:  stats.Durable,
		InFlight: stats.InFlight,
		Limited:  stats.Limited,
	})
}

// fileSubscribe handles GET /v1/spaces/:spaceId/files/subscribe.
//
// SSE stream of this space's FileStatus events on LOCAL transitions —
// attach, backup progress/failure, pin completion, manual retries.
// Remote flips (another device finishing a backup) are visible via
// Status/Get reads, not here. Frames: ready → status* → closed, same
// envelope and reason set as the sync-status streams.
//
//	@Summary	Subscribe to file durability transitions (SSE)
//	@Tags		files
//	@Produce	text/event-stream
//	@Param		spaceId	path	string	true	"Space ID"
//	@Success	200
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/files/subscribe [get]
func (d *deps) fileSubscribe(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	return forwardSSE(d, c, "status", sp.Files().SubscribeStatus,
		func(s space.FileStatus) any { return fileStatusToAPI(s) })
}

// filePin handles POST /v1/spaces/:spaceId/files/:fileId/pin.
//
//	@Summary	Pin a file (schedule a full background fetch)
//	@Tags		files
//	@Param		spaceId	path	string	true	"Space ID"
//	@Param		fileId	path	string	true	"File ID"
//	@Success	204
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/files/{fileId}/pin [post]
func (d *deps) filePin(c echo.Context) error {
	return d.fileAction(c, func(f space.Files, ctx context.Context, fileId string) error {
		return f.Pin(ctx, fileId)
	})
}

// fileRetry handles POST /v1/spaces/:spaceId/files/:fileId/retry.
//
//	@Summary	Retry a file's pending background work immediately
//	@Tags		files
//	@Param		spaceId	path	string	true	"Space ID"
//	@Param		fileId	path	string	true	"File ID"
//	@Success	204
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/files/{fileId}/retry [post]
func (d *deps) fileRetry(c echo.Context) error {
	return d.fileAction(c, func(f space.Files, ctx context.Context, fileId string) error {
		return f.Retry(ctx, fileId)
	})
}

// fileOffload handles POST /v1/spaces/:spaceId/files/:fileId/offload.
//
// Drops the file's local bytes, keeping the file itself refetchable.
// Refused with 409 file.not_durable while the local bytes are the only
// copy (file not backed up yet); inline files are a no-op.
//
//	@Summary	Offload a file's local bytes
//	@Tags		files
//	@Param		spaceId	path	string	true	"Space ID"
//	@Param		fileId	path	string	true	"File ID"
//	@Success	204
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/files/{fileId}/offload [post]
func (d *deps) fileOffload(c echo.Context) error {
	return d.fileAction(c, func(f space.Files, ctx context.Context, fileId string) error {
		return f.Offload(ctx, fileId)
	})
}

// fileDelete handles DELETE /v1/spaces/:spaceId/files/:fileId.
//
// Removes the file: the payload row — and the variant rows attached to
// it — is deleted in one synced change (every member sees the file
// disappear), pending background work is cancelled, and the local
// bytes' ref is released so cache GC reclaims them. The network copy
// is not reclaimed here (fileprotov2 has no delete RPC yet); the
// broker's row-driven accounting stops counting the rows once the
// deletion syncs. 404 file.not_found for an unknown (or already
// deleted) fileId.
//
//	@Summary	Delete a file
//	@Tags		files
//	@Param		spaceId	path	string	true	"Space ID"
//	@Param		fileId	path	string	true	"File ID"
//	@Success	204
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/files/{fileId} [delete]
func (d *deps) fileDelete(c echo.Context) error {
	return d.fileAction(c, func(f space.Files, ctx context.Context, fileId string) error {
		return f.Delete(ctx, fileId)
	})
}

// fileAction is the shared shape of the 204 per-file verbs.
func (d *deps) fileAction(c echo.Context, op func(space.Files, context.Context, string) error) error {
	sp, fileId, errResp, done := d.resolveSpaceFile(c)
	if done {
		return errResp
	}
	if err := op(sp.Files(), c.Request().Context(), fileId); err != nil {
		return fileError(c, err, map[string]any{"spaceId": sp.Id(), "fileId": fileId})
	}
	return c.NoContent(http.StatusNoContent)
}

// filesQuery handles POST /v1/spaces/:spaceId/objects/:objectId/files/query.
//
// Snapshot over the payload rows of ONE object's files — the SDK's
// Files().Query bridge to the generic query primitive (the payloads
// dataset lives on a derived child object whose id clients don't
// know, so the generic /query can't reach it). Body: the usual
// filter / sort / limit / offset / includeTotal; rows carry the
// cleartext fields only (id, rootCid, size, networkSign, objectId) —
// sealed member meta never appears here, use GET /files for it.
// 404 file.not_found until the object's first file is attached.
//
//	@Summary	Query one object's file payload rows
//	@Tags		files
//	@Accept		json
//	@Produce	json
//	@Param		spaceId		path		string					true	"Space ID"
//	@Param		objectId	path		string					true	"Object ID"
//	@Param		body		body		api.SpaceQueryObjectsRequest		false	"Query params"
//	@Success	200	{object}	api.QueryResponse
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/files/query [post]
func (d *deps) filesQuery(c echo.Context) error {
	sp, q, opts, errResp, done := d.buildFilesQuery(c)
	if done {
		return errResp
	}
	res, err := q.Snapshot(c.Request().Context(), opts)
	if err != nil {
		return fileError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": c.Param("objectId")})
	}
	return writeQueryResponse(c, res, opts.IncludeTotal)
}

// filesQuerySubscribe handles POST /v1/spaces/:spaceId/objects/:objectId/files/query/subscribe.
//
// The live sibling of /files/query — same body plus the Subscribe-only
// QueryOpts, same SSE frame set as every windowed subscribe
// (ready → snapshot → changes* → closed).
//
//	@Summary	Subscribe to one object's file payload rows (SSE)
//	@Tags		files
//	@Accept		json
//	@Produce	text/event-stream
//	@Param		spaceId		path	string				true	"Space ID"
//	@Param		objectId	path	string				true	"Object ID"
//	@Param		body		body	api.SpaceQueryObjectsRequest	false	"Query params"
//	@Success	200
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/objects/{objectId}/files/query/subscribe [post]
func (d *deps) filesQuerySubscribe(c echo.Context) error {
	sp, q, opts, errResp, done := d.buildFilesQuery(c)
	if done {
		return errResp
	}
	res, err := q.Subscribe(c.Request().Context(), opts)
	if err != nil {
		return fileError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": c.Param("objectId")})
	}
	return d.streamQuerySubscribe(c, res, opts.IncludeTotal)
}

// buildFilesQuery resolves the space + the object's files Query and
// applies the shared body params. objectId comes from the path (unlike
// the generic /query, which carries it in the body); the body itself
// is optional. Mirrors buildSharedQuery's (…, errResp, done) contract.
func (d *deps) buildFilesQuery(c echo.Context) (space.Space, space.Query, space.QueryOpts, error, bool) {
	sp, objectId, errResp, done := d.resolveSpaceObject(c)
	if done {
		return nil, nil, space.QueryOpts{}, errResp, true
	}
	fq, err := sp.Files().Query(objectId)
	if err != nil {
		return nil, nil, space.QueryOpts{}, fileError(c, err, map[string]any{"spaceId": sp.Id(), "objectId": objectId}), true
	}
	q, opts, errResp, done := buildBodyQuery(c, queryBodyFields, func(*fastjson.Value) (space.Query, error, bool) {
		return fq, nil, false
	})
	if done {
		return nil, nil, space.QueryOpts{}, errResp, true
	}
	return sp, q, opts, nil, false
}

// fileCacheGet handles GET /v1/files/cache — account-scoped.
//
//	@Summary	Get local file-cache size (all spaces)
//	@Tags		files
//	@Produce	json
//	@Success	200	{object}	api.FileCacheInfo
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/files/cache [get]
func (d *deps) fileCacheGet(c echo.Context) error {
	size, err := d.sdk.FileCacheSize(c.Request().Context())
	if err != nil {
		return fileError(c, err, nil)
	}
	return c.JSON(http.StatusOK, api.FileCacheInfo{Size: size})
}

// fileCacheFree handles POST /v1/files/cache/free — account-scoped.
//
// Reclaims local file bytes least-recently-used first, dropping only
// content that is safe to drop (backed up or unreferenced). Returns
// the bytes actually freed.
//
//	@Summary	Free up local file-cache bytes (LRU)
//	@Tags		files
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.FileCacheFreeRequest	true	"Bytes to free"
//	@Success	200		{object}	api.FileCacheFreeResult
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/files/cache/free [post]
func (d *deps) fileCacheFree(c echo.Context) error {
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
	bytes := root.GetInt64("bytes")
	if bytes <= 0 {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"bytes must be a positive integer", map[string]any{"field": "bytes"})
	}
	freed, err := d.sdk.FreeUpFileCache(c.Request().Context(), bytes)
	if err != nil {
		return fileError(c, err, nil)
	}
	return c.JSON(http.StatusOK, api.FileCacheFreeResult{Freed: freed})
}

// fileCacheSweep handles POST /v1/files/cache/sweep — account-scoped.
//
// One manual file-cache safety pass (prune deleted-file refs, delete
// unreferenced content past grace, drop stale partials). The same
// pass runs periodically only when files.gcInterval is configured.
//
//	@Summary	Run one file-cache safety sweep
//	@Tags		files
//	@Success	204
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/files/cache/sweep [post]
func (d *deps) fileCacheSweep(c echo.Context) error {
	if err := d.sdk.SweepFileCache(c.Request().Context()); err != nil {
		return fileError(c, err, nil)
	}
	return c.NoContent(http.StatusNoContent)
}

// resolveSpaceFile is resolveSpace plus the :fileId param check.
func (d *deps) resolveSpaceFile(c echo.Context) (space.Space, string, error, bool) {
	fileId := c.Param("fileId")
	if fileId == "" {
		return nil, "", writeError(c, http.StatusBadRequest, "request.missing_field", "fileId required", nil), true
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return nil, "", errResp, true
	}
	return sp, fileId, nil, false
}

// fileError maps SDK files-surface errors to the canonical envelope
// via the SDK's exported sentinels (errors.Is, never by message).
func fileError(c echo.Context, err error, details map[string]any) error {
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
	case errors.Is(err, space.ErrNotFound):
		return writeError(c, http.StatusNotFound, api.ErrFileNotFound, err.Error(), details)
	case errors.Is(err, space.ErrFileNotBackedUp):
		// Offload refused: the local bytes are the only copy.
		return writeError(c, http.StatusConflict, api.ErrFileNotDurable, err.Error(), details)
	case errors.Is(err, space.ErrFileNotAvailable):
		// The receiver-side wait state: row synced, bytes not local, and
		// the network can't serve them yet (not durable, or no public
		// read base). A retry-later resource state, NOT a server fault —
		// clients poll or wait for the row's networkSign update.
		return writeError(c, http.StatusConflict, api.ErrFileNotAvailable, err.Error(), details)
	case errors.Is(err, space.ErrFileVariantInvalid):
		return writeError(c, http.StatusBadRequest, api.ErrFileVariantInvalid, err.Error(), details)
	}
	return writeError(c, http.StatusInternalServerError, "internal", err.Error(), details)
}

func fileInfoToAPI(info space.FileInfo) api.FileInfo {
	return api.FileInfo{
		FileId:    info.FileId,
		ObjectId:  info.ObjectId,
		RootCid:   info.RootCid,
		Size:      info.Size,
		Inline:    info.Inline,
		Durable:   info.Durable,
		Cached:    info.Cached,
		Name:      info.Name,
		Mime:      info.Mime,
		Variant:   string(info.Variant),
		VariantOf: info.VariantOf,
	}
}

func fileStatusToAPI(st space.FileStatus) api.FileStatus {
	return api.FileStatus{
		FileId:   st.FileId,
		ObjectId: st.ObjectId,
		State:    string(st.State),
		Cached:   st.Cached,
		Attempts: st.Attempts,
		LastErr:  st.LastErr,
	}
}
