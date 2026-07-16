// Multipeer read-tracking coverage — the acceptance test for SYN-61.
//
// Read tracking is untestable single-account by construction: your own
// writes are born read (docs/16-chat.md), so an unread message can only
// exist when a DIFFERENT account authored it. The single-process tests
// in internal/server cover the write handlers and the local-scope flag
// fields; this test covers the seam none of them can reach — the SDK
// materializing per-message unread flags AND per-chat counter
// properties from sync-ingested changes, and the read endpoints
// clearing both.
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// rowCounters is a chat row's unread counter properties, read off the
// shared objects collection via POST /objects/query — the exact
// surface docs/16-chat.md § Desktop notifications tells clients to
// badge from. Counter properties are stored at row[typeId][propId]
// (chat.unreadCount, ...) and are absent until first materialized;
// absent decodes as 0, matching the wire contract.
type rowCounters struct {
	found     bool
	messages  int // chat.unreadCount
	mentions  int // chat.unreadMentions
	reactions int // chat.unreadReactionsCount
}

func (rc rowCounters) String() string {
	return fmt.Sprintf("{found:%v unreadCount:%d unreadMentions:%d unreadReactionsCount:%d}",
		rc.found, rc.messages, rc.mentions, rc.reactions)
}

// chatRowCounters fetches the chat object's row on `p` and extracts
// the counter properties. Non-200 / missing row return found=false so
// counter polls ride through the window where the joiner hasn't
// materialized the row yet.
func chatRowCounters(t *testing.T, p *peer, spaceId, objectId string) rowCounters {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"filter": map[string]any{"id": objectId}})
	resp, raw := doRequest(t, http.MethodPost,
		p.base+"/v1/spaces/"+spaceId+"/objects/query", string(body))
	if resp.StatusCode != http.StatusOK {
		return rowCounters{}
	}
	var qr struct {
		Records []struct {
			Chat struct {
				UnreadCount          float64 `json:"unreadCount"`
				UnreadMentions       float64 `json:"unreadMentions"`
				UnreadReactionsCount float64 `json:"unreadReactionsCount"`
			} `json:"chat"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &qr); err != nil {
		t.Fatalf("chatRowCounters: parse: %v\nbody=%s", err, raw)
	}
	if len(qr.Records) == 0 {
		return rowCounters{}
	}
	c := qr.Records[0].Chat
	return rowCounters{
		found:     true,
		messages:  int(c.UnreadCount),
		mentions:  int(c.UnreadMentions),
		reactions: int(c.UnreadReactionsCount),
	}
}

// TestE2E_MultipeerChatReadTracking drives the unread flag + counter
// story across two accounts, both directions, messages and reactions:
//
//  1. Cross-account messages set the per-message `unread` flag AND the
//     chat row's `unreadCount` — the SYN-61 assertion (flags worked,
//     the counter never materialized).
//  2. Own messages are born read on the author, in both roles.
//  3. `POST .../messages/:id/read` is a boundary: clears at-and-before,
//     leaves after; `POST .../read-all` clears the rest.
//  4. A cross-account reaction on YOUR message sets `unreadReactions`
//     + the row's `unreadReactionsCount` without touching
//     `unreadCount`; a reaction on a message you did NOT author syncs
//     without badging you (the audience rule — reactions signal the
//     message's author only).
//  5. A reaction retracted before the recipient looks leaves no trace
//     (the classifier's supersede key), and read-all clears a live one.
//
// Convergence waits force head-sync (pollUntilSynced) and only ever
// gate on chat_messages predicates; flag/counter materialization is
// local and debounced on the receiving peer, so those assertions use
// plain pollUntil — no cross-peer sync involved.
//
// Deliberately NOT asserted: the first-sight seed for pre-join history.
// Whether m1 lands seeded-read on the joiner depends on when its chat
// tree first materializes relative to cold sync — timing the HTTP
// boundary can't control. The SDK's own e2e (read_tracking_test.go)
// owns the seed contract; the explicit read-all below baselines the
// joiner either way.
func TestE2E_MultipeerChatReadTracking(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("multipeer read-tracking test takes ~3min; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"chat-unread"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)

	ownerBase := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId
	joinerBase := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId

	// Pre-join message. Its ensureType also attaches the chat type on
	// the owner, so the type binding is already on the wire when the
	// joiner cold-syncs. The read-back carries the owner's StrKey
	// identity — needed below because an un-react leaves the emoji key
	// behind with an empty identity map, so reaction presence checks
	// must key by identity, not emoji.
	m1 := sendChat(t, ownerBase, `{"text":"pre-join history"}`)
	ownerId := m1.Creator
	if ownerId == "" {
		t.Fatalf("m1 not stamped with creator: %+v", m1)
	}

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		return findById(chatMessages(t, joinerBase), m1.Id).Id == m1.Id
	}) {
		t.Fatalf("joiner never converged on the pre-join message")
	}

	// Baseline the joiner's read state (see the doc comment above).
	mustStatus(t, http.MethodPost, joinerBase+"/chat/read-all", "", http.StatusNoContent)

	// --- (1) Cross-account messages: flag AND row counter on the joiner.
	m2 := sendChat(t, ownerBase, `{"text":"first unread"}`)
	m3 := sendChat(t, ownerBase, `{"text":"second unread"}`)

	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		list := chatMessages(t, joinerBase)
		return findById(list, m2.Id).Id != "" && findById(list, m3.Id).Id != ""
	}) {
		t.Fatalf("joiner never converged on m2+m3")
	}

	var lastList chatListResp
	var lastCounters rowCounters
	if !pollUntil(30*time.Second, func() bool {
		lastList = chatMessages(t, joinerBase)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return findById(lastList, m2.Id).Unread &&
			findById(lastList, m3.Id).Unread &&
			lastCounters.messages == 2 && lastCounters.reactions == 0
	}) {
		t.Fatalf("joiner: cross-account messages never materialized as unread (SYN-61): "+
			"m2.unread=%v m3.unread=%v counters=%v",
			findById(lastList, m2.Id).Unread, findById(lastList, m3.Id).Unread, lastCounters)
	}

	// --- (2) Own messages born read: the owner authored everything so
	// far and its materializer had the whole convergence wait to run —
	// a plain read is sound.
	if c := chatRowCounters(t, owner, sp.Id, obj.ObjectId); c.messages != 0 || c.reactions != 0 {
		t.Errorf("owner: own messages must be born read, counters=%v", c)
	}

	// --- (3a) Read boundary: up-to-m2 clears m2, leaves m3.
	mustStatus(t, http.MethodPost, joinerBase+"/chat/messages/"+m2.Id+"/read",
		"", http.StatusNoContent)
	if !pollUntil(30*time.Second, func() bool {
		lastList = chatMessages(t, joinerBase)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return !findById(lastList, m2.Id).Unread &&
			findById(lastList, m3.Id).Unread &&
			lastCounters.messages == 1
	}) {
		t.Fatalf("joiner: read-up-to-m2 never settled at counter=1: "+
			"m2.unread=%v m3.unread=%v counters=%v",
			findById(lastList, m2.Id).Unread, findById(lastList, m3.Id).Unread, lastCounters)
	}

	// --- (3b) read-all clears the rest.
	mustStatus(t, http.MethodPost, joinerBase+"/chat/read-all", "", http.StatusNoContent)
	if !pollUntil(30*time.Second, func() bool {
		lastList = chatMessages(t, joinerBase)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return !findById(lastList, m3.Id).Unread && lastCounters.messages == 0
	}) {
		t.Fatalf("joiner: read-all never cleared the counter: counters=%v", lastCounters)
	}

	// --- Reverse direction: the joiner's reply goes unread on the owner.
	m4 := sendChat(t, joinerBase, `{"text":"reply from joiner"}`)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{joiner, owner}, func() bool {
		return findById(chatMessages(t, ownerBase), m4.Id).Id == m4.Id
	}) {
		t.Fatalf("owner never converged on the joiner's reply")
	}
	if !pollUntil(30*time.Second, func() bool {
		lastList = chatMessages(t, ownerBase)
		lastCounters = chatRowCounters(t, owner, sp.Id, obj.ObjectId)
		return findById(lastList, m4.Id).Unread && lastCounters.messages == 1
	}) {
		t.Fatalf("owner: joiner's reply never went unread: m4.unread=%v counters=%v",
			findById(lastList, m4.Id).Unread, lastCounters)
	}
	// Born read is identity-based: the author's side stayed clean while
	// the owner's unread materialized.
	if c := chatRowCounters(t, joiner, sp.Id, obj.ObjectId); c.messages != 0 {
		t.Errorf("joiner: own reply must stay read on the author, counters=%v", c)
	}
	mustStatus(t, http.MethodPost, ownerBase+"/chat/read-all", "", http.StatusNoContent)
	if !pollUntil(30*time.Second, func() bool {
		return chatRowCounters(t, owner, sp.Id, obj.ObjectId).messages == 0
	}) {
		t.Fatalf("owner: read-all never cleared the reply's unread")
	}

	// --- (4) Cross-account reaction: reaction counter moves, message
	// counter doesn't.
	heart := url.PathEscape("❤️")
	mustStatus(t, http.MethodPost,
		ownerBase+"/chat/messages/"+m4.Id+"/reactions/"+heart, "", http.StatusOK)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		_, ok := findById(chatMessages(t, joinerBase), m4.Id).Reactions["❤️"][ownerId]
		return ok
	}) {
		t.Fatalf("joiner never saw the owner's ❤️ reaction")
	}
	if !pollUntil(30*time.Second, func() bool {
		lastList = chatMessages(t, joinerBase)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return findById(lastList, m4.Id).UnreadReactions &&
			lastCounters.reactions == 1 && lastCounters.messages == 0
	}) {
		t.Fatalf("joiner: cross-account reaction never materialized: "+
			"m4.unreadReactions=%v counters=%v",
			findById(lastList, m4.Id).UnreadReactions, lastCounters)
	}

	// --- (4b) Audience: a reaction badges the message's AUTHOR only.
	// The owner reacting to its own m3 syncs to the joiner but must not
	// move the joiner's flag or counter (still the 1 pending ❤️ on m4).
	party := url.PathEscape("🎉")
	mustStatus(t, http.MethodPost,
		ownerBase+"/chat/messages/"+m3.Id+"/reactions/"+party, "", http.StatusOK)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		_, ok := findById(chatMessages(t, joinerBase), m3.Id).Reactions["🎉"][ownerId]
		return ok
	}) {
		t.Fatalf("joiner never saw the owner's 🎉 self-reaction")
	}
	lastList = chatMessages(t, joinerBase)
	lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
	if findById(lastList, m3.Id).UnreadReactions || lastCounters.reactions != 1 {
		t.Errorf("joiner: reaction on the owner's own message must not badge the joiner: "+
			"m3.unreadReactions=%v counters=%v",
			findById(lastList, m3.Id).UnreadReactions, lastCounters)
	}

	// --- (5a) Retracting the reaction before the joiner reads leaves no
	// trace — the classifier's supersede key withdraws the pending entry.
	mustStatus(t, http.MethodPost,
		ownerBase+"/chat/messages/"+m4.Id+"/reactions/"+heart, "", http.StatusOK)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		_, ok := findById(chatMessages(t, joinerBase), m4.Id).Reactions["❤️"][ownerId]
		return !ok
	}) {
		t.Fatalf("joiner never saw the ❤️ reaction retract")
	}
	if !pollUntil(30*time.Second, func() bool {
		lastList = chatMessages(t, joinerBase)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return !findById(lastList, m4.Id).UnreadReactions && lastCounters.reactions == 0
	}) {
		t.Fatalf("joiner: retracted reaction left unread state behind: counters=%v", lastCounters)
	}

	// --- (5b) A live reaction clears through read-all. Fresh emoji =
	// fresh supersede key, so this can't collide with the retracted ❤️.
	thumb := url.PathEscape("👍")
	mustStatus(t, http.MethodPost,
		ownerBase+"/chat/messages/"+m4.Id+"/reactions/"+thumb, "", http.StatusOK)
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		_, ok := findById(chatMessages(t, joinerBase), m4.Id).Reactions["👍"][ownerId]
		return ok
	}) {
		t.Fatalf("joiner never saw the owner's 👍 reaction")
	}
	if !pollUntil(30*time.Second, func() bool {
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return lastCounters.reactions == 1
	}) {
		t.Fatalf("joiner: 👍 never counted unread: counters=%v", lastCounters)
	}
	mustStatus(t, http.MethodPost, joinerBase+"/chat/read-all", "", http.StatusNoContent)
	if !pollUntil(30*time.Second, func() bool {
		lastList = chatMessages(t, joinerBase)
		lastCounters = chatRowCounters(t, joiner, sp.Id, obj.ObjectId)
		return !findById(lastList, m4.Id).UnreadReactions &&
			lastCounters.reactions == 0 && lastCounters.messages == 0
	}) {
		t.Fatalf("joiner: read-all never cleared the reaction: counters=%v", lastCounters)
	}

	// Nothing in this test mentions anybody, so the mention counter
	// must never have moved — mention badging has its own acceptance
	// test (TestE2E_MultipeerChatMentions).
	if lastCounters.mentions != 0 {
		t.Errorf("unreadMentions moved without any mention: %v", lastCounters)
	}
}

// TestE2E_ChatReactionsRead is the acceptance test for the per-message
// reaction-read route (POST .../messages/:msgId/reactions-read), and it
// pins the route's ACTUAL semantics — which are subtler than "clear just
// the reaction". Marking a reaction change read goes through the SDK's
// MarkRead, and MarkRead covers the change AND its causal ancestry. So
// reactions-read on a message clears:
//
//   - the unread reaction itself, and
//   - any unread MESSAGE the reactor had already seen when they reacted
//     (everything causally before the reaction),
//
// while unread messages that arrived AFTER the reaction stay unread —
// and that surviving tail is what still separates this from read-all.
// The scenario arranges all three: m2 (written before the reaction, a
// causal ancestor → cleared), the reaction on m1 (cleared), and m3
// (written after the reaction, a causal descendant → survives). See
// chat.ReadReactions / docs/16-chat.md § Scope caveat for the why.
//
// Asserted on the OWNER (a reaction badges the reacted-to message's
// AUTHOR). Convergence gates on chat_messages predicates
// (pollUntilSynced); flag/counter materialization is local and debounced
// so those use plain pollUntil (the harness caveat baked into this file).
func TestE2E_ChatReactionsRead(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("reaction-read test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	owner := startPeer(t, bin, "owner")
	defer owner.stop(t)
	joiner := startPeer(t, bin, "joiner")
	defer joiner.stop(t)

	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces",
		`{"name":"reaction-read"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, owner.base+"/v1/spaces/"+sp.Id+"/objects",
		`{}`, http.StatusCreated, &obj)

	ownerBase := owner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId
	joinerBase := joiner.base + "/v1/spaces/" + sp.Id + "/objects/" + obj.ObjectId

	// Pre-join message authored by the owner — the one the joiner reacts
	// to (a reaction badges the message's author, so the owner is the
	// audience). Its ensureType attaches the chat type before the joiner
	// cold-syncs.
	m1 := sendChat(t, ownerBase, `{"text":"owner message"}`)
	ownerId := m1.Creator
	if ownerId == "" {
		t.Fatalf("m1 not stamped with creator: %+v", m1)
	}

	joinSpace(t, owner, joiner, sp.Id, api.SpacePermissionWriter)

	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{owner, joiner}, func() bool {
		return findById(chatMessages(t, joinerBase), m1.Id).Id == m1.Id
	}) {
		t.Fatalf("joiner never converged on the owner's message")
	}
	// Baseline the joiner's read state so its own writes below are the
	// only unread things on the owner.
	mustStatus(t, http.MethodPost, joinerBase+"/chat/read-all", "", http.StatusNoContent)

	// The joiner authors m2, then reacts to m1, then authors m3 — in that
	// order on the joiner's single node, so the DAG is a linear chain:
	// m2 is a causal ANCESTOR of the reaction (written before it), and m3
	// is a causal DESCENDANT (written after it). Both land unread on the
	// owner alongside the reaction.
	m2 := sendChat(t, joinerBase, `{"text":"before the reaction"}`)
	joinerId := m2.Creator
	if joinerId == "" || joinerId == ownerId {
		t.Fatalf("creator stamps broken: owner=%q joiner=%q", ownerId, joinerId)
	}
	heart := url.PathEscape("❤️")
	mustStatus(t, http.MethodPost,
		joinerBase+"/chat/messages/"+m1.Id+"/reactions/"+heart, "", http.StatusOK)
	m3 := sendChat(t, joinerBase, `{"text":"after the reaction"}`)

	// Owner converges on all three writes (m2, the reaction, m3).
	if !pollUntilSynced(t, 3*time.Minute, sp.Id, []*peer{joiner, owner}, func() bool {
		list := chatMessages(t, ownerBase)
		_, reacted := findById(list, m1.Id).Reactions["❤️"][joinerId]
		return findById(list, m2.Id).Id != "" && findById(list, m3.Id).Id != "" && reacted
	}) {
		t.Fatalf("owner never converged on m2 + reaction + m3")
	}

	// Owner now holds: 2 unread messages (m2, m3) + 1 unread reaction (m1).
	var lastList chatListResp
	var lastCounters rowCounters
	if !pollUntil(30*time.Second, func() bool {
		lastList = chatMessages(t, ownerBase)
		lastCounters = chatRowCounters(t, owner, sp.Id, obj.ObjectId)
		return findById(lastList, m1.Id).UnreadReactions &&
			findById(lastList, m2.Id).Unread &&
			findById(lastList, m3.Id).Unread &&
			lastCounters.reactions == 1 && lastCounters.messages == 2
	}) {
		t.Fatalf("owner: initial unread state never materialized: "+
			"m1.rx=%v m2.unread=%v m3.unread=%v counters=%v",
			findById(lastList, m1.Id).UnreadReactions,
			findById(lastList, m2.Id).Unread,
			findById(lastList, m3.Id).Unread, lastCounters)
	}

	// reactions-read on m1 clears the reaction AND its causal ancestry:
	// the reaction goes away, m2 (written before the reaction) is marked
	// read as an ancestor, and m3 (written after) survives as unread. So
	// messages drops 2 -> 1, NOT to 0 — that surviving m3 is exactly what
	// still distinguishes this route from read-all.
	mustStatus(t, http.MethodPost,
		ownerBase+"/chat/messages/"+m1.Id+"/reactions-read", "", http.StatusNoContent)
	if !pollUntil(30*time.Second, func() bool {
		lastList = chatMessages(t, ownerBase)
		lastCounters = chatRowCounters(t, owner, sp.Id, obj.ObjectId)
		return !findById(lastList, m1.Id).UnreadReactions && // reaction cleared
			!findById(lastList, m2.Id).Unread && //             ancestor cleared
			findById(lastList, m3.Id).Unread && //              descendant survives
			lastCounters.reactions == 0 && lastCounters.messages == 1
	}) {
		t.Fatalf("owner: reactions-read semantics wrong "+
			"(want reaction+ancestor cleared, descendant kept): "+
			"m1.rx=%v m2.unread=%v m3.unread=%v counters=%v",
			findById(lastList, m1.Id).UnreadReactions,
			findById(lastList, m2.Id).Unread,
			findById(lastList, m3.Id).Unread, lastCounters)
	}

	// Idempotency: a second call on m1 (no reaction left) and a call on a
	// never-reacted message (m3) are both clean 204 no-ops; the surviving
	// m3 unread must not move.
	mustStatus(t, http.MethodPost,
		ownerBase+"/chat/messages/"+m1.Id+"/reactions-read", "", http.StatusNoContent)
	mustStatus(t, http.MethodPost,
		ownerBase+"/chat/messages/"+m3.Id+"/reactions-read", "", http.StatusNoContent)
	// Give the (no-op) marks a beat to fail loudly if they touched state.
	if !pollUntil(5*time.Second, func() bool {
		lastCounters = chatRowCounters(t, owner, sp.Id, obj.ObjectId)
		return lastCounters.reactions == 0 && lastCounters.messages == 1
	}) {
		t.Fatalf("owner: idempotent reactions-read moved the counters: %v", lastCounters)
	}
}

// TestE2E_OneToOneChatReadTracking is the 1-1 (DM) variant — SYN-61's
// primary surface. A 1-1 space has no invite handshake and an
// immutable two-writer ACL, so the membership path differs from a
// shared space end to end; this pins that read tracking behaves the
// same there: messages from the other side set the flag AND the row
// counter in both directions, read-all clears them, and the full
// reaction flow holds — a reaction badges the reacted-to message's
// author only, retract leaves no trace, read-all clears a live one.
// The read-boundary matrix stays in TestE2E_MultipeerChatReadTracking
// — same machinery, no need to repeat it here.
func TestE2E_OneToOneChatReadTracking(t *testing.T) {
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}
	if testing.Short() {
		t.Skip("1-1 read-tracking test takes minutes; rerun without -short")
	}

	bin := buildBinary(t)
	alice := startPeer(t, bin, "alice")
	defer alice.stop(t)
	bob := startPeer(t, bin, "bob")
	defer bob.stop(t)

	var accA, accB api.AccountResponse
	mustJSON(t, http.MethodGet, alice.base+"/v1/account", "", http.StatusOK, &accA)
	mustJSON(t, http.MethodGet, bob.base+"/v1/account", "", http.StatusOK, &accB)

	// Derive + accept: the minimal handshake, asserted in detail by
	// TestE2E_MultipeerOneToOne.
	var oneOne api.SpaceInfo
	mustJSON(t, http.MethodPost, alice.base+"/v1/spaces/one-to-one",
		`{"otherIdentity":"`+accB.Id+`"}`, http.StatusCreated, &oneOne)
	mustStatus(t, http.MethodPost, bob.base+"/v1/spaces/one-to-one/register-incoming",
		`{"peerIdentity":"`+accA.Id+`"}`, http.StatusNoContent)
	mustJSON(t, http.MethodPost, bob.base+"/v1/spaces/"+oneOne.Id+"/one-to-one/accept",
		"", http.StatusOK, new(api.SpaceInfo))

	var obj api.ObjectsCreateResponse
	mustJSON(t, http.MethodPost, alice.base+"/v1/spaces/"+oneOne.Id+"/objects",
		`{}`, http.StatusCreated, &obj)
	aliceObj := alice.base + "/v1/spaces/" + oneOne.Id + "/objects/" + obj.ObjectId
	bobObj := bob.base + "/v1/spaces/" + oneOne.Id + "/objects/" + obj.ObjectId

	// First DM message + explicit baseline on bob. SYN-61 notes that
	// with seed-at-first-sight a brand-new DM's first message may seed
	// read on the recipient (tree first loads with it already present);
	// whether that's the right default is an open design question — so
	// the counter contract is pinned on the SECOND message and the
	// baseline makes the test deterministic either way.
	m1 := sendChat(t, aliceObj, `{"text":"hi from alice"}`)
	if !pollUntilSynced(t, 3*time.Minute, oneOne.Id, []*peer{alice, bob}, func() bool {
		return findById(chatMessages(t, bobObj), m1.Id).Id == m1.Id
	}) {
		t.Fatalf("bob never converged on alice's first DM message")
	}
	mustStatus(t, http.MethodPost, bobObj+"/chat/read-all", "", http.StatusNoContent)

	// Alice → Bob: flag + counter on bob.
	m2 := sendChat(t, aliceObj, `{"text":"unread in the dm"}`)
	if !pollUntilSynced(t, 3*time.Minute, oneOne.Id, []*peer{alice, bob}, func() bool {
		return findById(chatMessages(t, bobObj), m2.Id).Id == m2.Id
	}) {
		t.Fatalf("bob never converged on m2")
	}
	var lastCounters rowCounters
	var lastMsg chatMsg
	if !pollUntil(30*time.Second, func() bool {
		lastMsg = findById(chatMessages(t, bobObj), m2.Id)
		lastCounters = chatRowCounters(t, bob, oneOne.Id, obj.ObjectId)
		return lastMsg.Unread && lastCounters.messages == 1
	}) {
		t.Fatalf("bob: DM message never materialized as unread (SYN-61): m2.unread=%v counters=%v",
			lastMsg.Unread, lastCounters)
	}

	// read-all clears flag + counter.
	mustStatus(t, http.MethodPost, bobObj+"/chat/read-all", "", http.StatusNoContent)
	if !pollUntil(30*time.Second, func() bool {
		lastMsg = findById(chatMessages(t, bobObj), m2.Id)
		lastCounters = chatRowCounters(t, bob, oneOne.Id, obj.ObjectId)
		return !lastMsg.Unread && lastCounters.messages == 0
	}) {
		t.Fatalf("bob: read-all never cleared the DM unread: m2.unread=%v counters=%v",
			lastMsg.Unread, lastCounters)
	}

	// Bob → Alice: the counter works in the other direction too, and
	// stays clean on the author.
	m3 := sendChat(t, bobObj, `{"text":"reply from bob"}`)
	if !pollUntilSynced(t, 3*time.Minute, oneOne.Id, []*peer{bob, alice}, func() bool {
		return findById(chatMessages(t, aliceObj), m3.Id).Id == m3.Id
	}) {
		t.Fatalf("alice never converged on bob's reply")
	}
	if !pollUntil(30*time.Second, func() bool {
		lastMsg = findById(chatMessages(t, aliceObj), m3.Id)
		lastCounters = chatRowCounters(t, alice, oneOne.Id, obj.ObjectId)
		return lastMsg.Unread && lastCounters.messages == 1
	}) {
		t.Fatalf("alice: bob's DM reply never went unread: m3.unread=%v counters=%v",
			lastMsg.Unread, lastCounters)
	}
	if c := chatRowCounters(t, bob, oneOne.Id, obj.ObjectId); c.messages != 0 {
		t.Errorf("bob: own reply must stay read on the author, counters=%v", c)
	}

	// --- Full reaction flow, DM edition. m3 is bob's message, m2 is
	// alice's own; identities for the reaction map checks come from
	// the creator stamps.
	aliceId, bobId := m1.Creator, m3.Creator
	if aliceId == "" || bobId == "" || aliceId == bobId {
		t.Fatalf("creator stamps broken: alice=%q bob=%q", aliceId, bobId)
	}

	// Alice reacts to bob's message → bob (the author) gets the flag
	// and the reaction counter; his message counter stays clean.
	heart := url.PathEscape("❤️")
	mustStatus(t, http.MethodPost,
		aliceObj+"/chat/messages/"+m3.Id+"/reactions/"+heart, "", http.StatusOK)
	if !pollUntilSynced(t, 3*time.Minute, oneOne.Id, []*peer{alice, bob}, func() bool {
		_, ok := findById(chatMessages(t, bobObj), m3.Id).Reactions["❤️"][aliceId]
		return ok
	}) {
		t.Fatalf("bob never saw alice's ❤️ reaction")
	}
	if !pollUntil(30*time.Second, func() bool {
		lastMsg = findById(chatMessages(t, bobObj), m3.Id)
		lastCounters = chatRowCounters(t, bob, oneOne.Id, obj.ObjectId)
		return lastMsg.UnreadReactions &&
			lastCounters.reactions == 1 && lastCounters.messages == 0
	}) {
		t.Fatalf("bob: DM reaction never materialized: m3.unreadReactions=%v counters=%v",
			lastMsg.UnreadReactions, lastCounters)
	}

	// Audience: alice reacting to her OWN m2 syncs to bob without
	// badging him (still just the ❤️ pending on m3).
	party := url.PathEscape("🎉")
	mustStatus(t, http.MethodPost,
		aliceObj+"/chat/messages/"+m2.Id+"/reactions/"+party, "", http.StatusOK)
	if !pollUntilSynced(t, 3*time.Minute, oneOne.Id, []*peer{alice, bob}, func() bool {
		_, ok := findById(chatMessages(t, bobObj), m2.Id).Reactions["🎉"][aliceId]
		return ok
	}) {
		t.Fatalf("bob never saw alice's 🎉 self-reaction")
	}
	lastList := chatMessages(t, bobObj)
	lastCounters = chatRowCounters(t, bob, oneOne.Id, obj.ObjectId)
	if findById(lastList, m2.Id).UnreadReactions || lastCounters.reactions != 1 {
		t.Errorf("bob: reaction on alice's own message must not badge bob: "+
			"m2.unreadReactions=%v counters=%v",
			findById(lastList, m2.Id).UnreadReactions, lastCounters)
	}

	// Retract before bob looks: no trace left.
	mustStatus(t, http.MethodPost,
		aliceObj+"/chat/messages/"+m3.Id+"/reactions/"+heart, "", http.StatusOK)
	if !pollUntilSynced(t, 3*time.Minute, oneOne.Id, []*peer{alice, bob}, func() bool {
		_, ok := findById(chatMessages(t, bobObj), m3.Id).Reactions["❤️"][aliceId]
		return !ok
	}) {
		t.Fatalf("bob never saw the ❤️ retract")
	}
	if !pollUntil(30*time.Second, func() bool {
		lastMsg = findById(chatMessages(t, bobObj), m3.Id)
		lastCounters = chatRowCounters(t, bob, oneOne.Id, obj.ObjectId)
		return !lastMsg.UnreadReactions && lastCounters.reactions == 0
	}) {
		t.Fatalf("bob: retracted DM reaction left unread state: counters=%v", lastCounters)
	}

	// A live reaction clears through read-all (fresh emoji, fresh
	// supersede key).
	thumb := url.PathEscape("👍")
	mustStatus(t, http.MethodPost,
		aliceObj+"/chat/messages/"+m3.Id+"/reactions/"+thumb, "", http.StatusOK)
	if !pollUntilSynced(t, 3*time.Minute, oneOne.Id, []*peer{alice, bob}, func() bool {
		_, ok := findById(chatMessages(t, bobObj), m3.Id).Reactions["👍"][aliceId]
		return ok
	}) {
		t.Fatalf("bob never saw alice's 👍 reaction")
	}
	if !pollUntil(30*time.Second, func() bool {
		return chatRowCounters(t, bob, oneOne.Id, obj.ObjectId).reactions == 1
	}) {
		t.Fatalf("bob: 👍 never counted unread")
	}
	mustStatus(t, http.MethodPost, bobObj+"/chat/read-all", "", http.StatusNoContent)
	if !pollUntil(30*time.Second, func() bool {
		lastMsg = findById(chatMessages(t, bobObj), m3.Id)
		lastCounters = chatRowCounters(t, bob, oneOne.Id, obj.ObjectId)
		return !lastMsg.UnreadReactions && lastCounters.reactions == 0
	}) {
		t.Fatalf("bob: read-all never cleared the DM reaction: counters=%v", lastCounters)
	}
}
