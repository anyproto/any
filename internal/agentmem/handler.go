package agentmem

import (
	"errors"
	"fmt"
	"math"
	"strings"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/handler"
)

// Indexes: `category` backs the category filter (replaces the old
// tags[0] convention), `createdAt` newest-first listings, `validFrom`
// period range queries. All three are stamped or defaulted on every
// record, so the indexes are dense.
func (itemsHandler) Indexes() []anystore.IndexInfo {
	return []anystore.IndexInfo{
		{Name: "idx_category", Fields: []string{FieldCategory}},
		{Name: "idx_created", Fields: []string{FieldCreatedAt}},
		{Name: "idx_valid_from", Fields: []string{FieldValidFrom}},
	}
}

// BeforeCreate validates the creation payload, stamps creator /
// createdAt / modifiedAt, and defaults the scoring fields the old
// system carried (confidence 5, importance 5, salience 10, accessCount
// 0, validFrom = createdAt) when the payload omits them — so every
// record is complete for the indexed fallback-recall queries.
func (itemsHandler) BeforeCreate(ctx *handler.ChangeCtx, rec *handler.RecordChange, sink *handler.Sink) error {
	if len(rec.Ops) != 1 {
		return rejectCreate("expected exactly one multi-field $set op")
	}
	op := &rec.Ops[0]
	if op.Type != handler.OpSet || len(op.Path) != 0 {
		return rejectCreate("expected multi-field $set with empty path")
	}
	if op.Payload == nil || op.Payload.Type() != anyenc.TypeObject {
		return rejectCreate("payload must be a JSON object")
	}

	present, err := validateCreatePayload(op.Payload)
	if err != nil {
		return err
	}

	stampCreate(ctx, sink, present)
	return nil
}

// BeforeModify gates per-op evolution. Allow-list — single-segment
// $set on a mutable field, author-only, modifiedAt bumped:
//
//	salience, accessCount, confidence, importance   — numeric ranges
//	context                                          — non-empty string
//	body                                             — string
//	tags, edges                                      — validated arrays
//
// Everything else (category, creator, createdAt, fromAgent, chatId,
// entities, keywords, validFrom, embeddingRef, _ver.*) is immutable —
// category changes or re-attribution mean writing a new item.
func (itemsHandler) BeforeModify(ctx *handler.ChangeCtx, _ *handler.RecordChange, op *handler.Op, sink *handler.Sink) error {
	if op.Type != handler.OpSet || len(op.Path) != 1 {
		return rejectOp("field_not_modifiable: " + pathString(op.Path))
	}
	if !isAuthor(ctx) {
		return rejectOp("not_author")
	}
	field := op.Path[0]
	var err error
	switch field {
	case FieldSalience:
		err = checkFloatRange(field, op.Payload, 0, 10)
	case FieldAccessCount:
		err = checkIntRange(field, op.Payload, 0, math.MaxInt32)
	case FieldConfidence:
		err = checkIntRange(field, op.Payload, 0, 10)
	case FieldImportance:
		err = checkIntRange(field, op.Payload, 1, 10)
	case FieldContext:
		err = checkString(field, op.Payload, MaxContextBytes, false)
	case FieldBody:
		err = checkString(field, op.Payload, MaxBodyBytes, true)
	case FieldTags:
		err = checkStringArray(field, op.Payload, MaxTags, MaxTagBytes)
	case FieldEdges:
		err = validateEdges(op.Payload)
	default:
		return rejectOp("field_not_modifiable: " + field)
	}
	if err != nil {
		return err
	}
	bumpModifiedAt(ctx, sink)
	return nil
}

// BeforeDelete enforces "you can only delete your own memory".
func (itemsHandler) BeforeDelete(ctx *handler.ChangeCtx, _ *handler.RecordChange, _ *handler.Sink) error {
	if !isAuthor(ctx) {
		return rejectRecord("not_author")
	}
	return nil
}

// --- create validation ------------------------------------------------------

// presentFields tracks which defaultable fields the payload carried so
// stampCreate only derives the missing ones.
type presentFields struct {
	confidence, importance, salience, accessCount, validFrom bool
}

func validateCreatePayload(payload *anyenc.Value) (presentFields, error) {
	var present presentFields
	obj, err := payload.Object()
	if err != nil {
		return present, rejectCreate("payload must be a JSON object")
	}
	var (
		visitErr                error
		hasCategory, hasContext bool
	)
	obj.Visit(func(rawKey []byte, v *anyenc.Value) {
		if visitErr != nil {
			return
		}
		key := string(rawKey)
		switch key {
		case FieldCategory:
			hasCategory = true
			visitErr = checkSlug(key, v, MaxCategoryBytes)
		case FieldContext:
			hasContext = true
			visitErr = checkString(key, v, MaxContextBytes, false)
		case FieldBody:
			visitErr = checkString(key, v, MaxBodyBytes, true)
		case FieldFromAgent:
			visitErr = checkString(key, v, MaxFromAgentBytes, false)
		case FieldChatId:
			visitErr = checkString(key, v, MaxIdBytes, false)
		case FieldTags:
			visitErr = checkStringArray(key, v, MaxTags, MaxTagBytes)
		case FieldEntities:
			visitErr = checkStringArray(key, v, MaxEntities, MaxEntityBytes)
		case FieldKeywords:
			visitErr = checkStringArray(key, v, MaxKeywords, MaxKeywordBytes)
		case FieldConfidence:
			present.confidence = true
			visitErr = checkIntRange(key, v, 0, 10)
		case FieldImportance:
			present.importance = true
			visitErr = checkIntRange(key, v, 1, 10)
		case FieldSalience:
			present.salience = true
			visitErr = checkFloatRange(key, v, 0, 10)
		case FieldAccessCount:
			present.accessCount = true
			visitErr = checkIntRange(key, v, 0, math.MaxInt32)
		case FieldValidFrom:
			present.validFrom = true
			visitErr = checkNonNegNumber(key, v)
		case FieldEdges:
			visitErr = validateEdges(v)
		case FieldSource:
			visitErr = checkString(key, v, MaxSourceBytes, false)
		case FieldProvenance:
			visitErr = validateProvenance(v)
		default:
			// Covers server-stamped fields (creator, createdAt,
			// modifiedAt), the reserved embeddingRef, and anything
			// unknown.
			visitErr = rejectCreate("field_not_allowed: " + key)
		}
	})
	if visitErr != nil {
		return present, visitErr
	}
	if !hasCategory {
		return present, rejectCreate("category required")
	}
	if !hasContext {
		return present, rejectCreate("context required")
	}
	return present, nil
}

// validateEdges gates the typed-link array: each entry is
// {to (required id), type (required slug), strength (optional 0..1)};
// unknown sub-fields reject.
// validateProvenance checks the structured drill-back pointer: an
// object whose only known subkey is fromSeq (non-negative int, a seq
// in the chat object's agent_turns dataset). Unknown subkeys reject —
// new pointer kinds are contract changes, mirroring the top-level
// field allow-list.
func validateProvenance(v *anyenc.Value) error {
	obj, err := v.Object()
	if err != nil {
		return rejectCreate("provenance must be an object")
	}
	var visitErr error
	obj.Visit(func(rawKey []byte, sub *anyenc.Value) {
		if visitErr != nil {
			return
		}
		switch key := string(rawKey); key {
		case FieldProvFromSeq:
			visitErr = checkIntRange("provenance."+key, sub, 0, math.MaxInt32)
		default:
			visitErr = rejectCreate("provenance: field_not_allowed: " + key)
		}
	})
	return visitErr
}

func validateEdges(v *anyenc.Value) error {
	if v.Type() != anyenc.TypeArray {
		return rejectCreate("edges must be an array")
	}
	items, err := v.Array()
	if err != nil {
		return rejectCreate("edges must be an array")
	}
	if len(items) > MaxEdges {
		return rejectCreate(fmt.Sprintf("edges: too many entries (%d > %d)", len(items), MaxEdges))
	}
	for i, item := range items {
		if item.Type() != anyenc.TypeObject {
			return rejectCreate(fmt.Sprintf("edges[%d] must be an object", i))
		}
		obj, err := item.Object()
		if err != nil {
			return rejectCreate(fmt.Sprintf("edges[%d] must be an object", i))
		}
		var (
			visitErr       error
			hasTo, hasType bool
		)
		obj.Visit(func(rawKey []byte, val *anyenc.Value) {
			if visitErr != nil {
				return
			}
			key := string(rawKey)
			switch key {
			case FieldEdgeTo:
				hasTo = true
				visitErr = checkString(fmt.Sprintf("edges[%d].to", i), val, MaxIdBytes, false)
			case FieldEdgeType:
				hasType = true
				visitErr = checkSlug(fmt.Sprintf("edges[%d].type", i), val, MaxEdgeTypeBytes)
			case FieldEdgeStrength:
				if val.Type() != anyenc.TypeNumber {
					visitErr = rejectCreate(fmt.Sprintf("edges[%d].strength must be a number", i))
					return
				}
				f, err := val.Float64()
				if err != nil || f < 0 || f > 1 {
					visitErr = rejectCreate(fmt.Sprintf("edges[%d].strength must be in 0..1", i))
					return
				}
			default:
				visitErr = rejectCreate(fmt.Sprintf("edges[%d]: unknown field %s", i, key))
				return
			}
		})
		if visitErr != nil {
			return visitErr
		}
		if !hasTo {
			return rejectCreate(fmt.Sprintf("edges[%d].to required", i))
		}
		if !hasType {
			return rejectCreate(fmt.Sprintf("edges[%d].type required", i))
		}
	}
	return nil
}

// --- field checks -------------------------------------------------------------

func checkString(key string, v *anyenc.Value, maxBytes int, allowEmpty bool) error {
	if v == nil || v.Type() != anyenc.TypeString {
		return rejectCreate(key + " must be a string")
	}
	s := v.GetStringBytes()
	if len(s) == 0 && !allowEmpty {
		return rejectCreate(key + " must be non-empty")
	}
	if len(s) > maxBytes {
		return rejectCreate(fmt.Sprintf("%s too long (%d > %d bytes)", key, len(s), maxBytes))
	}
	return nil
}

// checkSlug enforces the open-enum shape shared by category and edge
// type: non-empty, lowercase [a-z0-9_]+, bounded.
func checkSlug(key string, v *anyenc.Value, maxBytes int) error {
	if err := checkString(key, v, maxBytes, false); err != nil {
		return err
	}
	s := v.GetStringBytes()
	for i := range s {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return rejectCreate(key + " must be a lowercase slug ([a-z0-9_]+)")
		}
	}
	return nil
}

func checkStringArray(key string, v *anyenc.Value, maxItems, maxItemBytes int) error {
	if v == nil || v.Type() != anyenc.TypeArray {
		return rejectCreate(key + " must be an array of strings")
	}
	items, err := v.Array()
	if err != nil {
		return rejectCreate(key + " must be an array of strings")
	}
	if len(items) > maxItems {
		return rejectCreate(fmt.Sprintf("%s: too many entries (%d > %d)", key, len(items), maxItems))
	}
	for i, item := range items {
		if item.Type() != anyenc.TypeString {
			return rejectCreate(fmt.Sprintf("%s[%d] must be a string", key, i))
		}
		if len(item.GetStringBytes()) > maxItemBytes {
			return rejectCreate(fmt.Sprintf("%s[%d] too long (> %d bytes)", key, i, maxItemBytes))
		}
	}
	return nil
}

// checkFloatRange: salience is CONTINUOUS (0..10) — the decay sweep
// writes half-life fractions; the other scores stay integer rubrics.
func checkFloatRange(key string, v *anyenc.Value, lo, hi float64) error {
	if v == nil || v.Type() != anyenc.TypeNumber {
		return rejectCreate(key + " must be a number")
	}
	n, err := v.Float64()
	if err != nil {
		return rejectCreate(key + " must be a number")
	}
	if n < lo || n > hi {
		return rejectCreate(fmt.Sprintf("%s must be in %v..%v", key, lo, hi))
	}
	return nil
}

func checkIntRange(key string, v *anyenc.Value, lo, hi int) error {
	if v == nil || v.Type() != anyenc.TypeNumber {
		return rejectCreate(key + " must be a number")
	}
	n, err := v.Int()
	if err != nil {
		return rejectCreate(key + " must be an integer")
	}
	if n < lo || n > hi {
		return rejectCreate(fmt.Sprintf("%s must be in %d..%d", key, lo, hi))
	}
	return nil
}

func checkNonNegNumber(key string, v *anyenc.Value) error {
	if v == nil || v.Type() != anyenc.TypeNumber {
		return rejectCreate(key + " must be a number")
	}
	f, err := v.Float64()
	if err != nil || f < 0 {
		return rejectCreate(key + " must be ≥ 0")
	}
	return nil
}

// --- stamping & authorization -------------------------------------------------

// stampCreate queues sink.Derive ops for the server-stamped fields and
// the absent defaultable ones.
func stampCreate(ctx *handler.ChangeCtx, sink *handler.Sink, present presentFields) {
	if ctx == nil || ctx.Change == nil || sink == nil {
		return
	}
	a := &anyenc.Arena{}
	derive := func(field string, payload *anyenc.Value) {
		sink.Derive(handler.Op{
			Type:    handler.OpSet,
			Path:    []string{field},
			Payload: payload,
		})
	}
	if c := ctx.Change.Creator; c != "" {
		derive(FieldCreator, a.NewString(c))
	}
	ts := ctx.Change.Timestamp
	if ts > 0 {
		derive(FieldCreatedAt, a.NewNumberInt(int(ts)))
		derive(FieldModifiedAt, a.NewNumberInt(int(ts)))
		if !present.validFrom {
			derive(FieldValidFrom, a.NewNumberInt(int(ts)))
		}
	}
	if !present.confidence {
		derive(FieldConfidence, a.NewNumberInt(DefaultConfidence))
	}
	if !present.importance {
		derive(FieldImportance, a.NewNumberInt(DefaultImportance))
	}
	if !present.salience {
		derive(FieldSalience, a.NewNumberInt(DefaultSalience))
	}
	if !present.accessCount {
		derive(FieldAccessCount, a.NewNumberInt(0))
	}
}

func bumpModifiedAt(ctx *handler.ChangeCtx, sink *handler.Sink) {
	if ctx == nil || ctx.Change == nil || sink == nil || ctx.Change.Timestamp <= 0 {
		return
	}
	a := &anyenc.Arena{}
	sink.Derive(handler.Op{
		Type:    handler.OpSet,
		Path:    []string{FieldModifiedAt},
		Payload: a.NewNumberInt(int(ctx.Change.Timestamp)),
	})
}

// isAuthor compares the record's creator with the change signer —
// fail-closed, same contract as chat.isAuthor.
func isAuthor(ctx *handler.ChangeCtx) bool {
	if ctx == nil || ctx.Change == nil || ctx.Before == nil {
		return false
	}
	signer := ctx.Change.Creator
	if signer == "" {
		return false
	}
	creator := ctx.Before.Get(FieldCreator)
	if creator == nil || creator.Type() != anyenc.TypeString {
		return false
	}
	return string(creator.GetStringBytes()) == signer
}

func pathString(path []string) string {
	if len(path) == 0 {
		return "<root>"
	}
	return strings.Join(path, ".")
}

func rejectCreate(msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("agent_memory.create: %s", msg))
}

func rejectRecord(msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("agent_memory: %s", msg))
}

func rejectOp(msg string) error {
	return errors.Join(handler.ErrValidation, fmt.Errorf("agent_memory: %s", msg))
}
