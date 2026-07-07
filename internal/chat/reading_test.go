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
