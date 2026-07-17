package chat

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

// hand-built crdt.Change — handler tests don't boot the SDK; they
// just need the metadata that ChangeCtx exposes.
func makeChange(creator string, ts int64) *handler.Change {
	return &handler.Change{
		SpaceId:   "space",
		ObjectId:  "obj",
		Dataset:   Dataset,
		ChangeId:  "ch1",
		VersionId: "v1",
		Creator:   creator,
		Timestamp: ts,
	}
}

// existingMessage builds the pre-state anyenc record passed via
// ChangeCtx.Before. Mirrors what BeforeCreate would have stamped.
func existingMessage(arena *anyenc.Arena, creator string, createdAt int64, text string) *anyenc.Value {
	rec := arena.NewObject()
	rec.Set("id", arena.NewString("msg1"))
	rec.Set(FieldCreator, arena.NewString(creator))
	rec.Set(FieldCreatedAt, arena.NewNumberInt(int(createdAt)))
	rec.Set(FieldModifiedAt, arena.NewNumberInt(int(createdAt)))
	rec.Set(FieldText, arena.NewString(text))
	return rec
}

// --- BeforeCreate ----------------------------------------------------------

func TestBeforeCreate_StampsServerFields(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := arena.NewObject()
	payload.Set(FieldText, arena.NewString("hello"))

	rec := &handler.RecordChange{
		Id:     "",
		Upsert: true,
		Ops: []handler.Op{{
			Type:    handler.OpSet,
			Path:    nil,
			Payload: payload,
		}},
	}
	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
	sink := &handler.Sink{}

	if err := (messagesHandler{}).BeforeCreate(ctx, rec, sink); err != nil {
		t.Fatalf("BeforeCreate: %v", err)
	}
	// We can't introspect Sink directly (private fields), but the
	// shape is exercised via the apply loop in the end-to-end tests.
	// Smoke that no error escapes.
}

func TestBeforeCreate_AcceptsAttachments(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := arena.NewObject()
	payload.Set(FieldText, arena.NewString("hello"))

	atts := arena.NewObject()
	one := arena.NewObject()
	one.Set(FieldAttachmentType, arena.NewString("link"))
	one.Set(FieldAttachmentLink, arena.NewString("any://abc/def"))
	atts.Set("a1", one)
	payload.Set(FieldAttachments, atts)

	rec := &handler.RecordChange{
		Id:     "",
		Upsert: true,
		Ops: []handler.Op{{
			Type:    handler.OpSet,
			Path:    nil,
			Payload: payload,
		}},
	}
	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
	if err := (messagesHandler{}).BeforeCreate(ctx, rec, &handler.Sink{}); err != nil {
		t.Fatalf("BeforeCreate with attachments: %v", err)
	}
}

// A message carrying attachments needs no text — a photo sent with no
// caption is ordinary messenger behaviour. Text stays required only for
// a message that would otherwise be empty; see TestBeforeCreate_Rejects
// ("empty text", "missing text"), which pins that half of the contract.
func TestBeforeCreate_AcceptsAttachmentOnlyMessage(t *testing.T) {
	withAttachment := func(a *anyenc.Arena, p *anyenc.Value) *anyenc.Value {
		atts := a.NewObject()
		one := a.NewObject()
		one.Set(FieldAttachmentType, a.NewString("image"))
		one.Set(FieldAttachmentLink, a.NewString("any://f/space/file1"))
		atts.Set("f0", one)
		p.Set(FieldAttachments, atts)
		return p
	}
	cases := []struct {
		name  string
		build func(a *anyenc.Arena) *handler.RecordChange
	}{
		{
			name: "empty text with attachment",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString(""))
				return setRoot(a, withAttachment(a, p))
			},
		},
		{
			name: "absent text with attachment",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				return setRoot(a, withAttachment(a, p))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arena := &anyenc.Arena{}
			ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
			if err := (messagesHandler{}).BeforeCreate(ctx, tc.build(arena), &handler.Sink{}); err != nil {
				t.Fatalf("BeforeCreate: %v", err)
			}
		})
	}
}

func TestBeforeCreate_AttachmentRejects(t *testing.T) {
	cases := []struct {
		name   string
		build  func(arena *anyenc.Arena) *handler.RecordChange
		wantIn string
	}{
		{
			name: "attachments not an object",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("x"))
				p.Set(FieldAttachments, a.NewString("nope"))
				return setRoot(a, p)
			},
			wantIn: "attachments must be an object",
		},
		{
			name: "attachment id with dot",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("x"))
				atts := a.NewObject()
				entry := a.NewObject()
				entry.Set(FieldAttachmentType, a.NewString("link"))
				entry.Set(FieldAttachmentLink, a.NewString("any://x"))
				atts.Set("a.bad", entry)
				p.Set(FieldAttachments, atts)
				return setRoot(a, p)
			},
			wantIn: "invalid id",
		},
		{
			name: "attachment missing link",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("x"))
				atts := a.NewObject()
				entry := a.NewObject()
				entry.Set(FieldAttachmentType, a.NewString("link"))
				atts.Set("a1", entry)
				p.Set(FieldAttachments, atts)
				return setRoot(a, p)
			},
			wantIn: "link required",
		},
		{
			name: "attachment unknown sub-field",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("x"))
				atts := a.NewObject()
				entry := a.NewObject()
				entry.Set(FieldAttachmentType, a.NewString("link"))
				entry.Set(FieldAttachmentLink, a.NewString("any://x"))
				entry.Set("ghost", a.NewString("hi"))
				atts.Set("a1", entry)
				p.Set(FieldAttachments, atts)
				return setRoot(a, p)
			},
			wantIn: "unknown field ghost",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arena := &anyenc.Arena{}
			rec := tc.build(arena)
			ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
			err := (messagesHandler{}).BeforeCreate(ctx, rec, &handler.Sink{})
			if err == nil {
				t.Fatalf("expected validation error containing %q", tc.wantIn)
			}
			if !errors.Is(err, handler.ErrValidation) {
				t.Errorf("err is not ErrValidation: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("err = %q, want contains %q", err.Error(), tc.wantIn)
			}
		})
	}
}

func TestBeforeCreate_AcceptsAgent(t *testing.T) {
	cases := []struct {
		name  string
		build func(a *anyenc.Arena, agent *anyenc.Value)
	}{
		{
			name: "full group",
			build: func(a *anyenc.Arena, agent *anyenc.Value) {
				agent.Set(FieldAgentName, a.NewString("bao"))
				agent.Set(FieldAgentDebugLink, a.NewString("any://sp/obj#turn_3"))
				agent.Set(FieldAgentDone, a.NewTrue())
			},
		},
		{
			name: "minimal — name + done only",
			build: func(a *anyenc.Arena, agent *anyenc.Value) {
				agent.Set(FieldAgentName, a.NewString("bao"))
				agent.Set(FieldAgentDone, a.NewFalse())
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arena := &anyenc.Arena{}
			payload := arena.NewObject()
			payload.Set(FieldText, arena.NewString("hello"))
			agent := arena.NewObject()
			tc.build(arena, agent)
			payload.Set(FieldAgent, agent)

			rec := &handler.RecordChange{
				Id:     "",
				Upsert: true,
				Ops: []handler.Op{{
					Type:    handler.OpSet,
					Path:    nil,
					Payload: payload,
				}},
			}
			ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
			if err := (messagesHandler{}).BeforeCreate(ctx, rec, &handler.Sink{}); err != nil {
				t.Fatalf("BeforeCreate with agent: %v", err)
			}
		})
	}
}

func TestBeforeCreate_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		build  func(arena *anyenc.Arena) *handler.RecordChange
		wantIn string
	}{
		{
			name: "empty text",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString(""))
				return setRoot(a, p)
			},
			wantIn: "text required",
		},
		{
			name: "missing text",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldReplyToMessageId, a.NewString("m"))
				return setRoot(a, p)
			},
			wantIn: "text required",
		},
		{
			name: "text not a string",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewNumberInt(42))
				return setRoot(a, p)
			},
			wantIn: "text must be a string",
		},
		{
			name: "text too long",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString(strings.Repeat("x", MaxTextBytes+1)))
				return setRoot(a, p)
			},
			wantIn: "text too long",
		},
		{
			name: "extra field — creator (spoof attempt)",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				p.Set(FieldCreator, a.NewString("evil"))
				return setRoot(a, p)
			},
			wantIn: "field_not_allowed: creator",
		},
		{
			name: "extra field — createdAt",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				p.Set(FieldCreatedAt, a.NewNumberInt(0))
				return setRoot(a, p)
			},
			wantIn: "field_not_allowed: createdAt",
		},
		{
			name: "extra field — reactions (cannot seed reactions on create)",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				seed := a.NewObject()
				perEmoji := a.NewObject()
				perEmoji.Set(alice, a.NewNumberInt(1700000000))
				seed.Set("👍", perEmoji)
				p.Set(FieldReactions, seed)
				return setRoot(a, p)
			},
			wantIn: "field_not_allowed: reactions",
		},
		{
			name: "fromAgent removed — unknown field",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				p.Set("fromAgent", a.NewString("bao"))
				return setRoot(a, p)
			},
			wantIn: "field_not_allowed: fromAgent",
		},
		{
			name: "agent not an object",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				p.Set(FieldAgent, a.NewString("bao"))
				return setRoot(a, p)
			},
			wantIn: "agent must be an object",
		},
		{
			name: "agent missing name",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				agent := a.NewObject()
				agent.Set(FieldAgentDone, a.NewTrue())
				p.Set(FieldAgent, agent)
				return setRoot(a, p)
			},
			wantIn: "agent.name required",
		},
		{
			name: "agent name empty",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				agent := a.NewObject()
				agent.Set(FieldAgentName, a.NewString(""))
				agent.Set(FieldAgentDone, a.NewTrue())
				p.Set(FieldAgent, agent)
				return setRoot(a, p)
			},
			wantIn: "agent.name required",
		},
		{
			name: "agent name too long",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				agent := a.NewObject()
				agent.Set(FieldAgentName, a.NewString(strings.Repeat("x", MaxAgentNameBytes+1)))
				agent.Set(FieldAgentDone, a.NewTrue())
				p.Set(FieldAgent, agent)
				return setRoot(a, p)
			},
			wantIn: "agent.name too long",
		},
		{
			name: "agent missing done",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				agent := a.NewObject()
				agent.Set(FieldAgentName, a.NewString("bao"))
				p.Set(FieldAgent, agent)
				return setRoot(a, p)
			},
			wantIn: "agent.done required",
		},
		{
			name: "agent done not a boolean",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				agent := a.NewObject()
				agent.Set(FieldAgentName, a.NewString("bao"))
				agent.Set(FieldAgentDone, a.NewNumberInt(1))
				p.Set(FieldAgent, agent)
				return setRoot(a, p)
			},
			wantIn: "agent.done must be a boolean",
		},
		{
			name: "agent debugLink empty",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				agent := a.NewObject()
				agent.Set(FieldAgentName, a.NewString("bao"))
				agent.Set(FieldAgentDone, a.NewTrue())
				agent.Set(FieldAgentDebugLink, a.NewString(""))
				p.Set(FieldAgent, agent)
				return setRoot(a, p)
			},
			wantIn: "agent.debugLink must be non-empty",
		},
		{
			name: "agent debugLink too long",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				agent := a.NewObject()
				agent.Set(FieldAgentName, a.NewString("bao"))
				agent.Set(FieldAgentDone, a.NewTrue())
				agent.Set(FieldAgentDebugLink, a.NewString(strings.Repeat("x", MaxDebugLinkBytes+1)))
				p.Set(FieldAgent, agent)
				return setRoot(a, p)
			},
			wantIn: "agent.debugLink too long",
		},
		{
			name: "agent unknown sub-field",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				p := a.NewObject()
				p.Set(FieldText, a.NewString("hi"))
				agent := a.NewObject()
				agent.Set(FieldAgentName, a.NewString("bao"))
				agent.Set(FieldAgentDone, a.NewTrue())
				agent.Set("ghost", a.NewString("hi"))
				p.Set(FieldAgent, agent)
				return setRoot(a, p)
			},
			wantIn: "agent: field_not_allowed: ghost",
		},
		{
			name: "non-set op",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				return &handler.RecordChange{
					Id:     "",
					Upsert: true,
					Ops: []handler.Op{{
						Type:    handler.OpAddToSet,
						Path:    []string{"x"},
						Payload: a.NewString("y"),
					}},
				}
			},
			wantIn: "expected multi-field $set",
		},
		{
			name: "two ops",
			build: func(a *anyenc.Arena) *handler.RecordChange {
				return &handler.RecordChange{
					Id:     "",
					Upsert: true,
					Ops: []handler.Op{
						{Type: handler.OpSet, Path: nil, Payload: a.NewObject()},
						{Type: handler.OpSet, Path: []string{"x"}, Payload: a.NewString("y")},
					},
				}
			},
			wantIn: "expected exactly one",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arena := &anyenc.Arena{}
			rec := tc.build(arena)
			ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000)}
			sink := &handler.Sink{}
			err := (messagesHandler{}).BeforeCreate(ctx, rec, sink)
			if err == nil {
				t.Fatalf("expected validation error containing %q", tc.wantIn)
			}
			if !errors.Is(err, handler.ErrValidation) {
				t.Errorf("err is not ErrValidation: %v", err)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("err = %q, want contains %q", err.Error(), tc.wantIn)
			}
		})
	}
}

func setRoot(a *anyenc.Arena, payload *anyenc.Value) *handler.RecordChange {
	return &handler.RecordChange{
		Id:     "",
		Upsert: true,
		Ops:    []handler.Op{{Type: handler.OpSet, Path: nil, Payload: payload}},
	}
}

// TestBeforeCreate_RejectsRemoteReactionSeed pins the invariant for
// peer-originated creates. BeforeCreate is the SDK's single apply-time
// gate (any-sync-sdk internal/crdt/controller.go: recordModifier.Modify
// calls handler.BeforeCreate on every new record), and that path runs
// identically for locally-issued writes and for inbound/remote changes
// replayed off the DAG — there is no origin flag on ChangeCtx and no
// separate apply route that skips the handler. So a malicious or buggy
// peer that signs a create payload pre-seeded with a reaction is
// rejected on every receiving peer, and the record never materialises
// locally.
//
// The reaction here is keyed to the change's OWN signer (bob reacting
// to bob's brand-new message) — the most innocuous-looking case, since
// it isn't forging someone else's slot. It's still rejected: reactions
// are post-create-only, addable solely through the toggle op that
// BeforeModify binds to the signer. Seeding any reaction at create —
// even a self-keyed one — would let an author fabricate reaction state
// in a single signed change, so the create allow-list bars the field
// outright.
func TestBeforeCreate_RejectsRemoteReactionSeed(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := arena.NewObject()
	payload.Set(FieldText, arena.NewString("hi from a peer"))
	perEmoji := arena.NewObject()
	perEmoji.Set(bob, arena.NewNumberInt(1700000000)) // signer's own slot
	seed := arena.NewObject()
	seed.Set("👍", perEmoji)
	payload.Set(FieldReactions, seed)
	rec := setRoot(arena, payload)

	// Change signed by bob, a remote peer — not the local account.
	ctx := &handler.ChangeCtx{Change: makeChange(bob, 1700000000)}
	err := (messagesHandler{}).BeforeCreate(ctx, rec, &handler.Sink{})
	if err == nil {
		t.Fatal("expected remote create with seeded reaction to be rejected")
	}
	if !errors.Is(err, handler.ErrValidation) {
		t.Errorf("err is not ErrValidation: %v", err)
	}
	if !strings.Contains(err.Error(), "field_not_allowed: reactions") {
		t.Errorf("err = %q, want contains %q", err.Error(), "field_not_allowed: reactions")
	}
}

// --- BeforeModify (text) ---------------------------------------------------

func TestBeforeModify_TextEdit_Author(t *testing.T) {
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "old")
	op := &handler.Op{
		Type:    handler.OpSet,
		Path:    []string{FieldText},
		Payload: arena.NewString("new"),
	}
	ctx := &handler.ChangeCtx{
		Change: makeChange(alice, 1700000050),
		Before: before,
	}
	sink := &handler.Sink{}
	if err := (messagesHandler{}).BeforeModify(ctx, nil, op, sink); err != nil {
		t.Fatalf("BeforeModify text by author: %v", err)
	}
}

func TestBeforeModify_TextEdit_NotAuthor(t *testing.T) {
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "old")
	op := &handler.Op{
		Type:    handler.OpSet,
		Path:    []string{FieldText},
		Payload: arena.NewString("new"),
	}
	ctx := &handler.ChangeCtx{
		Change: makeChange(bob, 1700000050),
		Before: before,
	}
	err := (messagesHandler{}).BeforeModify(ctx, nil, op, &handler.Sink{})
	if err == nil || !errors.Is(err, handler.ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
	if !strings.Contains(err.Error(), "not_author") {
		t.Errorf("err = %q, want not_author", err.Error())
	}
}

func TestBeforeModify_TextEdit_TooLong(t *testing.T) {
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "old")
	op := &handler.Op{
		Type:    handler.OpSet,
		Path:    []string{FieldText},
		Payload: arena.NewString(strings.Repeat("x", MaxTextBytes+1)),
	}
	ctx := &handler.ChangeCtx{
		Change: makeChange(alice, 1700000050),
		Before: before,
	}
	err := (messagesHandler{}).BeforeModify(ctx, nil, op, &handler.Sink{})
	if err == nil || !strings.Contains(err.Error(), "text too long") {
		t.Fatalf("expected text-too-long rejection, got %v", err)
	}
}

// --- BeforeModify (reactions) ---------------------------------------------

func TestBeforeModify_Reaction_OwnSlot(t *testing.T) {
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "msg")
	op := &handler.Op{
		Type:    handler.OpSet,
		Path:    []string{FieldReactions, "👍", bob},
		Payload: arena.NewNumberInt(0),
	}
	// bob is reacting; path[2] == change.Creator. Allowed.
	ctx := &handler.ChangeCtx{
		Change: makeChange(bob, 1700000050),
		Before: before,
	}
	if err := (messagesHandler{}).BeforeModify(ctx, nil, op, &handler.Sink{}); err != nil {
		t.Fatalf("react own slot: %v", err)
	}
	// The client's placeholder 0 must be overwritten in place with the
	// change timestamp — a same-path derived op would be dropped by the
	// CRDT version gate, so the user op itself has to carry the value.
	if op.Payload == nil || op.Payload.Type() != anyenc.TypeNumber {
		t.Fatalf("react own slot: payload not rewritten, got %v", op.Payload)
	}
	if got := op.Payload.GetInt(); got != 1700000050 {
		t.Fatalf("react own slot: payload = %d, want change timestamp 1700000050", got)
	}
}

func TestBeforeModify_Reaction_Unset(t *testing.T) {
	// $unset on the caller's own leaf is the toggle-off path; no
	// payload required.
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "msg")
	op := &handler.Op{
		Type: handler.OpUnset,
		Path: []string{FieldReactions, "👍", bob},
	}
	ctx := &handler.ChangeCtx{
		Change: makeChange(bob, 1700000050),
		Before: before,
	}
	if err := (messagesHandler{}).BeforeModify(ctx, nil, op, &handler.Sink{}); err != nil {
		t.Fatalf("react unset own slot: %v", err)
	}
	_ = arena
}

func TestBeforeModify_Reaction_ForeignSlot(t *testing.T) {
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "msg")
	op := &handler.Op{
		Type:    handler.OpSet,
		Path:    []string{FieldReactions, "👍", alice}, // bob trying to add for alice
		Payload: arena.NewNumberInt(0),
	}
	ctx := &handler.ChangeCtx{
		Change: makeChange(bob, 1700000050),
		Before: before,
	}
	err := (messagesHandler{}).BeforeModify(ctx, nil, op, &handler.Sink{})
	if err == nil || !strings.Contains(err.Error(), "not_own_reaction_key") {
		t.Fatalf("expected not_own_reaction_key, got %v", err)
	}
}

func TestBeforeModify_Reaction_AddToSetRejected(t *testing.T) {
	// Only $set / $unset on the emoji.identity leaf are reactions;
	// $addToSet on the old identity-rooted path is no longer a
	// reaction shape and falls through to field_not_modifiable.
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "msg")
	op := &handler.Op{
		Type:    handler.OpAddToSet,
		Path:    []string{FieldReactions, bob},
		Payload: arena.NewString("👍"),
	}
	ctx := &handler.ChangeCtx{
		Change: makeChange(bob, 1700000050),
		Before: before,
	}
	err := (messagesHandler{}).BeforeModify(ctx, nil, op, &handler.Sink{})
	if err == nil || !strings.Contains(err.Error(), "field_not_modifiable") {
		t.Fatalf("expected field_not_modifiable on legacy $addToSet, got %v", err)
	}
}

func TestBeforeModify_Reaction_EmojiTooLong(t *testing.T) {
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "msg")
	op := &handler.Op{
		Type:    handler.OpSet,
		Path:    []string{FieldReactions, strings.Repeat("x", MaxEmojiBytes+1), bob},
		Payload: arena.NewNumberInt(0),
	}
	ctx := &handler.ChangeCtx{
		Change: makeChange(bob, 1700000050),
		Before: before,
	}
	err := (messagesHandler{}).BeforeModify(ctx, nil, op, &handler.Sink{})
	if err == nil || !strings.Contains(err.Error(), "emoji too long") {
		t.Fatalf("expected emoji too long, got %v", err)
	}
}

// --- BeforeModify (disallowed paths) --------------------------------------

func TestBeforeModify_DisallowedPath(t *testing.T) {
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "msg")
	cases := [][]string{
		{FieldCreator},
		{FieldCreatedAt},
		{FieldModifiedAt},
		{FieldReplyToMessageId},
		{FieldAgent},
		{FieldAgent, FieldAgentDone},
		{"_ver", "id"},
		{"_deletedAt"},
		{"unknown"},
	}
	for _, path := range cases {
		t.Run(strings.Join(path, "."), func(t *testing.T) {
			op := &handler.Op{
				Type:    handler.OpSet,
				Path:    path,
				Payload: arena.NewString("x"),
			}
			ctx := &handler.ChangeCtx{
				Change: makeChange(alice, 1700000050),
				Before: before,
			}
			err := (messagesHandler{}).BeforeModify(ctx, nil, op, &handler.Sink{})
			if err == nil || !strings.Contains(err.Error(), "field_not_modifiable") {
				t.Errorf("path %v: expected field_not_modifiable, got %v", path, err)
			}
		})
	}
}

// --- BeforeDelete ---------------------------------------------------------

func TestBeforeDelete_Author(t *testing.T) {
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "msg")
	ctx := &handler.ChangeCtx{
		Change: makeChange(alice, 1700000100),
		Before: before,
	}
	if err := (messagesHandler{}).BeforeDelete(ctx, nil, &handler.Sink{}); err != nil {
		t.Fatalf("delete by author: %v", err)
	}
}

func TestBeforeDelete_NotAuthor(t *testing.T) {
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "msg")
	ctx := &handler.ChangeCtx{
		Change: makeChange(bob, 1700000100),
		Before: before,
	}
	err := (messagesHandler{}).BeforeDelete(ctx, nil, &handler.Sink{})
	if err == nil || !errors.Is(err, handler.ErrValidation) ||
		!strings.Contains(err.Error(), "not_author") {
		t.Fatalf("expected not_author rejection, got %v", err)
	}
}
