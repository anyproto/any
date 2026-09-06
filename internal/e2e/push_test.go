// Push-notification e2e (SYN-47 M5). Runs only against a reachable
// anytype-push-server — gate on ANY_PUSH_E2E_PEER_ID +
// ANY_PUSH_E2E_ADDRS (skipped when unset). The push server's own
// Redis/Mongo deps are the operator's problem, never stood up here;
// local recipe in docs/20-push.md § Local e2e.
//
// Delivery to FCM/APNs is fire-and-forget, so assertions stop at the
// DRPC-visible surface: the token round-trips locally, chat writes
// with a mention succeed with the hooks armed, and the account's
// server-held topic set converges to the expected bulk topics
// (register + SubscribeAll actually reached the push node).
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

func TestE2E_Push(t *testing.T) {
	peerId := os.Getenv("ANY_PUSH_E2E_PEER_ID")
	addrsRaw := os.Getenv("ANY_PUSH_E2E_ADDRS")
	if peerId == "" || addrsRaw == "" {
		t.Skip("push e2e disabled: set ANY_PUSH_E2E_PEER_ID and ANY_PUSH_E2E_ADDRS to a running anytype-push-server")
	}
	if _, err := os.Stat(stagingFixture); err != nil {
		t.Skipf("staging fixture not present at %s: %v", stagingFixture, err)
	}

	bin := buildBinary(t)
	dataDir := t.TempDir()
	addr := freeLoopbackAddr(t)

	// startServerConf writes its own config, so build the push-enabled
	// one by hand and reuse the init + exec plumbing.
	var addrLines strings.Builder
	for _, a := range strings.Split(addrsRaw, ",") {
		if a = strings.TrimSpace(a); a != "" {
			fmt.Fprintf(&addrLines, "    - %q\n", a)
		}
	}
	cfgBody := fmt.Sprintf(
		"network:\n  nodeconfPath: %s\nindex:\n  embedder: none\npush:\n  peerId: %s\n  addrs:\n%s",
		absStagingPath(t), peerId, addrLines.String())
	cfgPath := filepath.Join(dataDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	initCmd := exec.Command(bin, "init", "--config", cfgPath, "--data-dir", dataDir)
	initCmd.Env = append(os.Environ(), "ANY_DATA_DIR="+dataDir)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("any init: %v\n%s", err, out)
	}
	srv := execServer(t, bin, cfgPath, dataDir, addr)
	defer srv.stop(t)
	waitForReady(t, addr, 60*time.Second)
	base := "http://" + addr

	// Token set → local status round-trip.
	mustStatus(t, http.MethodPost, base+"/v1/push/token",
		`{"platform":"android","token":"any-e2e-push-token"}`, http.StatusNoContent)
	var st api.PushTokenStatus
	mustJSON(t, http.MethodGet, base+"/v1/push/token", "", http.StatusOK, &st)
	if !st.Registered || st.Platform != "android" {
		t.Fatalf("token status after set = %+v, want registered android", st)
	}

	// A space + a chat message with a (self-)mention: exercises
	// RegisterSpace on create and the send hook's read-back + Notify
	// enqueue. The send must not error with the hooks armed.
	var acc api.AccountResponse
	mustJSON(t, http.MethodGet, base+"/v1/account", "", http.StatusOK, &acc)
	var sp api.SpaceInfo
	mustJSON(t, http.MethodPost, base+"/v1/spaces",
		`{"name":"push-e2e"}`, http.StatusCreated, &sp)
	var obj api.ObjectsCreateResponse
	obj.ObjectId = createModuleObject(t, base, sp.Id, "chat")
	msg := fmt.Sprintf(`{"text":"ping [me](any://m/%s/%s)"}`, sp.Id, acc.Id)
	sendChat(t, base+"/v1/spaces/"+sp.Id+"/objects/"+obj.ObjectId, msg)

	// Subscriptions round-trip: the sync loop (kicked by the space-list
	// event, debounced) must land the bulk topics for the new space on
	// the push node — default space mode "all" ⇒ ["chats", <identity>].
	var subs api.PushSubscriptionsResponse
	if !pollUntil(2*time.Minute, func() bool {
		resp, raw := doRequest(t, http.MethodGet, base+"/v1/push/subscriptions", "")
		if resp.StatusCode != http.StatusOK {
			return false
		}
		subs = api.PushSubscriptionsResponse{}
		if err := json.Unmarshal(raw, &subs); err != nil {
			return false
		}
		var haveChats, haveIdentity bool
		for _, s := range subs.Subscriptions {
			if s.SpaceKey == "" {
				continue
			}
			switch s.Topic {
			case "chats":
				haveChats = true
			case acc.Id:
				haveIdentity = true
			}
		}
		return haveChats && haveIdentity
	}) {
		t.Fatalf("server-held subscriptions never converged to the bulk topics: %+v", subs)
	}

	// Revoke → local status flips back.
	mustStatus(t, http.MethodDelete, base+"/v1/push/token", "", http.StatusNoContent)
	var st2 api.PushTokenStatus
	mustJSON(t, http.MethodGet, base+"/v1/push/token", "", http.StatusOK, &st2)
	if st2.Registered {
		t.Fatalf("token status after revoke = %+v, want unregistered", st2)
	}
}
