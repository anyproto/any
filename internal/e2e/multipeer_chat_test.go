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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// chatListResp mirrors the old api.ChatListResponse shape, but is
// materialised via POST /v1/spaces/:id/query — the canonical read
// path after the GET endpoint was removed.
type chatListResp struct {
	Messages []api.ChatMessage
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
	out := chatListResp{Messages: make([]api.ChatMessage, 0, len(qr.Records))}
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
//     <timestamp>` (server-derived). We transpose to the wire-friendly
//     `{emoji: [accountId, ...]}` shape (timestamp-sorted ascending) —
//     mirrors what the bespoke handler used to do, so test assertions
//     written against the old shape keep working.
func decodeQueryChatMessage(t *testing.T, raw []byte) api.ChatMessage {
	t.Helper()
	var f struct {
		Id               string                        `json:"id"`
		Creator          string                        `json:"creator"`
		CreatedAt        float64                       `json:"createdAt"`
		ModifiedAt       float64                       `json:"modifiedAt"`
		ReplyToMessageId string                        `json:"replyToMessageId"`
		FromAgent        string                        `json:"fromAgent"`
		Text             string                        `json:"text"`
		Reactions        map[string]map[string]float64 `json:"reactions"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("chatMessages: decode record: %v\nraw=%s", err, raw)
	}
	var transposed map[string][]string
	if len(f.Reactions) > 0 {
		transposed = make(map[string][]string, len(f.Reactions))
		for emoji, byAcct := range f.Reactions {
			ids := make([]string, 0, len(byAcct))
			for acctId := range byAcct {
				ids = append(ids, acctId)
			}
			sort.Slice(ids, func(i, j int) bool {
				return byAcct[ids[i]] < byAcct[ids[j]]
			})
			transposed[emoji] = ids
		}
	}
	return api.ChatMessage{
		Id:               f.Id,
		Creator:          f.Creator,
		CreatedAt:        int64(f.CreatedAt),
		ModifiedAt:       int64(f.ModifiedAt),
		ReplyToMessageId: f.ReplyToMessageId,
		FromAgent:        f.FromAgent,
		Text:             f.Text,
		Reactions:        transposed,
	}
}

// findMessage returns the first message in list with the given text,
// or zero ChatMessage if absent.
func findMessage(list chatListResp, text string) api.ChatMessage {
	for _, m := range list.Messages {
		if m.Text == text {
			return m
		}
	}
	return api.ChatMessage{}
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
	// still converges into the joiner's local store. The send response
	// carries the StrKey-encoded `creator` (PubKey.Account()) — that's
	// the identity the chat handler stamps and the only one we should
	// compare against. /v1/account.id and Members.identity use the
	// libp2p PeerId encoding of the same key — equivalent identities
	// but different strings, do not cross-compare.
	var m1 api.ChatMessage
	mustJSON(t, http.MethodPost, ownerBase+"/chat/messages",
		`{"text":"hello from owner"}`, http.StatusCreated, &m1)
	ownerId := m1.Creator
	if m1.Id == "" || ownerId == "" {
		t.Fatalf("m1 not stamped: %+v", m1)
	}

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// Wait for M1 to land on the joiner. ~3 min budget mirrors
	// TestE2E_MultipeerCRDTConvergence — chat_messages is a per-object
	// dataset, same sync path as properties/objects.
	var m1OnJoiner api.ChatMessage
	if !pollUntil(3*time.Minute, func() bool {
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
	var m2 api.ChatMessage
	mustJSON(t, http.MethodPost, joinerBase+"/chat/messages",
		string(body), http.StatusCreated, &m2)
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
	var m2OnOwner api.ChatMessage
	if !pollUntil(3*time.Minute, func() bool {
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
	if !pollUntil(3*time.Minute, func() bool {
		list := chatMessages(t, ownerBase)
		got := findMessage(list, "hello from owner")
		ids := got.Reactions["👍"]
		for _, id := range ids {
			if id == joinerId {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("owner never saw joiner's 👍 reaction on M1")
	}

	// Joiner sees owner's ❤️ on M2.
	if !pollUntil(3*time.Minute, func() bool {
		list := chatMessages(t, joinerBase)
		got := findMessage(list, "reply from joiner")
		ids := got.Reactions["❤️"]
		for _, id := range ids {
			if id == ownerId {
				return true
			}
		}
		return false
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

	var msg api.ChatMessage
	mustJSON(t, http.MethodPost, ownerBase+"/chat/messages",
		`{"text":"original"}`, http.StatusCreated, &msg)

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	// First make sure the joiner has the original. Without this, the
	// edit-converge poll below can't tell stale-cache "haven't seen
	// the edit" from "haven't seen the message".
	if !pollUntil(3*time.Minute, func() bool {
		list := chatMessages(t, joinerBase)
		return findMessage(list, "original").Id == msg.Id
	}) {
		t.Fatalf("joiner never saw the original message")
	}

	// Owner edits. The PATCH response carries the locally-applied
	// post-edit record; the wire convergence to the joiner is what
	// we're really exercising here.
	var edited api.ChatMessage
	mustJSON(t, http.MethodPatch, ownerBase+"/chat/messages/"+msg.Id,
		`{"text":"edited"}`, http.StatusOK, &edited)
	if edited.Text != "edited" {
		t.Fatalf("owner-side edit didn't apply: %+v", edited)
	}
	if edited.ModifiedAt < edited.CreatedAt {
		t.Errorf("modifiedAt %d < createdAt %d after edit", edited.ModifiedAt, edited.CreatedAt)
	}

	// Joiner waits for the edit. modifiedAt must move forward — same
	// id, new text, monotonic ts.
	var lastSeen api.ChatMessage
	if !pollUntil(3*time.Minute, func() bool {
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

