package enricheddata

import (
	"errors"
	"fmt"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/handler"
)

// BeforeCreate validates the creation payload (one multi-field $set op with an
// object payload, `text` required, optional `source`/`target`/`value` strings,
// server fields rejected) and stamps createdBy/createdAt — same contract as
// chat. Modify and delete are left to DefaultHandler: enrichment
// records are editable and removable (a user may prune a sourced fact).
func (dataHandler) BeforeCreate(ctx *handler.ChangeCtx, rec *handler.RecordChange, sink *handler.Sink) error {
	if len(rec.Ops) != 1 {
		return reject("expected exactly one multi-field $set op")
	}
	op := &rec.Ops[0]
	if op.Type != handler.OpSet || len(op.Path) != 0 {
		return reject("expected multi-field $set with empty path")
	}
	if op.Payload == nil || op.Payload.Type() != anyenc.TypeObject {
		return reject("payload must be a JSON object")
	}
	obj, err := op.Payload.Object()
	if err != nil {
		return reject("payload must be a JSON object")
	}
	var (
		visitErr error
		hasText  bool
	)
	obj.Visit(func(rawKey []byte, v *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		switch key {
		case FieldText:
			hasText = true
			visitErr = checkString(key, v, MaxTextBytes, false)
		case FieldSource:
			visitErr = checkString(key, v, MaxSourceBytes, true)
		case FieldTarget:
			visitErr = checkString(key, v, MaxTargetBytes, true)
		case FieldValue:
			visitErr = checkString(key, v, MaxValueBytes, true)
		default:
			// creator/createdAt are server-stamped; anything else is unknown.
			visitErr = reject("field_not_allowed: " + key)
		}
	})
	if visitErr != nil {
		return visitErr
	}
	if !hasText {
		return reject("text required")
	}
	stampCreate(ctx, sink)
	return nil
}

func checkString(key string, v *anyenc.Value, maxBytes int, allowEmpty bool) error {
	if v.Type() != anyenc.TypeString {
		return reject(key + " must be a string")
	}
	s := v.GetStringBytes()
	if len(s) == 0 && !allowEmpty {
		return reject(key + " must be non-empty when present")
	}
	if len(s) > maxBytes {
		return reject(fmt.Sprintf("%s too long (%d > %d bytes)", key, len(s), maxBytes))
	}
	return nil
}

// stampCreate queues sink.Derive ops for createdBy/createdAt — mirrors
// chat's stampCreate.
func stampCreate(ctx *handler.ChangeCtx, sink *handler.Sink) {
	if ctx == nil || ctx.Change == nil || sink == nil {
		return
	}
	a := &anyenc.Arena{}
	if c := ctx.Change.Creator; c != "" {
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{FieldCreatedBy},
			Payload: a.NewString(c),
		})
	}
	if ts := ctx.Change.Timestamp; ts > 0 {
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{FieldCreatedAt},
			Payload: a.NewNumberInt(int(ts)),
		})
	}
}

func reject(msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("enriched_data.create: %s", msg))
}
