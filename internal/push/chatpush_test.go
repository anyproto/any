package push

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any-sync-sdk/space"
)

// TestChatPayload_HeartWireFormat pins the wire JSON byte-for-byte
// against heart's chatpush.Payload (core/block/chats/chatpush/push.go)
// — the mobile notification extensions decode this exact shape, so
// field names, order, and presence must never drift.
func TestChatPayload_HeartWireFormat(t *testing.T) {
	p := Payload{
		SpaceId:  "sp1",
		SenderId: "acc1",
		Type:     ChatMessage,
		NewMessagePayload: &NewMessagePayload{
			ChatId:         "chat1",
			MsgId:          "m1",
			SpaceName:      "Space",
			ChatName:       "General",
			SenderName:     "Alice",
			Text:           "hi",
			HasAttachments: true,
			Attachments:    []*Attachment{{}},
		},
	}
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"spaceId":"sp1","spaceUxType":0,"spaceType":0,"senderId":"acc1","type":1,` +
		`"newMessage":{"chatId":"chat1","msgId":"m1","spaceName":"Space","chatName":"General",` +
		`"senderName":"Alice","text":"hi","hasAttachments":true,"attachments":[{"layout":0}]}}`
	if string(got) != want {
		t.Errorf("payload JSON drifted from heart's wire format:\n got: %s\nwant: %s", got, want)
	}
}

// TestChatPayload_OneToOneKinds pins the enum values a receiver keys
// its direct-message rendering on (heart's SpaceUxType_OneToOne = 4,
// SpaceType_SpaceTypeOneToOne = 4); everything else stays 0.
func TestChatPayload_OneToOneKinds(t *testing.T) {
	if ux, kind := heartSpaceKinds(space.SpaceTypeOneToOne); ux != 4 || kind != 4 {
		t.Fatalf("one-to-one kinds = (%d, %d), want (4, 4)", ux, kind)
	}
	for _, st := range []string{space.SpaceTypeAny, "", "any.something"} {
		if ux, kind := heartSpaceKinds(st); ux != 0 || kind != 0 {
			t.Errorf("kinds(%q) = (%d, %d), want (0, 0)", st, ux, kind)
		}
	}
	ux, kind := heartSpaceKinds(space.SpaceTypeOneToOne)
	got, err := json.Marshal(Payload{SpaceId: "sp1", SpaceUxType: ux, SpaceType: kind, SenderId: "acc1", Type: ChatMessage})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"spaceId":"sp1","spaceUxType":4,"spaceType":4,"senderId":"acc1","type":1}`; string(got) != want {
		t.Fatalf("payload = %s, want %s", got, want)
	}
}

// TestChatPayload_HeartWireFormat_Minimal: empty-string omitempty
// fields drop exactly the way heart's do (spaceId omitted, empty
// nested strings kept, nil attachments marshal as null).
func TestChatPayload_HeartWireFormat_Minimal(t *testing.T) {
	p := Payload{
		SenderId: "acc1",
		Type:     ChatMessage,
		NewMessagePayload: &NewMessagePayload{
			ChatId: "chat1",
			MsgId:  "m1",
			Text:   "hi",
		},
	}
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"spaceUxType":0,"spaceType":0,"senderId":"acc1","type":1,` +
		`"newMessage":{"chatId":"chat1","msgId":"m1","spaceName":"","chatName":"",` +
		`"senderName":"","text":"hi","hasAttachments":false,"attachments":null}}`
	if string(got) != want {
		t.Errorf("minimal payload JSON drifted:\n got: %s\nwant: %s", got, want)
	}
}

// TestChatTopics pins the topic construction against a known sha256
// vector (sha256("abc")) — byte-identical with heart's
// strings.Join({"chats", sha256hex(chatId), identity}, "/").
func TestChatTopics(t *testing.T) {
	const abcSha = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := sha256hex("abc"); got != abcSha {
		t.Errorf("sha256hex = %s, want %s", got, abcSha)
	}
	if got := topicChat("abc"); got != "chats/"+abcSha {
		t.Errorf("topicChat = %s", got)
	}
	if got := topicChatMention("abc", "AIdent"); got != "chats/"+abcSha+"/AIdent" {
		t.Errorf("topicChatMention = %s", got)
	}
}

func TestAddedMentions(t *testing.T) {
	cases := []struct {
		name          string
		before, after []string
		want          []string
	}{
		{"nothing before, all added", nil, []string{"a", "b"}, []string{"a", "b"}},
		{"no change", []string{"a"}, []string{"a"}, nil},
		{"one added", []string{"a"}, []string{"a", "b"}, []string{"b"}},
		{"removed only", []string{"a", "b"}, []string{"a"}, nil},
		{"swap", []string{"a"}, []string{"b"}, []string{"b"}},
		{"cleared", []string{"a"}, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := addedMentions(tc.before, tc.after); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("addedMentions(%v, %v) = %v, want %v", tc.before, tc.after, got, tc.want)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("short string changed: %q", got)
	}
	if got := truncateRunes(strings.Repeat("x", 2000), maxPushTextRunes); len(got) != maxPushTextRunes {
		t.Errorf("ascii truncation: len = %d", len(got))
	}
	// Multibyte: 1500 runes of "я" (2 bytes each) → exactly 1024 runes,
	// no split rune.
	in := strings.Repeat("я", 1500)
	got := truncateRunes(in, maxPushTextRunes)
	if runes := []rune(got); len(runes) != maxPushTextRunes || runes[len(runes)-1] != 'я' {
		t.Errorf("multibyte truncation: %d runes, last %q", len(runes), runes[len(runes)-1])
	}
}

// TestNotifyChatRead_Direct unit-tests the read hook without an SDK:
// it must enqueue a SILENT job with groupId = sha256hex(chatObjectId)
// and no topics. Works pre-Start (Enqueue + capture need no loops).
func TestNotifyChatRead_Direct(t *testing.T) {
	s := New(nil, t.TempDir())
	type job struct {
		spaceId string
		topics  []string
		payload []byte
		groupId string
		silent  bool
	}
	var got *job
	s.CaptureNotifications(func(spaceId string, topics []string, payload []byte, groupId string, silent bool) {
		got = &job{spaceId, topics, payload, groupId, silent}
	})
	s.NotifyChatRead("sp1", "chat-object")
	if got == nil {
		t.Fatal("nothing captured")
	}
	if !got.silent {
		t.Error("read hook must be silent")
	}
	if got.spaceId != "sp1" || got.groupId != sha256hex("chat-object") {
		t.Errorf("job = %+v", got)
	}
	if got.topics != nil || got.payload != nil {
		t.Errorf("silent job must carry no topics/payload: %+v", got)
	}
}

// TestGoHook_InertBeforeStart: the async hooks are guarded no-ops on a
// never-started service (nil ctx) — the handler nil-checks deps.push,
// but the service itself must also be safe pre-Start.
func TestGoHook_InertBeforeStart(t *testing.T) {
	s := New(nil, t.TempDir())
	ran := make(chan struct{}, 1)
	s.goHook(func(context.Context) { ran <- struct{}{} })
	select {
	case <-ran:
		t.Error("goHook must not run before Start")
	case <-time.After(50 * time.Millisecond):
	}
}
