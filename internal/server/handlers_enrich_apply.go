package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/anyproto/any-sync/commonspace/object/tree/treestorage"
	"github.com/anyproto/any-sync/commonspace/spacestorage"
	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"
	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/enricheddata"
	"github.com/anyproto/any/internal/enrichproposal"
	"github.com/anyproto/any/internal/ensure"
)

// enrichApply handles POST /v1/spaces/:spaceId/enrich/apply — the deterministic
// (no-LLM) apply of a reviewed enrich_proposal. For each enrich_proposal_items
// record it: creates the target object for `new` items (items sharing a
// newType+newName map to ONE object, so grouped facts land together, each
// keeping its own source); for `property` items sets the real property value on
// the target; and always writes an enriched_data record onto the target. Then
// it deletes the (ephemeral) proposal object. Idempotent — a re-apply of a
// deleted proposal reads no items and returns 404.
//
// This is the single apply implementation shared by the UI (a plain POST) and
// agent tooling (which just calls this endpoint).
//
//	@Summary	Apply a reviewed enrichment proposal and delete it
//	@Tags		enrich
//	@Accept		json
//	@Produce	json
//	@Param		spaceId	path		string					true	"Space ID"
//	@Param		body	body		api.EnrichApplyRequest	true	"Proposal id"
//	@Success	200		{object}	api.EnrichApplyResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/enrich/apply [post]
func (d *deps) enrichApply(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	req, ok := bindBody[api.EnrichApplyRequest](c)
	if !ok {
		return nil
	}
	if req.ProposalId == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "proposalId required", nil)
	}
	ctx := c.Request().Context()

	docs, err := sp.Query(req.ProposalId, enrichproposal.Dataset).Limit(10000).All(ctx)
	if err != nil {
		// A deleted proposal (the normal post-apply state — apply deletes it)
		// or a never-existing id must read as "nothing to apply", not a 500:
		// real deletion (SDK v0.0.12+) makes Query on a deleted object error
		// instead of returning zero rows.
		if errors.Is(err, spacestorage.ErrTreeStorageAlreadyDeleted) || errors.Is(err, treestorage.ErrUnknownTreeId) {
			return writeError(c, http.StatusNotFound, "enrich.empty_proposal",
				"no items in proposal (already applied, deleted, or empty)",
				map[string]any{"proposalId": req.ProposalId})
		}
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id(), "proposalId": req.ProposalId})
	}
	if len(docs) == 0 {
		return writeError(c, http.StatusNotFound, "enrich.empty_proposal",
			"no items in proposal (already applied, deleted, or empty)",
			map[string]any{"proposalId": req.ProposalId})
	}

	// xKey/id -> type id (built-ins resolve by their literal id; user types by xKey).
	typeById := map[string]string{}
	if types, terr := sp.Types().List(ctx); terr == nil {
		for _, t := range types {
			typeById[t.Id] = t.Id
			if t.XKey != "" {
				typeById[t.XKey] = t.Id
			}
		}
	}
	resolveType := func(xkeyOrId string) string {
		if id := typeById[xkeyOrId]; id != "" {
			return id
		}
		return xkeyOrId // assume it is already an id
	}
	// lazily cached per-type property (xKey/id -> prop id).
	propCache := map[string]map[string]string{}
	resolveProp := func(typeId, propXKey string) string {
		m := propCache[typeId]
		if m == nil {
			m = map[string]string{}
			if defs, perr := sp.Types().Properties(ctx, typeId); perr == nil {
				for _, p := range defs {
					m[p.Id] = p.Id
					if p.XKey != "" {
						m[p.XKey] = p.Id
					}
				}
			}
			propCache[typeId] = m
		}
		return m[propXKey]
	}

	res := api.EnrichApplyResponse{Failures: []string{}}
	// (newType, newName) -> objectId. A composite tuple key, so grouped `new`
	// items map to ONE object with NO delimiter to collide or corrupt — the
	// earlier "\x00" separator was a stray NUL that bricked storage (SYN-44).
	newByName := map[[2]string]string{}

	for _, doc := range docs {
		text := doc.GetString(enrichproposal.FieldText)
		source := doc.GetString(enrichproposal.FieldSource)
		outcome := doc.GetString(enrichproposal.FieldOutcome)
		targetId := doc.GetString(enrichproposal.FieldTargetObjectId)
		targetKind := doc.GetString(enrichproposal.FieldTargetKind)
		targetProperty := doc.GetString(enrichproposal.FieldTargetProperty)
		value := doc.GetString(enrichproposal.FieldValue)
		newType := doc.GetString(enrichproposal.FieldNewType)
		newName := doc.GetString(enrichproposal.FieldNewName)

		// 1. resolve/create the target object for `new` items (dedup by newName).
		if outcome == "new" && targetId == "" {
			if newType == "" || newName == "" {
				res.Failures = append(res.Failures, "new item missing newType/newName: "+trunc(text, 50))
				continue
			}
			key := [2]string{newType, newName}
			if id := newByName[key]; id != "" {
				targetId = id
			} else {
				opts := space.CreateObjectOpts{
					Types:             []string{resolveType(newType)},
					InitialProperties: map[string]map[string]any{"any": {"name": newName}},
				}
				_ = injectNavDefaults(c, sp, nil, &opts)
				oid, cerr := sp.Objects().Create(ctx, opts)
				if cerr != nil {
					handlerLog.Error("enrich apply: create object", zap.Error(cerr))
					res.Failures = append(res.Failures, "create '"+newName+"' failed")
					continue
				}
				targetId = oid
				newByName[key] = oid
				res.Created++
			}
		}
		if targetId == "" {
			res.Failures = append(res.Failures, "no target for item: "+trunc(text, 50))
			continue
		}

		// 2. property items: set the REAL property value on the target.
		recTarget, recValue := "", ""
		if targetKind == "property" && targetProperty != "" && value != "" {
			if dot := strings.IndexByte(targetProperty, '.'); dot > 0 {
				typeId := resolveType(targetProperty[:dot])
				propId := resolveProp(typeId, targetProperty[dot+1:])
				if propId == "" {
					res.Failures = append(res.Failures, "resolve property "+targetProperty+" failed")
				} else if err := setStringProperty(ctx, sp, targetId, typeId, propId, value); err != nil {
					handlerLog.Error("enrich apply: set property", zap.Error(err))
					res.Failures = append(res.Failures, "set "+targetProperty+" on "+targetId+" failed")
				} else {
					res.PropertiesSet++
					recTarget, recValue = targetProperty, value
				}
			} else {
				res.Failures = append(res.Failures, "bad targetProperty (need type.prop): "+targetProperty)
			}
		}

		// 3. durable provenance / collection record (always).
		t := text
		if t == "" {
			t = value
		}
		if t == "" {
			t = "(enrichment)"
		}
		if _, eerr := enricheddata.Create(ctx, sp, targetId, t, source, recTarget, recValue); eerr != nil {
			handlerLog.Error("enrich apply: enriched_data write", zap.Error(eerr))
			res.Failures = append(res.Failures, "enriched_data write on "+targetId+" failed")
		} else {
			res.EnrichedDataWritten++
		}
	}

	// 4. delete the ephemeral proposal.
	if derr := sp.Objects().Delete(ctx, req.ProposalId); derr != nil {
		handlerLog.Error("enrich apply: delete proposal", zap.Error(derr))
		res.Failures = append(res.Failures, "delete proposal failed")
	} else {
		res.ProposalDeleted = true
	}

	return c.JSON(http.StatusOK, res)
}

// setStringProperty sets one string-valued property, mirroring how propertiesSet
// passes a *fastjson.Value into Properties().Set. The SDK rejects value writes
// for a type the object doesn't implement, so the target's any.types is
// ensured first — a reviewed "set project.status on X" item must apply to any
// pre-existing object, not just ones that happen to carry the type (same
// ensure-on-write pattern as enricheddata.EnsureType).
func setStringProperty(ctx context.Context, sp space.Space, objectId, typeId, propId, value string) error {
	if err := ensure.TypeAttached(ctx, sp, objectId, typeId); err != nil {
		return err
	}
	arena := &fastjson.Arena{}
	_, err := sp.Properties().Set(ctx, objectId, typeId, map[string]any{propId: arena.NewString(value)})
	return err
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
