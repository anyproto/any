package agentmem

import (
	"context"
	"errors"
	"fmt"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// ErrNotFound signals that a referenced itemId does not exist on the
// brain object's agent_memory_items dataset. Maps to 404
// agent.memory_not_found at the HTTP layer.
var ErrNotFound = errors.New("agentmem: memory item not found")

// ErrNotAuthor signals an evolve / delete attempt by someone other
// than the item's creator. Maps to 403 agent.not_author.
var ErrNotAuthor = errors.New("agentmem: not the item author")

// ErrNoFields signals an evolve request with no mutable field present.
var ErrNoFields = errors.New("agentmem: no fields to evolve")

// CreateItem writes one memory item to the brain object's dataset and
// returns the raw space.ModifyResult; res.RecordIds[0] is the derived
// item id. The brain object is created by DeriveBrainObjectId, which
// callers resolve first (the HTTP handler does it per request — it's
// a local deterministic lookup after first use).
func CreateItem(ctx context.Context, sp space.Space, brainId string, req api.AgentMemoryCreateRequest) (space.ModifyResult, error) {
	payload := map[string]any{
		FieldCategory: req.Category,
		FieldContext:  req.Context,
	}
	if req.Body != "" {
		payload[FieldBody] = req.Body
	}
	if req.FromAgent != "" {
		payload[FieldFromAgent] = req.FromAgent
	}
	if req.ChatId != "" {
		payload[FieldChatId] = req.ChatId
	}
	if len(req.Tags) > 0 {
		payload[FieldTags] = req.Tags
	}
	if len(req.Entities) > 0 {
		payload[FieldEntities] = req.Entities
	}
	if len(req.Keywords) > 0 {
		payload[FieldKeywords] = req.Keywords
	}
	if req.Confidence != nil {
		payload[FieldConfidence] = *req.Confidence
	}
	if req.Importance != nil {
		payload[FieldImportance] = *req.Importance
	}
	if req.Salience != nil {
		payload[FieldSalience] = *req.Salience
	}
	if req.ValidFrom > 0 {
		payload[FieldValidFrom] = req.ValidFrom
	}
	if len(req.Edges) > 0 {
		payload[FieldEdges] = edgesToValues(req.Edges)
	}

	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: brainId,
		Dataset:  Dataset,
		Records: []space.RecordModify{{
			Id:     "",
			Upsert: true,
			Ops: []space.Op{{
				Type:  space.OpSet,
				Path:  "",
				Value: payload,
			}},
		}},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("agentmem: create: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, fmt.Errorf("agentmem: create: rejected: %s", res.Rejections[0].Reason)
	}
	if len(res.RecordIds) == 0 {
		return space.ModifyResult{}, fmt.Errorf("agentmem: create: empty RecordIds")
	}
	return res, nil
}

// Evolve applies the allow-listed mutable fields from the request as
// per-field $set ops. The author check runs both here (clean 403 for
// local callers) and in the handler (defense-in-depth for peer
// changes); modifiedAt is bumped handler-side.
func Evolve(ctx context.Context, sp space.Space, brainId, itemId, callerId string, req api.AgentMemoryEvolveRequest) (space.ModifyResult, error) {
	if err := requireAuthor(ctx, sp, brainId, itemId, callerId); err != nil {
		return space.ModifyResult{}, err
	}
	var ops []space.Op
	set := func(field string, value any) {
		ops = append(ops, space.Op{Type: space.OpSet, Path: field, Value: value})
	}
	if req.Salience != nil {
		set(FieldSalience, *req.Salience)
	}
	if req.AccessCount != nil {
		set(FieldAccessCount, *req.AccessCount)
	}
	if req.Confidence != nil {
		set(FieldConfidence, *req.Confidence)
	}
	if req.Importance != nil {
		set(FieldImportance, *req.Importance)
	}
	if req.Context != nil {
		set(FieldContext, *req.Context)
	}
	if req.Body != nil {
		set(FieldBody, *req.Body)
	}
	if req.Tags != nil {
		set(FieldTags, *req.Tags)
	}
	if req.Edges != nil {
		set(FieldEdges, edgesToValues(*req.Edges))
	}
	if len(ops) == 0 {
		return space.ModifyResult{}, ErrNoFields
	}

	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: brainId,
		Dataset:  Dataset,
		Records: []space.RecordModify{{
			Id:  itemId,
			Ops: ops,
		}},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("agentmem: evolve: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, fmt.Errorf("agentmem: evolve: rejected: %s", res.Rejections[0].Reason)
	}
	return res, nil
}

// Delete tombstones a memory item (author-only).
func Delete(ctx context.Context, sp space.Space, brainId, itemId, callerId string) (space.ModifyResult, error) {
	if err := requireAuthor(ctx, sp, brainId, itemId, callerId); err != nil {
		return space.ModifyResult{}, err
	}
	res, err := sp.Delete(ctx, space.DeleteBatch{
		ObjectId:  brainId,
		Dataset:   Dataset,
		RecordIds: []string{itemId},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("agentmem: delete: %w", err)
	}
	return res, nil
}

func edgesToValues(edges []api.Edge) []any {
	out := make([]any, 0, len(edges))
	for _, e := range edges {
		entry := map[string]any{
			FieldEdgeTo:   e.To,
			FieldEdgeType: e.Type,
		}
		if e.Strength != 0 {
			entry[FieldEdgeStrength] = e.Strength
		}
		out = append(out, entry)
	}
	return out
}

// requireAuthor reads the item's creator and returns ErrNotAuthor on
// mismatch (ErrNotFound when absent) — same contract as
// chat.requireAuthor.
func requireAuthor(ctx context.Context, sp space.Space, brainId, itemId, callerId string) error {
	doc, err := sp.Query(brainId, Dataset).
		Filter(query.Key{
			Path:   []string{"id"},
			Filter: query.NewComp(query.CompOpEq, itemId),
		}).
		One(ctx)
	if err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("agentmem: get: %w", err)
	}
	creator := doc.Get(FieldCreator)
	if creator == nil || creator.Type() != anyenc.TypeString || string(creator.GetStringBytes()) != callerId {
		return ErrNotAuthor
	}
	return nil
}
