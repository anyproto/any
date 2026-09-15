package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/chat"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/push"
)

// pushSha mirrors the push package's sha256hex (heart's pushGroupId):
// per-chat topic segments and groupId are sha256 hex of the chat
// object id.
func pushSha(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])
}

// TestServer_ChatNotifyMode_RowAddressing (M3): a notifyMode value
// written through the generic properties surface lands on the chat
// object's row under `chat.notifyMode` — the exact addressing the
// push sync loop (and clients) read, same container as the
// chat.unreadCount badge counters.
func TestServer_ChatNotifyMode_RowAddressing(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	// The chat namespace on the row is granted by the object's chat
	// declaring type (the fixture's), not attached by the send.
	chatSend(t, e, base, "hello", "")

	rec := doJSON(t, e, http.MethodPost,
		"/v1/spaces/"+spaceId+"/properties/"+objectId+"/set/chat",
		`{"patch":{"notifyMode":"mentions"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set notifyMode: %d %s", rec.Code, rec.Body.String())
	}

	// Read the objects row back — the account-scope mirror puts the
	// value inline at row[chat][notifyMode].
	qBody, _ := json.Marshal(map[string]any{"filter": map[string]any{"id": objectId}})
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/query", string(qBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("objects query: %d %s", rec.Code, rec.Body.String())
	}
	var qr struct {
		Records []struct {
			Chat struct {
				NotifyMode string `json:"notifyMode"`
			} `json:"chat"`
		} `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &qr); err != nil {
		t.Fatalf("decode row: %v\nbody=%s", err, rec.Body.String())
	}
	if len(qr.Records) != 1 {
		t.Fatalf("row query: got %d records, want 1: %s", len(qr.Records), rec.Body.String())
	}
	if qr.Records[0].Chat.NotifyMode != "mentions" {
		t.Errorf("chat.notifyMode = %q, want mentions (row=%s)",
			qr.Records[0].Chat.NotifyMode, rec.Body.String())
	}

	// The push sync loop enumerates a space's chats as the objects whose
	// TYPE is an owner of the chat collection, plus the declaring roots
	// themselves (a definition hosts its own datasets — the general chat
	// is its own type). The owners come from dataset discovery. A plain
	// object is not a chat.
	other := mustCreateObject(t, e, spaceId, `{}`)
	rec = doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/datasets", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("datasets: %d %s", rec.Code, rec.Body.String())
	}
	var ds api.DatasetsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ds); err != nil {
		t.Fatal(err)
	}
	var owners []string
	for _, s := range ds.Datasets {
		if s.Name == chat.Dataset {
			owners = s.Owners
		}
	}
	if len(owners) != 1 || owners[0] != installModuleType(t, e, spaceId, "chat") {
		t.Fatalf("chat_messages owners = %v, want the fixture's chat type", owners)
	}
	fBody, _ := json.Marshal(map[string]any{"filter": map[string]any{"$or": []any{
		map[string]any{"any.type": map[string]any{"$in": owners}},
		map[string]any{"id": map[string]any{"$in": owners}},
	}}})
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/query", string(fBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat objects query: %d %s", rec.Code, rec.Body.String())
	}
	var chats struct {
		Records []struct {
			Id string `json:"id"`
		} `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &chats); err != nil {
		t.Fatal(err)
	}
	if len(chats.Records) != 1 || chats.Records[0].Id != objectId {
		t.Fatalf("chat objects = %+v, want only %s (not %s)", chats.Records, objectId, other)
	}
}

// pushJob is one captured would-be notification.
type pushJob struct {
	spaceId string
	topics  []string
	payload []byte
	groupId string
	silent  bool
}

// TestServer_ChatPushHooks drives the M4 hooks end-to-end against a
// real SDK, capturing at the push service's Enqueue seam (the node
// named in config is unreachable, so nothing leaves the process):
// send broadcasts, mentions add the per-chat mention + bare identity
// topics, edits push only NEWLY-added mentions, reads enqueue silent.
func TestServer_ChatPushHooks(t *testing.T) {
	d, teardown := newTestDepsCfg(t, func(cfg *config.Config) {
		cfg.Push = config.Push{PeerId: "12D3KooWTestPushNode", Addrs: []string{"127.0.0.1:1"}}
	})
	defer teardown()
	if d.push == nil {
		t.Fatal("push service not built despite cfg.Push")
	}
	jobs := make(chan pushJob, 16)
	d.push.CaptureNotifications(func(spaceId string, topics []string, payload []byte, groupId string, silent bool) {
		jobs <- pushJob{spaceId, topics, payload, groupId, silent}
	})
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	chatSha := pushSha(objectId)

	waitJob := func() pushJob {
		t.Helper()
		select {
		case j := <-jobs:
			return j
		case <-time.After(15 * time.Second):
			t.Fatal("no push job captured")
			return pushJob{}
		}
	}
	wantNoJob := func(grace time.Duration) {
		t.Helper()
		select {
		case j := <-jobs:
			t.Fatalf("unexpected push job: %+v", j)
		case <-time.After(grace):
		}
	}
	wantTopics := func(j pushJob, want ...string) {
		t.Helper()
		if len(j.topics) != len(want) {
			t.Fatalf("topics = %v, want %v", j.topics, want)
		}
		for i := range want {
			if j.topics[i] != want[i] {
				t.Fatalf("topics = %v, want %v", j.topics, want)
			}
		}
	}

	// 1. Plain send → loud broadcast on space-wide + per-chat topics
	//    only; groupId collapses per chat.
	plain := chatSend(t, e, base, "no pings here", "")
	job := waitJob()
	if job.silent {
		t.Error("send hook must be loud")
	}
	if job.spaceId != spaceId || job.groupId != chatSha {
		t.Errorf("job routing = {space %s group %s}, want {%s %s}", job.spaceId, job.groupId, spaceId, chatSha)
	}
	wantTopics(job, "chats", "chats/"+chatSha)

	var payload push.Payload
	if err := json.Unmarshal(job.payload, &payload); err != nil {
		t.Fatalf("decode payload: %v\nraw=%s", err, job.payload)
	}
	if payload.Type != push.ChatMessage || payload.SpaceId != spaceId || payload.SenderId != plain.Creator {
		t.Errorf("payload envelope = %+v", payload)
	}
	nm := payload.NewMessagePayload
	if nm == nil {
		t.Fatal("payload.newMessage missing")
	}
	if nm.ChatId != objectId || nm.MsgId != plain.Id || nm.Text != "no pings here" {
		t.Errorf("newMessage = %+v", nm)
	}
	if nm.SpaceName != "ChatTest" {
		t.Errorf("spaceName = %q, want ChatTest", nm.SpaceName)
	}
	if nm.HasAttachments || len(nm.Attachments) != 0 {
		t.Errorf("attachments should be empty: %+v", nm)
	}
	self := plain.Creator

	// 2. Send with a mention → the full topic superset: broadcast pair
	//    plus per-chat mention + bare identity per mentioned account.
	mentioned := chatSend(t, e, base,
		fmt.Sprintf("hey [you](any://m/%s/%s), look", spaceId, self), "")
	job = waitJob()
	wantTopics(job, "chats", "chats/"+chatSha, "chats/"+chatSha+"/"+self, self)
	if job.groupId != chatSha || job.silent {
		t.Errorf("mention job = %+v", job)
	}

	// 3. Edit that ADDS a mention → push to the added mention only
	//    (bare + per-chat mention topics, no broadcast).
	body := fmt.Sprintf(`{"text":"edited to ping [you](any://m/%s/%s)"}`, spaceId, self)
	if rec := doJSON(t, e, http.MethodPatch, base+"/chat/messages/"+plain.Id, body); rec.Code != http.StatusOK {
		t.Fatalf("edit add mention: %d %s", rec.Code, rec.Body.String())
	}
	job = waitJob()
	if job.silent {
		t.Error("edit hook must be loud")
	}
	wantTopics(job, "chats/"+chatSha+"/"+self, self)
	if job.groupId != chatSha {
		t.Errorf("edit groupId = %s, want %s", job.groupId, chatSha)
	}
	if err := json.Unmarshal(job.payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.NewMessagePayload.MsgId != plain.Id {
		t.Errorf("edit payload msgId = %q, want %q", payload.NewMessagePayload.MsgId, plain.Id)
	}

	// 4. Edit that KEEPS the existing mention → no newly-added
	//    mentions, no push at all.
	body = fmt.Sprintf(`{"text":"still pinging [you](any://m/%s/%s)"}`, spaceId, self)
	if rec := doJSON(t, e, http.MethodPatch, base+"/chat/messages/"+mentioned.Id, body); rec.Code != http.StatusOK {
		t.Fatalf("edit keep mention: %d %s", rec.Code, rec.Body.String())
	}
	wantNoJob(750 * time.Millisecond)

	// 5. Mark-read (single message boundary) → SILENT with the chat's
	//    groupId, no topics, no payload.
	if rec := doJSON(t, e, http.MethodPost, base+"/chat/messages/"+mentioned.Id+"/read", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	job = waitJob()
	if !job.silent || job.groupId != chatSha || job.topics != nil || job.payload != nil {
		t.Errorf("read job = %+v, want silent groupId-only", job)
	}

	// 6. read-all → same silent shape.
	if rec := doJSON(t, e, http.MethodPost, base+"/chat/read-all", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("read-all: %d %s", rec.Code, rec.Body.String())
	}
	job = waitJob()
	if !job.silent || job.groupId != chatSha {
		t.Errorf("read-all job = %+v, want silent groupId-only", job)
	}
}

// TestServer_ChatPushHooks_NilPush: with push disabled (deps.push ==
// nil, the default test harness) every hooked handler still works —
// the hooks are nil-guarded, nothing enqueues, nothing panics.
func TestServer_ChatPushHooks_NilPush(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	if d.push != nil {
		t.Fatal("harness unexpectedly built a push service")
	}
	e := buildEcho(d)

	spaceId, objectId := setupChatFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	msg := chatSend(t, e, base, "hello", "")
	if rec := doJSON(t, e, http.MethodPatch, base+"/chat/messages/"+msg.Id, `{"text":"hello, edited"}`); rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, e, http.MethodPost, base+"/chat/messages/"+msg.Id+"/read", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, e, http.MethodPost, base+"/chat/read-all", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("read-all: %d %s", rec.Code, rec.Body.String())
	}
}
