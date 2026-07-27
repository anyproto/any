package editor

import (
	"errors"
	"strings"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/handler"
)

// hand-built crdt.Change — handler tests don't boot the SDK; they
// just need the metadata that ChangeCtx exposes.
func makeChange(creator string) *handler.Change {
	return &handler.Change{
		SpaceId:  "space",
		ObjectId: "obj",
		Dataset:  Dataset,
		ChangeId: "ch1",
		Creator:  creator,
	}
}

// validCreate builds a minimally-valid create payload (type +
// nav.pos) so tests can flip individual fields and assert reject
// reasons without re-deriving the rest each time.
func validCreate(arena *anyenc.Arena, mutate func(*anyenc.Value)) *handler.RecordChange {
	payload := arena.NewObject()
	payload.Set(FieldType, arena.NewString(TypeParagraph))
	nav := arena.NewObject()
	nav.Set(NavParentId, arena.NewString(""))
	nav.Set(NavPos, arena.NewString("MMMM"))
	payload.Set(FieldNav, nav)
	if mutate != nil {
		mutate(payload)
	}
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

func TestBeforeCreate_Accepts(t *testing.T) {
	arena := &anyenc.Arena{}
	rec := validCreate(arena, func(p *anyenc.Value) {
		p.Set(FieldText, arena.NewString("hello"))
		style := arena.NewObject()
		style.Set(StyleLevel, arena.NewNumberInt(2))
		p.Set(FieldStyle, style)
	})
	ctx := &handler.ChangeCtx{Change: makeChange("alice")}
	sink := &handler.Sink{}
	if err := (blocksHandler{}).BeforeCreate(ctx, rec, sink); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
}

func TestBeforeCreate_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		build   func(arena *anyenc.Arena) *handler.RecordChange
		wantSub string
	}{
		{
			name: "missing type",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				nav := a.NewObject()
				nav.Set(NavParentId, a.NewString(""))
				nav.Set(NavPos, a.NewString("MMMM"))
				p.Set(FieldNav, nav)
				return &handler.RecordChange{
					Id:     "",
					Upsert: true,
					Ops: []handler.Op{{
						Type:    handler.OpSet,
						Path:    nil,
						Payload: p,
					}},
				}
			},
			wantSub: "type required",
		},
		{
			name: "empty type string",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				return validCreate(a, func(p *anyenc.Value) {
					p.Set(FieldType, a.NewString(""))
				})
			},
			wantSub: "type required",
		},
		{
			name: "type not a string",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				return validCreate(a, func(p *anyenc.Value) {
					p.Set(FieldType, a.NewNumberInt(42))
				})
			},
			wantSub: "type must be a string",
		},
		{
			name: "missing nav.pos",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				return validCreate(a, func(p *anyenc.Value) {
					nav := a.NewObject()
					nav.Set(NavParentId, a.NewString(""))
					p.Set(FieldNav, nav)
				})
			},
			wantSub: "nav.pos required",
		},
		{
			name: "unknown root key",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				return validCreate(a, func(p *anyenc.Value) {
					p.Set("creator", a.NewString("alice"))
				})
			},
			wantSub: "field_not_allowed: creator",
		},
		{
			name: "wrong op shape (path on $set)",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				return &handler.RecordChange{
					Id:     "",
					Upsert: true,
					Ops: []handler.Op{{
						Type:    handler.OpSet,
						Path:    []string{FieldType},
						Payload: a.NewString(TypeParagraph),
					}},
				}
			},
			wantSub: "expected multi-field $set with empty path",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arena := &anyenc.Arena{}
			rec := tc.build(arena)
			ctx := &handler.ChangeCtx{Change: makeChange("alice")}
			err := (blocksHandler{}).BeforeCreate(ctx, rec, &handler.Sink{})
			if err == nil {
				t.Fatalf("BeforeCreate: expected error, got nil")
			}
			if !errors.Is(err, handler.ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q missing substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestBeforeModify_AllowList(t *testing.T) {
	arena := &anyenc.Arena{}
	cases := []struct {
		name    string
		op      handler.Op
		wantErr bool
	}{
		{
			name: "set text",
			op: handler.Op{
				Type:    handler.OpSet,
				Path:    []string{FieldText},
				Payload: arena.NewString("hello"),
			},
		},
		{
			name: "set type",
			op: handler.Op{
				Type:    handler.OpSet,
				Path:    []string{FieldType},
				Payload: arena.NewString(TypeHeading),
			},
		},
		{
			name: "set style.level",
			op: handler.Op{
				Type:    handler.OpSet,
				Path:    []string{FieldStyle, StyleLevel},
				Payload: arena.NewNumberInt(3),
			},
		},
		{
			name: "set nav.pos",
			op: handler.Op{
				Type:    handler.OpSet,
				Path:    []string{FieldNav, NavPos},
				Payload: arena.NewString("MMMN"),
			},
		},
		{
			name: "unset text",
			op:   handler.Op{Type: handler.OpUnset, Path: []string{FieldText}},
		},
		{
			name: "unset style.level",
			op:   handler.Op{Type: handler.OpUnset, Path: []string{FieldStyle, StyleLevel}},
		},
		{
			name:    "set unknown path",
			op:      handler.Op{Type: handler.OpSet, Path: []string{"creator"}, Payload: arena.NewString("alice")},
			wantErr: true,
		},
		{
			name:    "unset required nav.pos",
			op:      handler.Op{Type: handler.OpUnset, Path: []string{FieldNav, NavPos}},
			wantErr: true,
		},
		{
			name:    "unset required type",
			op:      handler.Op{Type: handler.OpUnset, Path: []string{FieldType}},
			wantErr: true,
		},
		{
			name:    "unsupported op type",
			op:      handler.Op{Type: handler.OpAddToSet, Path: []string{FieldText}, Payload: arena.NewString("x")},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := tc.op
			err := (blocksHandler{}).BeforeModify(
				&handler.ChangeCtx{Change: makeChange("alice")},
				&handler.RecordChange{},
				&op,
				&handler.Sink{},
			)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBeforeDelete_Accepts(t *testing.T) {
	err := (blocksHandler{}).BeforeDelete(
		&handler.ChangeCtx{Change: makeChange("alice")},
		&handler.RecordChange{},
		&handler.Sink{},
	)
	if err != nil {
		t.Fatalf("BeforeDelete: %v", err)
	}
}

func TestAllocateRun_Monotonic(t *testing.T) {
	ids, err := AllocateRun("", "", 5)
	if err != nil {
		t.Fatalf("AllocateRun: %v", err)
	}
	if len(ids) != 5 {
		t.Fatalf("got %d ids, want 5", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("non-monotonic: %q >= %q", ids[i-1], ids[i])
		}
	}
}
