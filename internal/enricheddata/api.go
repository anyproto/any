package enricheddata

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/ensure"
)

// EnsureType attaches the enriched_data type to the object's any.types,
// making the target object multitype (its own types + enriched_data).
func EnsureType(ctx context.Context, sp space.Space, objectId string) error {
	return ensure.TypeAttached(ctx, sp, objectId, TypeId)
}

// Create writes one enrichment record onto objectId's enriched_data dataset,
// attaching the type first. text is required; source/target/value are written
// only when non-empty. The record id is auto-derived (RecordIds[0]).
func Create(ctx context.Context, sp space.Space, objectId, text, source, target, value string) (space.ModifyResult, error) {
	if err := EnsureType(ctx, sp, objectId); err != nil {
		return space.ModifyResult{}, fmt.Errorf("enricheddata: ensure type: %w", err)
	}
	payload := map[string]any{FieldText: text}
	if source != "" {
		payload[FieldSource] = source
	}
	if target != "" {
		payload[FieldTarget] = target
	}
	if value != "" {
		payload[FieldValue] = value
	}
	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  Dataset,
		Records: []space.RecordModify{{
			Upsert: true,
			Ops: []space.Op{{
				Type:  space.OpSet,
				Path:  "",
				Value: payload,
			}},
		}},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("enricheddata: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, fmt.Errorf("enricheddata: rejected: %s", res.Rejections[0].Reason)
	}
	if len(res.RecordIds) == 0 {
		return space.ModifyResult{}, fmt.Errorf("enricheddata: empty RecordIds")
	}
	return res, nil
}
