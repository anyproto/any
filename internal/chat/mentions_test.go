package chat

import (
	"errors"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-sync-sdk/handler"
)

// fakeGetter returns a ChangeCtx.Get closure backed by a map of
// records — the unit-test stand-in for the SDK's in-tx point read.
func fakeGetter(records map[string]*anyenc.Value) func(dataset, id string) *anyenc.Value {
	return func(dataset, id string) *anyenc.Value {
		if dataset != Dataset {
			return nil
		}
		return records[id]
	}
}

func mentionLink(identity string) string {
	return "[x](any://m/space1/" + identity + ")"
}

// --- classifyRead: mention tagging ------------------------------------------

func classifyCtx(self, recordId string, records map[string]*anyenc.Value) *handler.ChangeCtx {
	return &handler.ChangeCtx{
		SelfIdentity: self,
		RecordId:     recordId,
		Get:          fakeGetter(records),
	}
}

func createChange(id string) *handler.RecordChange {
	return &handler.RecordChange{
		Id:     id,
		Upsert: true,
		Ops:    []handler.Op{{Type: handler.OpSet, Path: nil}},
	}
}

func textEditChange(id string) *handler.RecordChange {
	return &handler.RecordChange{
		Id:  id,
		Ops: []handler.Op{{Type: handler.OpSet, Path: []string{FieldText}}},
	}
}

func TestClassifyRead_CreateMentioningSelf(t *testing.T) {
	records := map[string]*anyenc.Value{
		"m1": anyenc.MustParseJson(`{"id":"m1","creator":"acc-b","text":"hi","mentions":["acc-me"]}`),
	}
	cl := classifyRead(classifyCtx("acc-me", "m1", records), createChange("m1"))
	if !cl.Track {
		t.Fatalf("verdict untracked: %+v", cl)
	}
	if len(cl.Tags) != 2 || cl.Tags[0] != TagMessage || cl.Tags[1] != TagMention {
		t.Fatalf("tags = %v, want [message mention]", cl.Tags)
	}
	if cl.Key != "" {
		t.Errorf("create verdict must stay keyless (an edit must never clear it), got %q", cl.Key)
	}
}

func TestClassifyRead_CreateNotMentioningSelf(t *testing.T) {
	records := map[string]*anyenc.Value{
		"m1": anyenc.MustParseJson(`{"id":"m1","creator":"acc-b","text":"hi","mentions":["acc-other"]}`),
		"m2": anyenc.MustParseJson(`{"id":"m2","creator":"acc-b","text":"hi"}`),
	}
	for _, id := range []string{"m1", "m2"} {
		cl := classifyRead(classifyCtx("acc-me", id, records), createChange(id))
		if !cl.Track || len(cl.Tags) != 1 || cl.Tags[0] != TagMessage {
			t.Fatalf("%s: verdict = %+v, want tracked [message]", id, cl)
		}
	}
}

// A hand-built ctx with no getter (or no record) must degrade to
// message-only — never panic, never badge.
func TestClassifyRead_NilGetterDegrades(t *testing.T) {
	for _, ctx := range []*handler.ChangeCtx{
		{SelfIdentity: "acc-me", RecordId: "m1"},                         // no getter
		{SelfIdentity: "acc-me", Get: fakeGetter(nil)},                   // no record id
		{SelfIdentity: "", RecordId: "m1", Get: fakeGetter(nil)},         // no self
		{SelfIdentity: "acc-me", RecordId: "gone", Get: fakeGetter(nil)}, // absent record
	} {
		cl := classifyRead(ctx, createChange("m1"))
		if !cl.Track || len(cl.Tags) != 1 || cl.Tags[0] != TagMessage {
			t.Fatalf("verdict = %+v, want tracked [message]", cl)
		}
	}
}

func TestClassifyRead_TextEditMentionKey(t *testing.T) {
	records := map[string]*anyenc.Value{
		"m1": anyenc.MustParseJson(`{"id":"m1","creator":"acc-b","text":"hi","mentions":["acc-me"]}`),
	}

	// Edit whose re-derived mentions include self: tracked mention with
	// a supersede key, and deliberately NO message tag.
	cl := classifyRead(classifyCtx("acc-me", "m1", records), textEditChange("m1"))
	if !cl.Track || len(cl.Tags) != 1 || cl.Tags[0] != TagMention {
		t.Fatalf("edit verdict = %+v, want tracked [mention]", cl)
	}
	if cl.Key == "" {
		t.Fatalf("edit mention verdict missing supersede key")
	}

	// Edit that no longer mentions self: untracked, same key — clears
	// the previously edit-tracked entry.
	records["m1"] = anyenc.MustParseJson(`{"id":"m1","creator":"acc-b","text":"hi"}`)
	uncl := classifyRead(classifyCtx("acc-me", "m1", records), textEditChange("m1"))
	if uncl.Track {
		t.Errorf("mention-less edit tracked: %+v", uncl)
	}
	if uncl.Key != cl.Key {
		t.Errorf("clear key %q != track key %q — supersede would miss", uncl.Key, cl.Key)
	}
}

// A crafted multi-op change must not shadow the mention verdict: an
// author bundling a reaction toggle with a mention-adding text edit
// (only reachable via the generic /modify) must still badge the
// mentioned account, regardless of op order. The reaction verdict is
// the one to yield — a valid text edit is author-only, so the bundled
// reaction targets the author's own message and its audience (the
// author) is born read anyway.
func TestClassifyRead_ReactionCannotShadowMentionEdit(t *testing.T) {
	records := map[string]*anyenc.Value{
		"m1": anyenc.MustParseJson(`{"id":"m1","creator":"acc-b","text":"hi","mentions":["acc-me"]}`),
	}
	reaction := handler.Op{Type: handler.OpSet, Path: []string{FieldReactions, "x", "acc-b"}}
	edit := handler.Op{Type: handler.OpSet, Path: []string{FieldText}}

	for name, ops := range map[string][]handler.Op{
		"reaction first": {reaction, edit},
		"edit first":     {edit, reaction},
	} {
		rec := &handler.RecordChange{Id: "m1", Ops: ops}
		cl := classifyRead(classifyCtx("acc-me", "m1", records), rec)
		if !cl.Track || len(cl.Tags) != 1 || cl.Tags[0] != TagMention {
			t.Fatalf("%s: verdict = %+v, want tracked [mention]", name, cl)
		}
	}

	// Without a self-mention the reaction verdict resumes priority.
	records["m1"] = anyenc.MustParseJson(`{"id":"m1","creator":"acc-b","text":"hi"}`)
	cl := classifyRead(classifyCtx("acc-me", "m1", records),
		&handler.RecordChange{Id: "m1", Ops: []handler.Op{reaction, edit}})
	if !cl.Track || len(cl.Tags) != 1 || cl.Tags[0] != TagReaction {
		t.Fatalf("no-mention bundle: verdict = %+v, want tracked [reaction]", cl)
	}
}

// --- BeforeCreate / BeforeModify: client-supplied mentions rejected ---------

func TestBeforeCreate_RejectsClientMentions(t *testing.T) {
	arena := &anyenc.Arena{}
	payload := arena.NewObject()
	payload.Set(FieldText, arena.NewString("hello"))
	arr := arena.NewArray()
	arr.SetArrayItem(0, arena.NewString("acc-x"))
	payload.Set(FieldMentions, arr)

	rec := &handler.RecordChange{Upsert: true, Ops: []handler.Op{{Type: handler.OpSet, Payload: payload}}}
	err := (messagesHandler{}).BeforeCreate(&handler.ChangeCtx{Change: makeChange(alice, 1700000000)}, rec, &handler.Sink{})
	if err == nil || !errors.Is(err, handler.ErrValidation) {
		t.Fatalf("client-supplied mentions must reject with ErrValidation, got %v", err)
	}
}

func TestBeforeModify_RejectsMentionsWrite(t *testing.T) {
	arena := &anyenc.Arena{}
	before := existingMessage(arena, alice, 1700000000, "hi")
	arr := arena.NewArray()
	arr.SetArrayItem(0, arena.NewString("acc-x"))
	op := &handler.Op{Type: handler.OpSet, Path: []string{FieldMentions}, Payload: arr}

	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000001), Before: before}
	err := (messagesHandler{}).BeforeModify(ctx, &handler.RecordChange{Id: "msg1"}, op, &handler.Sink{})
	if err == nil || !errors.Is(err, handler.ErrValidation) {
		t.Fatalf("$set mentions must reject with ErrValidation, got %v", err)
	}
}

// --- deriveMentions: reply fold-in via ctx.Get ------------------------------

// deriveMentions must not error/panic across the getter edge cases;
// the derived VALUES are asserted end-to-end at the server layer
// (handlers_chat_test.go) since Sink internals are private.
func TestDeriveMentions_GetterEdgeCases(t *testing.T) {
	records := map[string]*anyenc.Value{
		"orig":   anyenc.MustParseJson(`{"id":"orig","creator":"acc-author","text":"hi"}`),
		"noauth": anyenc.MustParseJson(`{"id":"noauth","text":"hi"}`),
		"tomb":   anyenc.MustParseJson(`{"id":"tomb","_deletedAt":123}`),
	}
	ctx := &handler.ChangeCtx{Change: makeChange(alice, 1700000000), Get: fakeGetter(records)}

	for _, tc := range []struct {
		name    string
		text    string
		replyTo string
	}{
		{"text mention + reply", "hey " + mentionLink("acc-x"), "orig"},
		{"reply to tombstone", "hey", "tomb"},
		{"reply to absent", "hey", "gone"},
		{"reply to creator-less record", "hey", "noauth"},
		{"reply author already mentioned", mentionLink("acc-author"), "orig"},
		{"no mentions at all", "hey", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deriveMentions(ctx, &handler.Sink{}, []byte(tc.text), tc.replyTo, false)
			deriveMentions(ctx, &handler.Sink{}, []byte(tc.text), tc.replyTo, true)
		})
	}

	// Nil getter: text-only derivation, no panic.
	deriveMentions(&handler.ChangeCtx{Change: makeChange(alice, 1)}, &handler.Sink{}, []byte("x"), "orig", false)
	// Nil sink: no-op.
	deriveMentions(ctx, nil, []byte("x"), "orig", false)
}
