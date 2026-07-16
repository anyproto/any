// Multipeer chat coverage. The single-process tests in
// internal/server/handlers_chat_test.go drive every endpoint against a
// real SDK; what they cannot prove is that chat_messages records
// converge across two replicas and that the author-only check fires
// for peer-originated requests as well as locally-issued ones.
package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// chatMsg is the test-local decode target for a chat_messages record
// read back via /query. Chat writes return api.ModifyResult, so the
// message body is always fetched through the query path.
type chatMsg struct {
	Id               string
	Creator          string
	CreatedAt        int64
	ModifiedAt       int64
	ReplyToMessageId string
	Agent            *api.ChatAgentMeta
	Text             string
	Mentions         []string
	Reactions        map[string]map[string]int64
	// SDK-materialized read-tracking flags (docs/16-chat.md). Absent
	// on the wire once read, so false means "read".
	Unread          bool
	UnreadMention   bool
	UnreadReactions bool
}

// chatListResp is the read-back message list, materialised via POST
// /v1/spaces/:id/query with dataset=chat_messages.
type chatListResp struct {
	Messages []chatMsg
}

// sendChat posts a message to base and returns the read-back record
// from the same peer (a local write is immediately queryable). Fails
// if the message doesn't surface. Replaces decoding the send response,
// which now carries only api.ModifyResult.
func sendChat(t *testing.T, base, body string) chatMsg {
	t.Helper()
	var res api.ModifyResult
	mustJSON(t, http.MethodPost, base+"/chat/messages", body, http.StatusCreated, &res)
	if len(res.RecordIds) == 0 || res.RecordIds[0] == "" {
		t.Fatalf("sendChat: no recordIds in %+v", res)
	}
	id := res.RecordIds[0]
	var got chatMsg
	if !pollUntil(30*time.Second, func() bool {
		got = findById(chatMessages(t, base), id)
		return got.Id == id
	}) {
		t.Fatalf("sendChat: message %s not queryable on sender", id)
	}
	return got
}

// findById returns the message with the given id, or zero chatMsg.
func findById(list chatListResp, id string) chatMsg {
	for _, m := range list.Messages {
		if m.Id == id {
			return m
		}
	}
	return chatMsg{}
}

// chatMessages fetches the chat_messages list off `base` (the peer's
// /v1/spaces/:id/objects/:objId prefix). Thin wrapper around POST
// /v1/spaces/:id/query with dataset=chat_messages, sort=_ver.id.
//
// Tolerates non-200 responses by returning an empty list — convergence
// pollUntils call this in tight loops right after a joiner activates,
// before any-sync has fetched the chat tree. The query then returns
// 500 with `tree does not exist`, which is the right thing for the
// API to say (the local tree genuinely isn't there yet) but should
// not bail the polling loop.
func chatMessages(t *testing.T, base string) chatListResp {
	t.Helper()
	// base = http://<addr>/v1/spaces/<spId>/objects/<objId>
	idx := strings.LastIndex(base, "/v1/spaces/")
	if idx < 0 {
		t.Fatalf("chatMessages: unexpected base %q", base)
	}
	hostAndV1 := base[:idx+len("/v1/spaces/")]
	rest := base[idx+len("/v1/spaces/"):]
	parts := strings.SplitN(rest, "/objects/", 2)
	if len(parts) != 2 {
		t.Fatalf("chatMessages: cannot parse %q", rest)
	}
	spaceId, objectId := parts[0], parts[1]
	body, _ := json.Marshal(map[string]any{
		"objectId": objectId,
		"dataset":  "chat_messages",
		"sort":     []string{"_ver.id"},
	})
	resp, raw := doRequest(t, http.MethodPost, hostAndV1+spaceId+"/query", string(body))
	if resp.StatusCode != http.StatusOK {
		return chatListResp{}
	}
	var qr struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(raw, &qr); err != nil {
		t.Fatalf("chatMessages: parse %s: %v\nbody=%s", base, err, string(raw))
	}
	out := chatListResp{Messages: make([]chatMsg, 0, len(qr.Records))}
	for _, r := range qr.Records {
		out.Messages = append(out.Messages, decodeQueryChatMessage(t, r))
	}
	return out
}

// decodeQueryChatMessage rehydrates one chat record from the raw
// query wire shape. Two wrinkles vs. the bespoke /chat/messages
// response shape:
//
//   - anyenc → fastjson renders numeric fields as JSON numbers in
//     exponential form for large ints; we hop through float64 and cast.
//   - Reactions are stored as `reactions.<emoji>.<accountId> =
//     <timestamp>` (server-derived). The bespoke write handlers now
//     return this same shape, so we just narrow the float64 timestamps
//     to int64 — no transpose — and assertions compare object-to-object.
func decodeQueryChatMessage(t *testing.T, raw []byte) chatMsg {
	t.Helper()
	var f struct {
		Id               string                        `json:"id"`
		Creator          string                        `json:"creator"`
		CreatedAt        float64                       `json:"createdAt"`
		ModifiedAt       float64                       `json:"modifiedAt"`
		ReplyToMessageId string                        `json:"replyToMessageId"`
		Agent            *api.ChatAgentMeta            `json:"agent"`
		Text             string                        `json:"text"`
		Mentions         []string                      `json:"mentions"`
		Reactions        map[string]map[string]float64 `json:"reactions"`
		Unread           bool                          `json:"unread"`
		UnreadMention    bool                          `json:"unreadMention"`
		UnreadReactions  bool                          `json:"unreadReactions"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("chatMessages: decode record: %v\nraw=%s", err, raw)
	}
	// Reactions ship in storage layout (emoji → {accountId: ts}); narrow
	// the JSON float64 timestamps to int64.
	var reactions map[string]map[string]int64
	if len(f.Reactions) > 0 {
		reactions = make(map[string]map[string]int64, len(f.Reactions))
		for emoji, byAcct := range f.Reactions {
			inner := make(map[string]int64, len(byAcct))
			for acctId, ts := range byAcct {
				inner[acctId] = int64(ts)
			}
			reactions[emoji] = inner
		}
	}
	return chatMsg{
		Id:               f.Id,
		Creator:          f.Creator,
		CreatedAt:        int64(f.CreatedAt),
		ModifiedAt:       int64(f.ModifiedAt),
		ReplyToMessageId: f.ReplyToMessageId,
		Agent:            f.Agent,
		Text:             f.Text,
		Mentions:         f.Mentions,
		Reactions:        reactions,
		Unread:           f.Unread,
		UnreadMention:    f.UnreadMention,
		UnreadReactions:  f.UnreadReactions,
	}
}

// findMessage returns the first message in list with the given text,
// or zero chatMsg if absent.
func findMessage(list chatListResp, text string) chatMsg {
	for _, m := range list.Messages {
		if m.Text == text {
			return m
		}
	}
	return chatMsg{}
}

// TestE2E_MultipeerChat exercises the full chat surface across two
// peers in one shot — start-up cost dominates a multipeer test, so
// bundling bidirectional send / cross-peer reactions / author-only
// rejection into one body is far cheaper than splitting them.
//
// Sequence:
//  1. Owner creates space + chat object, sends message M1 BEFORE the
//     joiner joins (so the joiner's first ingestion has chat data
//     already on the wire — exercises the same ahead-of-schema path
//     as TestE2E_MultipeerCRDTConvergence).
//  2. Joiner joins as writer.
//  3. Joiner waits for M1 to converge into its list.
//  4. Joiner attempts PATCH/DELETE on M1 → 403 chat.not_author from
//     the API-layer pre-check (creator on the record ≠ joiner identity).
//  5. Joiner sends M2 with replyTo=M1.
//  6. Owner waits for M2 to converge.
//  7. Owner reacts ❤️ to M2; joiner reacts 👍 to M1.
//  8. Both peers eventually report both reactions on their respective
//     messages. The reactions map is identity-keyed in storage so each
//     peer's reaction lands under its own slot — no clobbering.
func TestE2E_MultipeerChat(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer chat test takes ~120s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"chat"}`, http.StatusCreated, &sp)

	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)
	if obj.ObjectId == "" {
		t.Fatalf("owner: no objectId in create response")
	}

	ownerBase := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId
	joinerBase := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId

	// Pre-join message: tests that chat data laid down before headsync
	// still converges into the joiner's local store. Read back from the
	// owner, the record carries the StrKey-encoded `creator`
	// (PubKey.Account()) — that's the identity the chat handler stamps
	// and the only one we should compare against. /v1/account.id and
	// Members.identity use the libp2p PeerId encoding of the same key —
	// equivalent identities but different strings, do not cross-compare.
	m1 := sendChat(t, ownerBase, `{"text":"hello from owner"}`)
	ownerId := m1.Creator
	if m1.Id == "" || ownerId == "" {
		t.Fatalf("m1 not stamped: %+v", m1)
	}

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// Wait for M1 to land on the joiner. ~3 min budget mirrors
	// TestE2E_MultipeerCRDTConvergence — chat_messages is a per-object
	// dataset, same sync path as properties/objects.
	var m1OnJoiner chatMsg
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		list := chatMessages(t, joinerBase)
		m1OnJoiner = findMessage(list, "hello from owner")
		return m1OnJoiner.Id == m1.Id && m1OnJoiner.Creator == ownerId
	}) {
		t.Fatalf("joiner never saw owner's M1 (id=%q creator=%q)",
			m1OnJoiner.Id, m1OnJoiner.Creator)
	}

	// Author-only enforcement, peer side. The API layer pre-fetches
	// the message and 403s before issuing a CRDT change — no need to
	// wait for handler-level rejection on the owner side.
	editBody := `{"text":"hostile takeover"}`
	var editEnv api.ErrorEnvelope
	mustJSON(t, http.MethodPatch, joinerBase+"/chat/messages/"+m1.Id,
		editBody, http.StatusForbidden, &editEnv)
	if editEnv.Error.Code != api.ErrChatNotAuthor {
		t.Errorf("joiner edit: code = %q, want %q", editEnv.Error.Code, api.ErrChatNotAuthor)
	}
	var delEnv api.ErrorEnvelope
	mustJSON(t, http.MethodDelete, joinerBase+"/chat/messages/"+m1.Id,
		"", http.StatusForbidden, &delEnv)
	if delEnv.Error.Code != api.ErrChatNotAuthor {
		t.Errorf("joiner delete: code = %q, want %q", delEnv.Error.Code, api.ErrChatNotAuthor)
	}

	// Joiner replies to M1. Reply id is a soft pointer — the SDK
	// doesn't validate it exists, so this works even pre-convergence;
	// we already polled above so the reference is sound.
	body, _ := json.Marshal(api.ChatSendRequest{
		Text:             "reply from joiner",
		ReplyToMessageId: m1.Id,
	})
	m2 := sendChat(t, joinerBase, string(body))
	joinerId := m2.Creator
	if m2.Id == "" || joinerId == "" {
		t.Fatalf("m2 not stamped: %+v", m2)
	}
	if joinerId == ownerId {
		t.Fatalf("owner and joiner stamped identical creator %q — fixture broken", joinerId)
	}
	if m2.ReplyToMessageId != m1.Id {
		t.Errorf("m2.replyToMessageId = %q, want %q", m2.ReplyToMessageId, m1.Id)
	}

	// Owner waits for M2.
	var m2OnOwner chatMsg
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{joiner, owner}, func() bool {
		list := chatMessages(t, ownerBase)
		m2OnOwner = findMessage(list, "reply from joiner")
		return m2OnOwner.Id == m2.Id && m2OnOwner.Creator == joinerId
	}) {
		t.Fatalf("owner never saw joiner's M2 (id=%q)", m2OnOwner.Id)
	}

	// Cross-peer reactions: each side reacts to the other side's
	// message. Identity-keyed storage means both reactions land in
	// distinct slots; the wire transpose joins them under each emoji.
	emojiHeart := url.PathEscape("❤️")
	emojiThumb := url.PathEscape("👍")
	mustStatus(t, http.MethodPost,
		ownerBase+"/chat/messages/"+m2.Id+"/reactions/"+emojiHeart,
		"", http.StatusOK)
	mustStatus(t, http.MethodPost,
		joinerBase+"/chat/messages/"+m1.Id+"/reactions/"+emojiThumb,
		"", http.StatusOK)

	// Owner sees joiner's 👍 on M1.
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{joiner, owner}, func() bool {
		list := chatMessages(t, ownerBase)
		got := findMessage(list, "hello from owner")
		_, ok := got.Reactions["👍"][joinerId]
		return ok
	}) {
		t.Fatalf("owner never saw joiner's 👍 reaction on M1")
	}

	// Joiner sees owner's ❤️ on M2.
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		list := chatMessages(t, joinerBase)
		got := findMessage(list, "reply from joiner")
		_, ok := got.Reactions["❤️"][ownerId]
		return ok
	}) {
		t.Fatalf("joiner never saw owner's ❤️ reaction on M2")
	}
}

// TestE2E_MultipeerChatEditConverges exercises edit propagation: the
// owner edits a message after the joiner has already cached it, and
// the joiner eventually sees the new text plus a bumped modifiedAt.
// Carved out of the bundle above so the convergence assertion has a
// dedicated runtime budget — edits trigger a fresh CRDT change that
// has to head-sync separately from the create.
func TestE2E_MultipeerChatEditConverges(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer chat edit test takes ~90s; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"chat-edit"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)

	ownerBase := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId
	joinerBase := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId

	msg := sendChat(t, ownerBase, `{"text":"original"}`)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// First make sure the joiner has the original. Without this, the
	// edit-converge poll below can't tell stale-cache "haven't seen
	// the edit" from "haven't seen the message".
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		list := chatMessages(t, joinerBase)
		return findMessage(list, "original").Id == msg.Id
	}) {
		t.Fatalf("joiner never saw the original message")
	}

	// Owner edits. The PATCH returns a ModifyResult; we read the
	// post-edit record back from the owner, then exercise wire
	// convergence to the joiner.
	var editRes api.ModifyResult
	mustJSON(t, http.MethodPatch, ownerBase+"/chat/messages/"+msg.Id,
		`{"text":"edited"}`, http.StatusOK, &editRes)
	if editRes.VersionId == "" {
		t.Errorf("edit: empty versionId in %+v", editRes)
	}
	edited := findById(chatMessages(t, ownerBase), msg.Id)
	if edited.Text != "edited" {
		t.Fatalf("owner-side edit didn't apply: %+v", edited)
	}
	if edited.ModifiedAt < edited.CreatedAt {
		t.Errorf("modifiedAt %d < createdAt %d after edit", edited.ModifiedAt, edited.CreatedAt)
	}

	// Joiner waits for the edit. modifiedAt must move forward — same
	// id, new text, monotonic ts.
	var lastSeen chatMsg
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		list := chatMessages(t, joinerBase)
		got := findMessage(list, "edited")
		if got.Id == msg.Id {
			lastSeen = got
			return got.ModifiedAt >= edited.CreatedAt
		}
		return false
	}) {
		t.Fatalf("joiner never saw owner's edit; lastSeen=%+v", lastSeen)
	}
	if lastSeen.ModifiedAt < lastSeen.CreatedAt {
		t.Errorf("joiner-side modifiedAt %d < createdAt %d", lastSeen.ModifiedAt, lastSeen.CreatedAt)
	}
}
