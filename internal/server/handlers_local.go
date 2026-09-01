package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/localstore"
)

// The local store — device-local, non-CRDT any-store collections that
// share the SDK's sdk.db under the "l_" tag (task-local-store.md). A
// consumer-side surface like /search and /events: nothing here goes
// through the SDK's dataset/handler/CRDT machinery, nothing syncs,
// nothing subscribes. Every collection name reaching any-store passes
// localstore's ParseRef fence, so this file can never address an SDK
// collection — including the raw names a client puts inside a
// pipeline ($out / $merge into / $lookup from), which are re-validated
// by SinkTarget after parse.

const (
	// localMaxDocs bounds insert/upsert per request.
	localMaxDocs = 1000
	// localWriteChunk is the per-transaction write size. any-store has
	// one global writer, so a 1000-doc tx would stall every CRDT apply
	// for its duration — the SDK's own offload sweep chunks for the
	// same reason.
	localWriteChunk = 256
	// localQueryDefaultLimit / localQueryMaxLimit window /query.
	localQueryDefaultLimit = 100
	localQueryMaxLimit     = 1000
)

func registerLocalRoutes(v1 *echo.Group, d *deps) {
	v1.GET("/local/meta", d.localMeta)
	v1.GET("/local/collections", d.localCollectionsList)
	v1.PUT("/local/collections", d.localCollectionsEnsure)
	v1.DELETE("/local/collections", d.localCollectionsDrop)
	v1.POST("/local/insert", d.localInsert)
	v1.POST("/local/upsert", d.localUpsert)
	v1.POST("/local/update", d.localUpdate)
	v1.POST("/local/delete", d.localDelete)
	v1.POST("/local/get", d.localGet)
	v1.POST("/local/query", d.localQuery)
	v1.POST("/local/aggregate", d.localAggregate)
	v1.POST("/local/indexes", d.localIndexes)
}

// Closed body vocabularies, derived from the api structs (the
// checkUnknownFields pattern the query/aggregate handlers use).
var (
	localEnsureFields    = jsonFieldNames(reflect.TypeFor[api.LocalEnsureRequest]())
	localDocsFields      = jsonFieldNames(reflect.TypeFor[api.LocalDocsRequest]())
	localUpdateFields    = jsonFieldNames(reflect.TypeFor[api.LocalUpdateRequest]())
	localDeleteFields    = jsonFieldNames(reflect.TypeFor[api.LocalDeleteRequest]())
	localGetFields       = jsonFieldNames(reflect.TypeFor[api.LocalGetRequest]())
	localQueryFields     = jsonFieldNames(reflect.TypeFor[api.LocalQueryRequest]())
	localAggregateFields = jsonFieldNames(reflect.TypeFor[api.LocalAggregateRequest]())
	localIndexesFields   = jsonFieldNames(reflect.TypeFor[api.LocalIndexesRequest]())
	localCollFields      = jsonFieldNames(reflect.TypeFor[api.LocalCollection]())
)

func localDisabled(c echo.Context) error {
	return writeError(c, http.StatusConflict, "local.disabled",
		"the local store is disabled on this server (local.enabled: false)", nil)
}

// localBody reads and parses a required JSON object body and gates it
// on the accepted field set. The returned root is owned by a pooled
// parser: the caller MUST `defer release()` and not touch root after —
// releasing early hands the same Value slots to the next request.
func localBody(c echo.Context, fields []string) (root *fastjson.Value, release func(), errResp error, done bool) {
	body, err := readBody(c)
	if err != nil || len(body) == 0 {
		return nil, nil, writeError(c, http.StatusBadRequest, "request.bad_json", "missing or unreadable body", nil), true
	}
	parser := getFastjsonParser()
	release = func() { putFastjsonParser(parser) }
	root, err = parser.ParseBytes(body)
	if err != nil {
		release()
		return nil, nil, writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil), true
	}
	if errResp, done := checkUnknownFields(c, root, "", fields...); done {
		release()
		return nil, nil, errResp, true
	}
	return root, release, nil, false
}

// localRefFromValue reads a {scope, spaceId?, name} object. strict
// rejects keys beyond the triple — off for a body whose own field gate
// already ran (ensure carries indexes next to the triple).
func localRefFromValue(c echo.Context, v *fastjson.Value, what string, strict bool) (localstore.Ref, error, bool) {
	if v == nil || v.Type() != fastjson.TypeObject {
		return localstore.Ref{}, writeError(c, http.StatusBadRequest, "request.missing_field",
			what+" required: {scope, spaceId?, name}", nil), true
	}
	if strict {
		if errResp, done := checkUnknownFields(c, v, "", localCollFields...); done {
			return localstore.Ref{}, errResp, true
		}
	}
	return localRef(c,
		string(v.GetStringBytes("scope")),
		string(v.GetStringBytes("spaceId")),
		string(v.GetStringBytes("name")))
}

// localRef is the wire → Ref step: ParseRef. The space pre-flight for
// space scope is localSpaceCheck (404 space.not_found / 409
// space.deleted — a dead space can't be resurrected as a namespace;
// this does not clean anything up).
func localRef(c echo.Context, scope, spaceId, name string) (localstore.Ref, error, bool) {
	ref, err := localstore.ParseRef(localstore.Scope(scope), spaceId, name)
	if err != nil {
		return localstore.Ref{}, writeError(c, http.StatusBadRequest, "local.bad_name", err.Error(),
			map[string]any{"scope": scope, "spaceId": spaceId, "name": name}), true
	}
	return ref, nil, false
}

// localReq is a resolved /v1/local request: the collection handle, its
// ref, and the parsed body. release returns the body's parser to the
// pool — defer it; root is invalid afterwards.
type localReq struct {
	coll    anystore.Collection
	ref     localstore.Ref
	root    *fastjson.Value
	release func()
}

// resolveLocal is the shared prologue: disabled guard → body → coll ref
// → space pre-flight → collection handle (404 local.collection_not_found).
// On done=true nothing is held; otherwise the caller owns req.release.
func (d *deps) resolveLocal(c echo.Context, fields []string) (req localReq, errResp error, done bool) {
	if d.local == nil {
		return localReq{}, localDisabled(c), true
	}
	root, release, errResp, done := localBody(c, fields)
	if done {
		return localReq{}, errResp, true
	}
	ref, errResp, done := localRefFromValue(c, root.Get("coll"), "coll", true)
	if done {
		release()
		return localReq{}, errResp, true
	}
	if errResp, done := d.localSpaceCheck(c, ref); done {
		release()
		return localReq{}, errResp, true
	}
	coll, err := d.local.Collection(c.Request().Context(), ref)
	if err != nil {
		release()
		return localReq{}, localError(c, err, ref), true
	}
	return localReq{coll: coll, ref: ref, root: root, release: release}, nil, false
}

func (d *deps) localSpaceCheck(c echo.Context, ref localstore.Ref) (error, bool) {
	if ref.Scope != localstore.ScopeSpace {
		return nil, false
	}
	if _, err := d.sdk.Spaces().Get(c.Request().Context(), ref.SpaceId); err != nil {
		return spaceError(c, err, ref.SpaceId), true
	}
	return nil, false
}

func localDetails(ref localstore.Ref) map[string]any {
	d := map[string]any{"scope": string(ref.Scope), "name": ref.Name}
	if ref.SpaceId != "" {
		d["spaceId"] = ref.SpaceId
	}
	return d
}

// localError maps store / any-store errors onto the local.* namespace.
func localError(c echo.Context, err error, ref localstore.Ref) error {
	details := localDetails(ref)
	switch {
	case errors.Is(err, localstore.ErrNotFound):
		return writeError(c, http.StatusNotFound, "local.collection_not_found",
			"local collection not found — PUT /v1/local/collections first", details)
	case errors.Is(err, localstore.ErrBadName):
		return writeError(c, http.StatusBadRequest, "local.bad_name", err.Error(), details)
	case errors.Is(err, localstore.ErrNotLocal):
		return writeError(c, http.StatusBadRequest, "local.bad_sink_target",
			"pipeline names a collection outside the local store: "+err.Error(), details)
	case errors.Is(err, anystore.ErrDocExists):
		return writeError(c, http.StatusConflict, "local.duplicate_id", "document id already exists", details)
	case errors.Is(err, anystore.ErrDocNotFound):
		return writeError(c, http.StatusNotFound, "local.doc_not_found", "document not found", details)
	case errors.Is(err, anystore.ErrUniqueConstraint):
		return writeError(c, http.StatusConflict, "local.unique_violation", "unique index violation", details)
	case errors.Is(err, anystore.ErrGroupLimitExceeded),
		errors.Is(err, anystore.ErrAccumArrayLimitExceeded),
		errors.Is(err, anystore.ErrAggMemoryLimitExceeded):
		limit := "group"
		if errors.Is(err, anystore.ErrAccumArrayLimitExceeded) {
			limit = "accumArray"
		} else if errors.Is(err, anystore.ErrAggMemoryLimitExceeded) {
			limit = "memory"
		}
		details["limit"] = limit
		return writeError(c, http.StatusBadRequest, "local.limit_exceeded", err.Error(), details)
	case errors.Is(err, anystore.ErrIndexMismatch), errors.Is(err, anystore.ErrInvalidIndexName):
		return writeError(c, http.StatusBadRequest, "local.bad_index", err.Error(), details)
	case errors.Is(err, anystore.ErrAggregateIntoSource),
		errors.Is(err, anystore.ErrDocWithoutId),
		errors.Is(err, anystore.ErrMergeNoId),
		errors.Is(err, anystore.ErrMergeMatched),
		errors.Is(err, anystore.ErrMergeNotMatched):
		return writeError(c, http.StatusBadRequest, "local.bad_pipeline", err.Error(), details)
	}
	var pe *query.ParseError
	if errors.As(err, &pe) {
		code := "local.bad_filter"
		switch pe.Source {
		case "pipeline":
			code = "local.bad_pipeline"
		case "modifier":
			code = "local.bad_modifier"
		case "sort":
			code = "local.bad_sort"
		}
		if pe.Path != "" {
			details["path"] = pe.Path
		}
		if pe.Op != "" {
			details["operator"] = pe.Op
		}
		return writeError(c, http.StatusBadRequest, code, err.Error(), details)
	}
	return sdkOpError(c, err, details)
}

func localIndexesToAPI(coll anystore.Collection) []api.LocalIndex {
	out := []api.LocalIndex{}
	for _, ix := range coll.GetIndexes() {
		info := ix.Info()
		out = append(out, api.LocalIndex{Name: info.Name, Fields: info.Fields, Unique: info.Unique, Sparse: info.Sparse})
	}
	return out
}

func localIndexesFromAPI(in []api.LocalIndex) []anystore.IndexInfo {
	out := make([]anystore.IndexInfo, 0, len(in))
	for _, ix := range in {
		out = append(out, anystore.IndexInfo{Name: ix.Name, Fields: ix.Fields, Unique: ix.Unique, Sparse: ix.Sparse})
	}
	return out
}

func localInfoToAPI(info localstore.Info) api.LocalCollectionInfo {
	out := api.LocalCollectionInfo{
		LocalCollection: api.LocalCollection{Scope: string(info.Scope), SpaceId: info.SpaceId, Name: info.Name},
		StorageName:     info.StorageName(),
		Count:           info.Count,
		Indexes:         []api.LocalIndex{},
	}
	for _, ix := range info.Indexes {
		out.Indexes = append(out.Indexes, api.LocalIndex{Name: ix.Name, Fields: ix.Fields, Unique: ix.Unique, Sparse: ix.Sparse})
	}
	return out
}

// localMeta handles GET /v1/local/meta.
//
//	@Summary	Local store aggregation grammar
//	@Tags		local
//	@Produce	json
//	@Success	200	{object}	api.LocalMetaResponse
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Router		/local/meta [get]
func (d *deps) localMeta(c echo.Context) error {
	if d.local == nil {
		return localDisabled(c)
	}
	return c.JSON(http.StatusOK, api.LocalMetaResponse{
		Stages:       anystore.AggregateStages(),
		Accumulators: anystore.AggregateAccumulators(),
	})
}

// localCollectionsList handles GET /v1/local/collections.
//
//	@Summary	List local collections
//	@Tags		local
//	@Produce	json
//	@Param		scope	query		string	false	"account | space (absent = both)"
//	@Param		spaceId	query		string	false	"restrict to one space (scope=space)"
//	@Success	200		{object}	api.LocalListResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/local/collections [get]
func (d *deps) localCollectionsList(c echo.Context) error {
	if d.local == nil {
		return localDisabled(c)
	}
	scope := localstore.Scope(c.QueryParam("scope"))
	spaceId := c.QueryParam("spaceId")
	if spaceId != "" && scope == "" {
		scope = localstore.ScopeSpace
	}
	infos, err := d.local.List(c.Request().Context(), scope, spaceId)
	if err != nil {
		return localError(c, err, localstore.Ref{Scope: scope, SpaceId: spaceId})
	}
	out := api.LocalListResponse{Collections: make([]api.LocalCollectionInfo, 0, len(infos))}
	for _, info := range infos {
		out.Collections = append(out.Collections, localInfoToAPI(info))
	}
	return c.JSON(http.StatusOK, out)
}

// localCollectionsEnsure handles PUT /v1/local/collections.
//
//	@Summary	Create a local collection (idempotent)
//	@Tags		local
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalEnsureRequest	true	"Collection + indexes"
//	@Success	200		{object}	api.LocalEnsureResponse	"already existed"
//	@Success	201		{object}	api.LocalEnsureResponse	"created"
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/local/collections [put]
func (d *deps) localCollectionsEnsure(c echo.Context) error {
	if d.local == nil {
		return localDisabled(c)
	}
	root, release, errResp, done := localBody(c, localEnsureFields)
	if done {
		return errResp
	}
	defer release()
	ref, errResp, done := localRefFromValue(c, root, "collection", false)
	if done {
		return errResp
	}
	if errResp, done := d.localSpaceCheck(c, ref); done {
		return errResp
	}
	indexes, errResp, done := localIndexesFromValue(c, root.Get("indexes"))
	if done {
		return errResp
	}
	ctx := c.Request().Context()
	created, err := d.local.Ensure(ctx, ref, indexes)
	if err != nil {
		return localError(c, err, ref)
	}
	coll, err := d.local.Collection(ctx, ref)
	if err != nil {
		return localError(c, err, ref)
	}
	count, err := coll.Count(ctx)
	if err != nil {
		return localError(c, err, ref)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return c.JSON(status, api.LocalEnsureResponse{
		Created: created,
		Collection: api.LocalCollectionInfo{
			LocalCollection: api.LocalCollection{Scope: string(ref.Scope), SpaceId: ref.SpaceId, Name: ref.Name},
			StorageName:     ref.StorageName(),
			Count:           count,
			Indexes:         localIndexesToAPI(coll),
		},
	})
}

// localIndexesFromValue decodes an optional `indexes` array.
func localIndexesFromValue(c echo.Context, v *fastjson.Value) ([]anystore.IndexInfo, error, bool) {
	if v == nil || v.Type() == fastjson.TypeNull {
		return nil, nil, false
	}
	var in []api.LocalIndex
	if err := json.Unmarshal(v.MarshalTo(nil), &in); err != nil {
		return nil, writeError(c, http.StatusBadRequest, "request.schema",
			"indexes must be an array of {name?, fields, unique?, sparse?}", nil), true
	}
	for i, ix := range in {
		if len(ix.Fields) == 0 {
			return nil, writeError(c, http.StatusBadRequest, "request.schema",
				"indexes: fields required", map[string]any{"index": i}), true
		}
	}
	return localIndexesFromAPI(in), nil, false
}

// localCollectionsDrop handles DELETE /v1/local/collections.
//
//	@Summary	Drop a local collection and its data
//	@Tags		local
//	@Param		scope	query	string	true	"account | space"
//	@Param		spaceId	query	string	false	"required for scope=space"
//	@Param		name	query	string	true	"collection name"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Router		/local/collections [delete]
func (d *deps) localCollectionsDrop(c echo.Context) error {
	if d.local == nil {
		return localDisabled(c)
	}
	ref, errResp, done := localRef(c, c.QueryParam("scope"), c.QueryParam("spaceId"), c.QueryParam("name"))
	if done {
		return errResp
	}
	// No space pre-flight on drop: it is the cleanup path for
	// collections whose space is already gone.
	if err := d.local.Drop(c.Request().Context(), ref); err != nil {
		return localError(c, err, ref)
	}
	return c.NoContent(http.StatusNoContent)
}

// localDocsFromValue decodes `docs`, minting ids where absent, and
// returns the anyenc docs plus their ids in order.
func localDocsFromValue(c echo.Context, v *fastjson.Value) ([]*anyenc.Value, []string, error, bool) {
	if v == nil || v.Type() != fastjson.TypeArray {
		return nil, nil, writeError(c, http.StatusBadRequest, "request.missing_field",
			"docs required: a non-empty array of objects", nil), true
	}
	arr := v.GetArray()
	if len(arr) == 0 {
		return nil, nil, writeError(c, http.StatusBadRequest, "request.missing_field",
			"docs required: a non-empty array of objects", nil), true
	}
	if len(arr) > localMaxDocs {
		return nil, nil, writeError(c, http.StatusBadRequest, "local.too_many_docs",
			"too many docs in one request", map[string]any{"max": localMaxDocs, "got": len(arr)}), true
	}
	docs := make([]*anyenc.Value, 0, len(arr))
	ids := make([]string, 0, len(arr))
	// One arena for every minted id, held until the docs are serialised:
	// NewString aliases the arena's buffer, and a returned arena is Reset
	// by its next borrower.
	arena := getFastjsonArena()
	defer putFastjsonArena(arena)
	for i, item := range arr {
		if item.Type() != fastjson.TypeObject {
			return nil, nil, writeError(c, http.StatusBadRequest, "request.schema",
				"docs: every element must be an object", map[string]any{"index": i}), true
		}
		idv := item.Get("id")
		var id string
		switch {
		case idv == nil || idv.Type() == fastjson.TypeNull:
			id = localMintId()
			item.Set("id", arena.NewString(id))
		case idv.Type() == fastjson.TypeString:
			id = string(idv.GetStringBytes())
			if id == "" {
				return nil, nil, writeError(c, http.StatusBadRequest, "request.schema",
					"docs: id must be a non-empty string", map[string]any{"index": i}), true
			}
		default:
			return nil, nil, writeError(c, http.StatusBadRequest, "request.schema",
				"docs: id must be a string", map[string]any{"index": i}), true
		}
		doc, err := anyenc.ParseJson(string(item.MarshalTo(nil)))
		if err != nil {
			return nil, nil, writeError(c, http.StatusBadRequest, "request.schema",
				"docs: "+err.Error(), map[string]any{"index": i}), true
		}
		docs = append(docs, doc)
		ids = append(ids, id)
	}
	return docs, ids, nil, false
}

// localMintId returns a fresh random id (16 bytes, hex) for a doc that
// carries none.
func localMintId() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// localWriteChunks runs fn over docs in localWriteChunk-sized slices,
// one write transaction each.
func localWriteChunks(c echo.Context, coll anystore.Collection, docs []*anyenc.Value, fn func(ctx echo.Context, coll anystore.Collection, chunk []*anyenc.Value) error) error {
	for start := 0; start < len(docs); start += localWriteChunk {
		end := min(start+localWriteChunk, len(docs))
		if err := fn(c, coll, docs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// localInsert handles POST /v1/local/insert.
//
//	@Summary	Insert documents into a local collection
//	@Tags		local
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalDocsRequest	true	"Collection + docs"
//	@Success	200		{object}	api.LocalIdsResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/local/insert [post]
func (d *deps) localInsert(c echo.Context) error {
	req, errResp, done := d.resolveLocal(c, localDocsFields)
	if done {
		return errResp
	}
	defer req.release()
	docs, ids, errResp, done := localDocsFromValue(c, req.root.Get("docs"))
	if done {
		return errResp
	}
	err := localWriteChunks(c, req.coll, docs, func(c echo.Context, coll anystore.Collection, chunk []*anyenc.Value) error {
		return coll.Insert(c.Request().Context(), chunk...)
	})
	if err != nil {
		return localError(c, err, req.ref)
	}
	return c.JSON(http.StatusOK, api.LocalIdsResponse{Ids: ids})
}

// localUpsert handles POST /v1/local/upsert — whole-document replace-
// or-insert per doc.
//
//	@Summary	Upsert documents into a local collection
//	@Tags		local
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalDocsRequest	true	"Collection + docs"
//	@Success	200		{object}	api.LocalIdsResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/local/upsert [post]
func (d *deps) localUpsert(c echo.Context) error {
	req, errResp, done := d.resolveLocal(c, localDocsFields)
	if done {
		return errResp
	}
	defer req.release()
	docs, ids, errResp, done := localDocsFromValue(c, req.root.Get("docs"))
	if done {
		return errResp
	}
	err := localWriteChunks(c, req.coll, docs, func(c echo.Context, coll anystore.Collection, chunk []*anyenc.Value) error {
		tx, err := coll.WriteTx(c.Request().Context())
		if err != nil {
			return err
		}
		for _, doc := range chunk {
			if err := coll.UpsertOne(tx.Context(), doc); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return localError(c, err, req.ref)
	}
	return c.JSON(http.StatusOK, api.LocalIdsResponse{Ids: ids})
}

// localUpdate handles POST /v1/local/update.
//
//	@Summary	Apply a modifier to one local document
//	@Tags		local
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalUpdateRequest	true	"Collection + id + modifier"
//	@Success	200		{object}	api.LocalUpdateResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/local/update [post]
func (d *deps) localUpdate(c echo.Context) error {
	req, errResp, done := d.resolveLocal(c, localUpdateFields)
	if done {
		return errResp
	}
	defer req.release()
	id := string(req.root.GetStringBytes("id"))
	if id == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "id required", nil)
	}
	modv := req.root.Get("modifier")
	if modv == nil || modv.Type() != fastjson.TypeObject {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "modifier required: a mongo-style modifier object", nil)
	}
	mod, err := query.ParseModifier(modv)
	if err != nil {
		return localError(c, err, req.ref)
	}
	upsert := req.root.GetBool("upsert")
	// Modify and read back in ONE transaction: a concurrent delete
	// between the two would otherwise turn an applied update into a
	// 404, and an upsert retry would resurrect the deleted document.
	tx, err := req.coll.WriteTx(c.Request().Context())
	if err != nil {
		return localError(c, err, req.ref)
	}
	var res anystore.ModifyResult
	if upsert {
		res, err = req.coll.UpsertId(tx.Context(), id, mod)
	} else {
		res, err = req.coll.UpdateId(tx.Context(), id, mod)
	}
	if err != nil {
		_ = tx.Rollback()
		return localError(c, err, req.ref)
	}
	doc, err := req.coll.FindId(tx.Context(), id)
	if err != nil {
		_ = tx.Rollback()
		return localError(c, err, req.ref)
	}
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	record := json.RawMessage(doc.Value().FastJson(fa).MarshalTo(nil))
	if err := tx.Commit(); err != nil {
		return localError(c, err, req.ref)
	}
	return c.JSON(http.StatusOK, api.LocalUpdateResponse{
		Modified: res.Modified > 0,
		Record:   record,
	})
}

// localDelete handles POST /v1/local/delete.
//
//	@Summary	Delete local documents by ids or filter
//	@Tags		local
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalDeleteRequest	true	"Collection + ids | filter"
//	@Success	200		{object}	api.LocalDeleteResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/local/delete [post]
func (d *deps) localDelete(c echo.Context) error {
	req, errResp, done := d.resolveLocal(c, localDeleteFields)
	if done {
		return errResp
	}
	defer req.release()
	coll, ref, root := req.coll, req.ref, req.root
	ctx := c.Request().Context()
	idsv := root.Get("ids")
	filter := root.Get("filter")
	hasIds := idsv != nil && idsv.Type() != fastjson.TypeNull
	hasFilter := filter != nil && filter.Type() != fastjson.TypeNull
	if hasIds == hasFilter {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "exactly one of ids or filter required", nil)
	}
	var ids []string
	if hasIds {
		if idsv.Type() != fastjson.TypeArray {
			return writeError(c, http.StatusBadRequest, "request.schema", "ids must be an array of strings", nil)
		}
		for _, v := range idsv.GetArray() {
			if v.Type() != fastjson.TypeString {
				return writeError(c, http.StatusBadRequest, "request.schema", "ids must be an array of strings", nil)
			}
			ids = append(ids, string(v.GetStringBytes()))
		}
	} else {
		if errResp, done := checkFilter(c, root); done {
			return errResp
		}
		// Collect under one read, then delete in chunks: not atomic
		// per call (documented), but never one long write tx.
		iter, err := coll.Find(filter).Iter(ctx)
		if err != nil {
			return localError(c, err, ref)
		}
		for iter.Next() {
			doc, err := iter.Doc()
			if err != nil {
				_ = iter.Close()
				return localError(c, err, ref)
			}
			ids = append(ids, string(doc.Value().GetStringBytes("id")))
		}
		if err := errors.Join(iter.Err(), iter.Close()); err != nil {
			return localError(c, err, ref)
		}
	}
	deleted := 0
	for start := 0; start < len(ids); start += localWriteChunk {
		end := min(start+localWriteChunk, len(ids))
		tx, err := coll.WriteTx(ctx)
		if err != nil {
			return localError(c, err, ref)
		}
		for _, id := range ids[start:end] {
			err := coll.DeleteId(tx.Context(), id)
			if errors.Is(err, anystore.ErrDocNotFound) {
				continue
			}
			if err != nil {
				_ = tx.Rollback()
				return localError(c, err, ref)
			}
			deleted++
		}
		if err := tx.Commit(); err != nil {
			return localError(c, err, ref)
		}
	}
	return c.JSON(http.StatusOK, api.LocalDeleteResponse{Deleted: deleted})
}

// localGet handles POST /v1/local/get.
//
//	@Summary	Read one local document by id
//	@Tags		local
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalGetRequest	true	"Collection + id"
//	@Success	200		{object}	api.LocalRecordResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/local/get [post]
func (d *deps) localGet(c echo.Context) error {
	req, errResp, done := d.resolveLocal(c, localGetFields)
	if done {
		return errResp
	}
	defer req.release()
	coll, ref, root := req.coll, req.ref, req.root
	id := string(root.GetStringBytes("id"))
	if id == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "id required", nil)
	}
	doc, err := coll.FindId(c.Request().Context(), id)
	if err != nil {
		return localError(c, err, ref)
	}
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	return c.JSON(http.StatusOK, api.LocalRecordResponse{
		Record: json.RawMessage(doc.Value().FastJson(fa).MarshalTo(nil)),
	})
}

// localQuery handles POST /v1/local/query.
//
//	@Summary	Query a local collection
//	@Tags		local
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalQueryRequest	true	"Collection + filter/sort/limit/offset"
//	@Success	200		{object}	api.QueryResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/local/query [post]
func (d *deps) localQuery(c echo.Context) error {
	req, errResp, done := d.resolveLocal(c, localQueryFields)
	if done {
		return errResp
	}
	defer req.release()
	coll, ref, root := req.coll, req.ref, req.root
	if errResp, done := checkFilter(c, root); done {
		return errResp
	}
	proj, errResp, done := parseProjection(c, root)
	if done {
		return errResp
	}
	shaper := recordShaper{proj: proj}
	ctx := c.Request().Context()
	var filter any
	if f := root.Get("filter"); f != nil && f.Type() != fastjson.TypeNull {
		filter = f
	}
	q := coll.Find(filter)
	if sortArr := root.GetArray("sort"); len(sortArr) > 0 {
		keys := make([]any, 0, len(sortArr))
		for _, s := range sortArr {
			keys = append(keys, string(s.GetStringBytes()))
		}
		if _, err := query.ParseSort(keys...); err != nil {
			return localError(c, err, ref)
		}
		q = q.Sort(keys...)
	}
	limit := localQueryDefaultLimit
	if v := root.Get("limit"); v != nil {
		if n := v.GetInt(); n > 0 {
			limit = min(n, localQueryMaxLimit)
		}
	}
	q = q.Limit(uint(limit))
	offset := 0
	if v := root.Get("offset"); v != nil {
		if n := v.GetInt(); n > 0 {
			offset = n
			q = q.Offset(uint(n))
		}
	}
	iter, err := q.Iter(ctx)
	if err != nil {
		return localError(c, err, ref)
	}
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	records := make([]json.RawMessage, 0, limit)
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			_ = iter.Close()
			return localError(c, err, ref)
		}
		records = append(records, json.RawMessage(shaper.record(doc.Value(), fa).MarshalTo(nil)))
	}
	if err := errors.Join(iter.Err(), iter.Close()); err != nil {
		return localError(c, err, ref)
	}
	out := api.QueryResponse{Records: records}
	if root.GetBool("includeTotal") {
		total, err := coll.Find(filter).Count(ctx)
		if err != nil {
			return localError(c, err, ref)
		}
		hasNext := offset+len(records) < total
		out.Total = &total
		out.HasNext = &hasNext
	}
	return c.JSON(http.StatusOK, out)
}

// localAggregate handles POST /v1/local/aggregate.
//
//	@Summary	Run an aggregation pipeline over a local collection
//	@Tags		local
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalAggregateRequest	true	"Collection + pipeline"
//	@Success	200		{object}	api.LocalAggregateResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/local/aggregate [post]
func (d *deps) localAggregate(c echo.Context) error {
	req, errResp, done := d.resolveLocal(c, localAggregateFields)
	if done {
		return errResp
	}
	defer req.release()
	coll, ref, root := req.coll, req.ref, req.root
	pipeline, errResp, done := requirePipeline(c, root)
	if done {
		return errResp
	}
	// The sink fence: every raw collection name a client put INSIDE the
	// pipeline must be a local one that exists. Done on the raw JSON,
	// before any-store parses — a rejected name never reaches the store.
	hasSink, errResp, done := d.localPipelineTargets(c, pipeline, ref)
	if done {
		return errResp
	}
	ctx := c.Request().Context()
	agg := coll.Aggregate(pipeline)
	if v := root.Get("groupLimit"); v != nil {
		agg = agg.GroupLimit(v.GetInt())
	}
	if v := root.Get("accumArrayLimit"); v != nil {
		agg = agg.AccumArrayLimit(v.GetInt())
	}
	if v := root.Get("memoryLimitBytes"); v != nil {
		agg = agg.MemoryLimit(v.GetInt())
	}
	if root.GetBool("explain") {
		plan, err := agg.Explain(ctx)
		if err != nil {
			return localError(c, err, ref)
		}
		p := plan.Plan
		return c.JSON(http.StatusOK, api.LocalAggregateResponse{Plan: &p})
	}
	if hasSink {
		written, err := agg.Count(ctx)
		if err != nil {
			return localError(c, err, ref)
		}
		return c.JSON(http.StatusOK, api.LocalAggregateResponse{Written: &written})
	}
	iter, err := agg.Iter(ctx)
	if err != nil {
		return localError(c, err, ref)
	}
	fa := getFastjsonArena()
	defer putFastjsonArena(fa)
	records := make([]json.RawMessage, 0)
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			_ = iter.Close()
			return localError(c, err, ref)
		}
		records = append(records, json.RawMessage(doc.Value().FastJson(fa).MarshalTo(nil)))
	}
	if err := errors.Join(iter.Err(), iter.Close()); err != nil {
		return localError(c, err, ref)
	}
	return c.JSON(http.StatusOK, api.LocalAggregateResponse{Records: records})
}

// localPipelineTargets walks the raw pipeline (recursing into $facet
// sub-pipelines) for the stages that name a collection — $out
// (string), $merge (string | {into}), $lookup ({from}) — and fences
// each: the name must be a local collection (localstore.SinkTarget),
// its space must pass the same pre-flight as the request's own
// collection, and it must already exist (404 local.collection_not_found
// — a sink never mints a collection, so a typo'd target cannot become
// a silent sibling). $lookup from must name the aggregated collection
// itself until any-store resolves cross-collection lookups (400
// local.bad_pipeline rather than the unmapped store error). A malformed
// stage shape is left for any-store's parser (local.bad_pipeline);
// only a present, string-typed name is fenced here. Reports whether
// the pipeline ends in a write sink.
func (d *deps) localPipelineTargets(c echo.Context, pipeline *fastjson.Value, source localstore.Ref) (hasSink bool, errResp error, done bool) {
	for _, stage := range pipeline.GetArray() {
		obj, err := stage.Object()
		if err != nil {
			continue
		}
		var target string
		var found, lookup bool
		var facets []*fastjson.Value
		obj.Visit(func(key []byte, v *fastjson.Value) {
			switch string(key) {
			case "$out":
				hasSink = true
				if v.Type() == fastjson.TypeString {
					target, found = string(v.GetStringBytes()), true
				}
			case "$merge":
				hasSink = true
				switch v.Type() {
				case fastjson.TypeString:
					target, found = string(v.GetStringBytes()), true
				case fastjson.TypeObject:
					if into := v.Get("into"); into != nil && into.Type() == fastjson.TypeString {
						target, found = string(into.GetStringBytes()), true
					}
				}
			case "$lookup":
				if from := v.Get("from"); from != nil && from.Type() == fastjson.TypeString {
					target, found, lookup = string(from.GetStringBytes()), true, true
				}
			case "$facet":
				if fo, err := v.Object(); err == nil {
					fo.Visit(func(_ []byte, sub *fastjson.Value) {
						if sub.Type() == fastjson.TypeArray {
							facets = append(facets, sub)
						}
					})
				}
			}
		})
		for _, sub := range facets {
			if _, errResp, done := d.localPipelineTargets(c, sub, source); done {
				return hasSink, errResp, true
			}
		}
		if !found {
			continue
		}
		ref, err := localstore.SinkTarget(target)
		if err != nil {
			return hasSink, localError(c, err, source), true
		}
		if lookup {
			if ref != source {
				return hasSink, writeError(c, http.StatusBadRequest, "local.bad_pipeline",
					"$lookup from must name the aggregated collection ("+source.StorageName()+"); cross-collection lookups are not supported yet",
					localDetails(source)), true
			}
			continue
		}
		if errResp, done := d.localSpaceCheck(c, ref); done {
			return hasSink, errResp, true
		}
		if _, err := d.local.Collection(c.Request().Context(), ref); err != nil {
			return hasSink, localError(c, err, ref), true
		}
	}
	return hasSink, nil, false
}

// localIndexes handles POST /v1/local/indexes.
//
//	@Summary	Ensure / drop indexes on a local collection
//	@Tags		local
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalIndexesRequest	true	"Collection + ensure/drop"
//	@Success	200		{object}	api.LocalIndexesResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Router		/local/indexes [post]
func (d *deps) localIndexes(c echo.Context) error {
	req, errResp, done := d.resolveLocal(c, localIndexesFields)
	if done {
		return errResp
	}
	defer req.release()
	coll, ref, root := req.coll, req.ref, req.root
	ctx := c.Request().Context()
	ensure, errResp, done := localIndexesFromValue(c, root.Get("ensure"))
	if done {
		return errResp
	}
	for _, v := range root.GetArray("drop") {
		name := string(v.GetStringBytes())
		if name == "" {
			return writeError(c, http.StatusBadRequest, "request.schema", "drop must be an array of index names", nil)
		}
		if err := coll.DropIndex(ctx, name); err != nil && !errors.Is(err, anystore.ErrIndexNotFound) {
			return localError(c, err, ref)
		}
	}
	if len(ensure) > 0 {
		if err := coll.EnsureIndex(ctx, ensure...); err != nil {
			return localError(c, err, ref)
		}
	}
	return c.JSON(http.StatusOK, api.LocalIndexesResponse{Indexes: localIndexesToAPI(coll)})
}
