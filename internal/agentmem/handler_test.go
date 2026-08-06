package agentmem

import (
	"errors"
	"strings"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/handler"
)

const (
	alice = "alice"
	bob   = "bob"
)

func makeChange(creator string, ts int64) *handler.Change {
	return &handler.Change{
		SpaceId:   "space",
		ObjectId:  "brain",
		Dataset:   Dataset,
		ChangeId:  "ch1",
		VersionId: "v1",
		Creator:   creator,
		Timestamp: ts,
	}
}

func createRec(payload *anyenc.Value) *handler.RecordChange {
	return &handler.RecordChange{
		Id:     "",
		Upsert: true,
		Ops: []handler.Op{{
			Type:    handler.OpSet,
			Path:    nil,
			Payload: payload,
		}},
	}
}

func newStringArray(arena *anyenc.Arena, items ...string) *anyenc.Value {
	arr := arena.NewArray()
	for i, s := range items {
		arr.SetArrayItem(i, arena.NewString(s))
	}
	return arr
}

// existingItem builds the pre-state record passed via ChangeCtx.Before.
func existingItem(arena *anyenc.Arena, creator string) *anyenc.Value {
	rec := arena.NewObject()
	rec.Set("id", arena.NewString("item1"))
	rec.Set(FieldCreator, arena.NewString(creator))
	rec.Set(FieldCategory, arena.NewString("preference"))
	rec.Set(FieldContext, arena.NewString("user prefers dark mode"))
	return rec
}

func minimalItem(arena *anyenc.Arena) *anyenc.Value {
	payload := arena.NewObject()
	payload.Set(FieldCategory, arena.NewString("preference"))
	payload.Set(FieldContext, arena.NewString("user prefers dark mode"))
	return payload
}

// --- BeforeCreate ------------------------------------------------------------

func TestCreate_Minimal(t *testing.T) {
	arena := &anyenc.Arena{}
	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
	if err := (itemsHandler{}).BeforeCreate(ctx, createRec(minimalItem(arena)), &handler.Sink{}); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
}

func TestCreate_FullPayload(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalItem(arena)
	payload.Set(FieldBody, arena.NewString("# Dark mode\nalways."))
	payload.Set(FieldFromAgent, arena.NewString("bobrik"))
	payload.Set(FieldChatId, arena.NewString("chat1"))
	payload.Set(FieldTags, newStringArray(arena, "ui", "style"))
	payload.Set(FieldEntities, newStringArray(arena, "dark mode"))
	payload.Set(FieldKeywords, newStringArray(arena, "theme", "ui"))
	payload.Set(FieldConfidence, arena.NewNumberInt(8))
	payload.Set(FieldImportance, arena.NewNumberInt(7))
	payload.Set(FieldSalience, arena.NewNumberInt(10))
	payload.Set(FieldAccessCount, arena.NewNumberInt(0))
	payload.Set(FieldValidFrom, arena.NewNumberInt(1700000000))
	edges := arena.NewArray()
	edge := arena.NewObject()
	edge.Set(FieldEdgeTo, arena.NewString("item2"))
	edge.Set(FieldEdgeType, arena.NewString("related_to"))
	edge.Set(FieldEdgeStrength, arena.NewNumberFloat64(0.8))
	edges.SetArrayItem(0, edge)
	payload.Set(FieldEdges, edges)

	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
	if err := (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{}); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
}

func TestCreate_SourceAndProvenance(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalItem(arena)
	payload.Set(FieldSource, arena.NewString("extraction"))
	prov := arena.NewObject()
	prov.Set(FieldProvFromSeq, arena.NewNumberInt(42))
	payload.Set(FieldProvenance, prov)
	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
	if err := (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{}); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
}

func TestCreate_BadProvenanceRejected(t *testing.T) {
	// Unknown subkey — the provenance subkey set is closed.
	arena := &anyenc.Arena{}
	payload := minimalItem(arena)
	prov := arena.NewObject()
	prov.Set("url", arena.NewString("https://example.com"))
	payload.Set(FieldProvenance, prov)
	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
	err := (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{})
	requireValidationErr(t, err, "provenance: field_not_allowed: url")

	// Non-object provenance.
	arena = &anyenc.Arena{}
	payload = minimalItem(arena)
	payload.Set(FieldProvenance, arena.NewString("turn 42"))
	err = (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{})
	requireValidationErr(t, err, "provenance must be an object")
}

func TestCreate_MissingCategoryRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := arena.NewObject()
	payload.Set(FieldContext, arena.NewString("ctx"))
	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
	err := (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{})
	requireValidationErr(t, err, "category required")
}

func TestCreate_MissingContextRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := arena.NewObject()
	payload.Set(FieldCategory, arena.NewString("claim"))
	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
	err := (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{})
	requireValidationErr(t, err, "context required")
}

func TestCreate_BadCategorySlugRejected(t *testing.T) {
	for _, bad := range []string{"Has Caps", "dash-ed", "dot.ted"} {
		arena := &anyenc.Arena{}
		payload := minimalItem(arena)
		payload.Set(FieldCategory, arena.NewString(bad))
		ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
		err := (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{})
		requireValidationErr(t, err, "lowercase slug")
	}
}

func TestCreate_CustomCategoryAllowed(t *testing.T) {
	// The category set is OPEN — agents may invent slugs beyond the
	// documented builtins.
	arena := &anyenc.Arena{}
	payload := minimalItem(arena)
	payload.Set(FieldCategory, arena.NewString("codegen_lesson"))
	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
	if err := (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{}); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
}

func TestCreate_StampedAndReservedFieldsRejected(t *testing.T) {
	for _, field := range []string{FieldCreator, FieldCreatedAt, FieldModifiedAt, "embeddingRef", "vector"} {
		arena := &anyenc.Arena{}
		payload := minimalItem(arena)
		payload.Set(field, arena.NewString("spoof"))
		ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
		err := (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{})
		requireValidationErr(t, err, "field_not_allowed: "+field)
	}
}

func TestCreate_ConfidenceOutOfRangeRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := minimalItem(arena)
	payload.Set(FieldConfidence, arena.NewNumberInt(11))
	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
	err := (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{})
	requireValidationErr(t, err, "confidence must be in 0..10")
}

func TestCreate_EdgeValidation(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(arena *anyenc.Arena, edge *anyenc.Value)
		contains string
	}{
		{"missing to", func(a *anyenc.Arena, e *anyenc.Value) {
			e.Del(FieldEdgeTo)
		}, "edges[0].to required"},
		{"missing type", func(a *anyenc.Arena, e *anyenc.Value) {
			e.Del(FieldEdgeType)
		}, "edges[0].type required"},
		{"bad strength", func(a *anyenc.Arena, e *anyenc.Value) {
			e.Set(FieldEdgeStrength, a.NewNumberFloat64(1.5))
		}, "strength must be in 0..1"},
		{"unknown field", func(a *anyenc.Arena, e *anyenc.Value) {
			e.Set("weight", a.NewNumberFloat64(0.5))
		}, "unknown field weight"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arena := &anyenc.Arena{}
			payload := minimalItem(arena)
			edge := arena.NewObject()
			edge.Set(FieldEdgeTo, arena.NewString("item2"))
			edge.Set(FieldEdgeType, arena.NewString("related_to"))
			tc.mutate(arena, edge)
			edges := arena.NewArray()
			edges.SetArrayItem(0, edge)
			payload.Set(FieldEdges, edges)
			ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
			err := (itemsHandler{}).BeforeCreate(ctx, createRec(payload), &handler.Sink{})
			requireValidationErr(t, err, tc.contains)
		})
	}
}

// --- BeforeModify ------------------------------------------------------------

func modifyCtx(arena *anyenc.Arena, signer, recordCreator string) *handler.ChangeCtx {
	return &handler.ChangeCtx{
		Change: makeChange(signer, 1700000100),
		Before: existingItem(arena, recordCreator),
	}
}

func TestModify_MutableFieldsByAuthor(t *testing.T) {
	arena := &anyenc.Arena{}
	cases := []struct {
		field   string
		payload *anyenc.Value
	}{
		{FieldSalience, arena.NewNumberInt(3)},
		{FieldAccessCount, arena.NewNumberInt(7)},
		{FieldConfidence, arena.NewNumberInt(9)},
		{FieldImportance, arena.NewNumberInt(2)},
		{FieldContext, arena.NewString("updated context")},
		{FieldBody, arena.NewString("updated body")},
		{FieldTags, newStringArray(arena, "new_tag")},
	}
	for _, tc := range cases {
		op := &handler.Op{Type: handler.OpSet, Path: []string{tc.field}, Payload: tc.payload}
		err := (itemsHandler{}).BeforeModify(modifyCtx(arena, alice, alice), nil, op, &handler.Sink{})
		if err != nil {
			t.Fatalf("modify %s: %v", tc.field, err)
		}
	}
}

func TestModify_ImmutableFieldsRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	for _, field := range []string{FieldCategory, FieldCreator, FieldCreatedAt, FieldChatId, FieldEntities, FieldKeywords, FieldValidFrom, FieldFromAgent} {
		op := &handler.Op{Type: handler.OpSet, Path: []string{field}, Payload: arena.NewString("x")}
		err := (itemsHandler{}).BeforeModify(modifyCtx(arena, alice, alice), nil, op, &handler.Sink{})
		requireValidationErr(t, err, "field_not_modifiable: "+field)
	}
}

func TestModify_NonAuthorRejected(t *testing.T) {
	arena := &anyenc.Arena{}
	op := &handler.Op{Type: handler.OpSet, Path: []string{FieldSalience}, Payload: arena.NewNumberInt(1)}
	err := (itemsHandler{}).BeforeModify(modifyCtx(arena, bob, alice), nil, op, &handler.Sink{})
	requireValidationErr(t, err, "not_author")
}

func TestModify_RangeEnforced(t *testing.T) {
	arena := &anyenc.Arena{}
	op := &handler.Op{Type: handler.OpSet, Path: []string{FieldImportance}, Payload: arena.NewNumberInt(0)}
	err := (itemsHandler{}).BeforeModify(modifyCtx(arena, alice, alice), nil, op, &handler.Sink{})
	requireValidationErr(t, err, "importance must be in 1..10")
}

// --- BeforeDelete ------------------------------------------------------------

func TestDelete_AuthorOnly(t *testing.T) {
	arena := &anyenc.Arena{}
	if err := (itemsHandler{}).BeforeDelete(modifyCtx(arena, alice, alice), nil, &handler.Sink{}); err != nil {
		t.Fatalf("author delete: %v", err)
	}
	err := (itemsHandler{}).BeforeDelete(modifyCtx(arena, bob, alice), nil, &handler.Sink{})
	requireValidationErr(t, err, "not_author")
}

// --- shared ------------------------------------------------------------------

func requireValidationErr(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error containing %q, got nil", contains)
	}
	if !errors.Is(err, handler.ErrValidation) {
		t.Fatalf("expected handler.ErrValidation, got: %v", err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err.Error(), contains)
	}
}
