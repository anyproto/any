package chat

import (
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-sync-sdk/handler"
)

// reactionSet builds the RecordChange for a reaction toggle on msgId:
// $set (on=true) or $unset of reactions.<emoji>.<accountId>.
func reactionSet(msgId, emoji, accountId string, on bool) *handler.RecordChange {
	opType := handler.OpUnset
	if on {
		opType = handler.OpSet
	}
	return &handler.RecordChange{
		Id:  msgId,
		Ops: []handler.Op{{Type: opType, Path: []string{FieldReactions, emoji, accountId}}},
	}
}

// TestClassifyReadReactionAudience pins the audience rule: a reaction
// verdict carries a filter matching ONLY records the classifying
// replica's account created — the SDK evaluates it against the
// reacted-to message, so reactions badge the message's author and
// nobody else.
func TestClassifyReadReactionAudience(t *testing.T) {
	ctx := &handler.ChangeCtx{SelfIdentity: "acc-me"}

	cl := classifyRead(ctx, reactionSet("m1", "❤️", "acc-reactor", true))
	if !cl.Track || len(cl.Tags) != 1 || cl.Tags[0] != TagReaction {
		t.Fatalf("react verdict = %+v, want tracked reaction", cl)
	}
	if cl.Key == "" {
		t.Errorf("react verdict missing supersede key")
	}
	if cl.Audience == nil {
		t.Fatalf("react verdict missing audience filter")
	}
	mine := anyenc.MustParseJson(`{"id":"m1","creator":"acc-me","text":"hi"}`)
	theirs := anyenc.MustParseJson(`{"id":"m1","creator":"acc-other","text":"hi"}`)
	if !cl.Audience.Ok(mine, nil) {
		t.Errorf("audience must match a message this account authored")
	}
	if cl.Audience.Ok(theirs, nil) {
		t.Errorf("audience must not match someone else's message")
	}

	// Un-react: untracked, but the key must survive so the pending
	// entry supersedes away.
	uncl := classifyRead(ctx, reactionSet("m1", "❤️", "acc-reactor", false))
	if uncl.Track {
		t.Errorf("un-react verdict tracked: %+v", uncl)
	}
	if uncl.Key != cl.Key {
		t.Errorf("un-react key %q != react key %q — supersede would miss", uncl.Key, cl.Key)
	}

	// New messages track for everyone — no audience restriction.
	msg := classifyRead(ctx, &handler.RecordChange{
		Id:     "m2",
		Upsert: true,
		Ops:    []handler.Op{{Type: handler.OpSet, Path: []string{FieldText}}},
	})
	if !msg.Track || len(msg.Tags) != 1 || msg.Tags[0] != TagMessage {
		t.Fatalf("message verdict = %+v, want tracked message", msg)
	}
	if msg.Audience != nil {
		t.Errorf("message verdict must not carry an audience filter")
	}
}

// TestClassifyRead_AgentDone pins the agent carve-out: a create whose
// `agent.done` is true asks the SDK to track the entry on the signer's
// own account (the agent writes under the owner's key); a streaming
// bubble (done:false), a human message, or an unresolvable record do
// not.
func TestClassifyRead_AgentDone(t *testing.T) {
	records := map[string]*anyenc.Value{
		"done":   anyenc.MustParseJson(`{"id":"done","creator":"acc-me","text":"answer","agent":{"name":"bao","done":true}}`),
		"stream": anyenc.MustParseJson(`{"id":"stream","creator":"acc-me","text":"thinking","agent":{"name":"bao","done":false}}`),
		"human":  anyenc.MustParseJson(`{"id":"human","creator":"acc-me","text":"hi"}`),
		"ping":   anyenc.MustParseJson(`{"id":"ping","creator":"acc-me","text":"@me","mentions":["acc-me"],"agent":{"name":"bao","done":true}}`),
	}
	for id, want := range map[string]bool{"done": true, "stream": false, "human": false} {
		cl := classifyRead(classifyCtx("acc-me", id, records), createChange(id))
		if !cl.Track || len(cl.Tags) != 1 || cl.Tags[0] != TagMessage {
			t.Fatalf("%s: verdict = %+v, want tracked [message]", id, cl)
		}
		if cl.TrackSelfAuthored != want {
			t.Errorf("%s: TrackSelfAuthored = %v, want %v", id, cl.TrackSelfAuthored, want)
		}
	}

	// A terminal agent message that mentions the owner carries the
	// mention tag as well — it badges unreadMentions too.
	cl := classifyRead(classifyCtx("acc-me", "ping", records), createChange("ping"))
	if !cl.TrackSelfAuthored || len(cl.Tags) != 2 || cl.Tags[1] != TagMention {
		t.Errorf("ping: verdict = %+v, want TrackSelfAuthored [message mention]", cl)
	}

	// No getter: degrade to a plain born-read message, never panic.
	cl = classifyRead(&handler.ChangeCtx{SelfIdentity: "acc-me", RecordId: "done"}, createChange("done"))
	if cl.TrackSelfAuthored {
		t.Errorf("nil getter must not set TrackSelfAuthored: %+v", cl)
	}

	// Edits never carry it: the group is create-only, and the rule is
	// one unread per completed run.
	cl = classifyRead(classifyCtx("acc-me", "ping", records), textEditChange("ping"))
	if cl.TrackSelfAuthored {
		t.Errorf("text edit must not set TrackSelfAuthored: %+v", cl)
	}
}

// TestClassifyRead_UpsertOnExistingIsEdit pins create detection: an
// Upsert-flagged change on a record whose creation version is not this
// change's version is an edit — it never re-tracks "message", never
// re-reads `agent`. The same ops with a matching version are a create.
func TestClassifyRead_UpsertOnExistingIsEdit(t *testing.T) {
	records := map[string]*anyenc.Value{
		"m1": anyenc.MustParseJson(`{"id":"m1","creator":"acc-me","text":"edited","_ver":{"id":"v1"},"agent":{"name":"bao","done":true}}`),
	}
	upsertText := &handler.RecordChange{
		Id:     "m1",
		Upsert: true,
		Ops:    []handler.Op{{Type: handler.OpSet, Path: []string{FieldText}}},
	}

	ctx := classifyCtx("acc-me", "m1", records)
	ctx.Change = &handler.Change{VersionId: "v2"}
	cl := classifyRead(ctx, upsertText)
	if cl.Track || cl.TrackSelfAuthored || cl.Key != "mention:m1" {
		t.Fatalf("upsert on existing record = %+v, want untracked edit with mention key", cl)
	}

	ctx.Change = &handler.Change{VersionId: "v1"}
	cl = classifyRead(ctx, upsertText)
	if !cl.Track || !cl.TrackSelfAuthored || len(cl.Tags) != 1 || cl.Tags[0] != TagMessage {
		t.Fatalf("upsert that created the record = %+v, want tracked agent message", cl)
	}

	// No change on the ctx (hand-built): an Upsert is taken at face value.
	ctx.Change = nil
	if cl = classifyRead(ctx, upsertText); !cl.Track {
		t.Fatalf("no change ctx: upsert must still classify as create, got %+v", cl)
	}
}
