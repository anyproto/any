// Package ensure holds small write-path helpers shared by the built-in
// dataset packages and the server handlers.
package ensure

import (
	"context"

	"github.com/anyproto/any-sync-sdk/space"
)

// TypeAttached attaches typeId to the object's any.types if not already
// present, so membership-gated dataset writes are admitted by the SDK.
// Idempotent and cheap: a local read, then AttachType only on first use
// (subsequent writes find it present).
func TypeAttached(ctx context.Context, sp space.Space, objectId, typeId string) error {
	if rec, err := sp.Properties().Get(ctx, objectId); err == nil && rec != nil {
		for _, v := range rec.GetArray("any", "types") {
			if string(v.GetStringBytes()) == typeId {
				return nil
			}
		}
	}
	_, err := sp.Properties().AttachType(ctx, objectId, typeId)
	return err
}
