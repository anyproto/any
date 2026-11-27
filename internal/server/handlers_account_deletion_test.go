package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/space"
	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

func decodeAccountDeletion(t *testing.T, rec *httptest.ResponseRecorder) api.AccountDeletion {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out api.AccountDeletion
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return out
}

func getAccount(t *testing.T, e http.Handler) (api.AccountResponse, []byte) {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/account", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/account: %d %s", rec.Code, rec.Body.String())
	}
	var out api.AccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return out, rec.Body.Bytes()
}

// awaitDeletionEvent reads the bus until an account.deletion event
// carries state.
func awaitDeletionEvent(t *testing.T, ch <-chan api.Event, state string) api.AccountDeletion {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != api.EventAccountDeletion || ev.Scope != api.EventScopeDevice || ev.Sender == nil || !ev.Sender.Self {
				t.Fatalf("envelope = %+v", ev)
			}
			var data api.AccountDeletion
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data.State == state {
				return data
			}
		case <-deadline:
			t.Fatalf("no account.deletion event with state %q", state)
		}
	}
}

// TestServer_AccountDeletion requests the account's deletion on the
// network the tests boot on, reads it back, reverts it, and then
// restarts the account with every node refusing the connection: the
// state reads from the device and the writes answer that the network is
// unavailable.
func TestServer_AccountDeletion(t *testing.T) {
	raw, err := os.ReadFile(stagingPath)
	if err != nil {
		t.Skipf("staging config not available: %v", err)
	}
	d := newUnauthorizedDeps(t)
	e := buildEcho(d)
	accountId := authorize(t, e, "")
	subId, events := d.eventsHub().subscribe(eventFilter{types: []string{api.EventAccountDeletion}})
	defer d.eventsHub().unsubscribe(subId)

	// The network registers an account on its first push.
	var rec *httptest.ResponseRecorder
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(time.Second) {
		rec = doJSON(t, e, http.MethodPost, "/v1/account/deletion", "")
		if rec.Code == http.StatusOK {
			break
		}
		code := errCode(t, rec.Body.Bytes())
		if (code != api.ErrAccountNotOnNetwork && code != api.ErrAccountNetworkUnavailable) || time.Now().After(deadline) {
			t.Fatalf("POST /v1/account/deletion: %d %s", rec.Code, rec.Body.String())
		}
	}
	pending := decodeAccountDeletion(t, rec)
	if pending.State != "pending" || pending.DeleteAt == nil || !pending.DeleteAt.After(time.Now()) {
		t.Fatalf("request = %s, want pending with a date ahead", rec.Body.String())
	}
	if age := time.Since(pending.FetchedAt); age < 0 || age > time.Minute {
		t.Errorf("fetchedAt = %s, want a live answer", pending.FetchedAt)
	}
	// The wire names, read without the response type.
	var wire map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"state", "deleteAt", "fetchedAt"} {
		if _, ok := wire[f]; !ok {
			t.Errorf("%s missing in %s", f, rec.Body.String())
		}
	}
	if len(wire) != 3 {
		t.Errorf("request = %v, want exactly state, deleteAt, fetchedAt", wire)
	}

	if ev := awaitDeletionEvent(t, events, "pending"); ev.DeleteAt == nil || !ev.DeleteAt.Equal(*pending.DeleteAt) {
		t.Errorf("event = %+v, want the date %s", ev, pending.DeleteAt)
	}
	account, body := getAccount(t, e)
	if account.Deletion == nil || account.Deletion.State != "pending" ||
		account.Deletion.DeleteAt == nil || !account.Deletion.DeleteAt.Equal(*pending.DeleteAt) {
		t.Fatalf("GET /v1/account = %s, want the pending deletion", body)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/account/deletion", "")
	if rec.Code != http.StatusConflict || errCode(t, rec.Body.Bytes()) != api.ErrAccountDeletionPending {
		t.Fatalf("second request: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, e, http.MethodDelete, "/v1/account/deletion", "")
	if active := decodeAccountDeletion(t, rec); active.State != "active" || active.DeleteAt != nil {
		t.Fatalf("revert = %s, want active with no date", rec.Body.String())
	}
	awaitDeletionEvent(t, events, "active")
	if account, body = getAccount(t, e); account.Deletion == nil || account.Deletion.State != "active" || account.Deletion.DeleteAt != nil {
		t.Fatalf("GET /v1/account after the revert = %s", body)
	}
	rec = doJSON(t, e, http.MethodDelete, "/v1/account/deletion", "")
	if rec.Code != http.StatusConflict || errCode(t, rec.Body.Bytes()) != api.ErrAccountDeletionNotPending {
		t.Fatalf("second revert: %d %s", rec.Code, rec.Body.String())
	}

	// Pending again, then the same account with the network cut off.
	pending = decodeAccountDeletion(t, doJSON(t, e, http.MethodPost, "/v1/account/deletion", ""))
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)
	forgetStoredNodeconf(t, config.AccountDir(d.root, accountId))
	d.cfg.Network = config.Network{Nodeconf: editNodeconf(t, raw, func(conf map[string]any) {
		nodes, _ := conf["nodes"].([]any)
		for _, n := range nodes {
			n.(map[string]any)["addresses"] = []any{"127.0.0.1:1"}
		}
	})}
	authorize(t, e, accountId)

	start := time.Now()
	account, body = getAccount(t, e)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %s: refused connections must not hold the reply", took)
	}
	if account.Deletion == nil || *account.Deletion.DeleteAt != *pending.DeleteAt ||
		account.Deletion.State != "pending" || !account.Deletion.FetchedAt.Equal(pending.FetchedAt) {
		t.Fatalf("offline GET /v1/account = %s, want the stored %+v", body, pending)
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		rec = doJSON(t, e, method, "/v1/account/deletion", "")
		if rec.Code != http.StatusServiceUnavailable || errCode(t, rec.Body.Bytes()) != api.ErrAccountNetworkUnavailable {
			t.Errorf("offline %s: %d %s", method, rec.Code, rec.Body.String())
		}
	}
}

// TestServer_AccountDeletion_NoNetwork: a device that never reached a
// network reports no deletion state, and a managed server takes the
// writes only with the control token.
func TestServer_AccountDeletion_NoNetwork(t *testing.T) {
	d := newUnauthorizedDeps(t)
	d.cfg.Network = config.Network{Nodeconf: string(config.NodeconfPlaceholder())}
	d.cfg.Mode = config.ModeManaged
	d.controlToken = "tok"
	e := buildEcho(d)
	token := map[string]string{api.ControlTokenHeader: "tok"}
	if rec := doJSONH(t, e, http.MethodPost, "/v1/auth", `{}`, token); rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/auth: %d %s", rec.Code, rec.Body.String())
	}

	if account, body := getAccount(t, e); account.Deletion != nil {
		t.Errorf("GET /v1/account = %s, want no deletion state", body)
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		rec := doJSON(t, e, method, "/v1/account/deletion", "")
		if rec.Code != http.StatusForbidden || errCode(t, rec.Body.Bytes()) != "control.forbidden" {
			t.Errorf("%s without the token: %d %s", method, rec.Code, rec.Body.String())
		}
		rec = doJSONH(t, e, method, "/v1/account/deletion", "", token)
		if rec.Code != http.StatusServiceUnavailable || errCode(t, rec.Body.Bytes()) != api.ErrAccountNetworkUnavailable {
			t.Errorf("%s with no network: %d %s", method, rec.Code, rec.Body.String())
		}
	}
}

// TestAccount_DeletionBusBridge: the engine turns a change of the
// deletion state into one device-scope account.deletion event.
func TestAccount_DeletionBusBridge(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	id, ch := d.eventsHub().subscribe(eventFilter{types: []string{api.EventAccountDeletion}})
	defer d.eventsHub().unsubscribe(id)

	deleteAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	d.accountDeletionFor(d.eng)(space.AccountDeletion{State: space.AccountDeletionPending, DeleteAt: deleteAt, FetchedAt: time.Now().UTC()})
	if ev := awaitDeletionEvent(t, ch, "pending"); ev.DeleteAt == nil || !ev.DeleteAt.Equal(deleteAt) {
		t.Errorf("event = %+v, want the date %s", ev, deleteAt)
	}
}

// TestServer_AccountDeletion_RevertFromWaitingLogin: a device that holds
// only the key of an account whose deletion is pending logs in, reads
// the state on the waiting login, and reverts the deletion from there.
// Nothing else of the account answers until the login ends.
func TestServer_AccountDeletion_RevertFromWaitingLogin(t *testing.T) {
	if _, err := os.ReadFile(stagingPath); err != nil {
		t.Skipf("staging config not available: %v", err)
	}
	// The device that requests the deletion, then goes away: nothing of
	// the account stays reachable but the network.
	first := newUnauthorizedDeps(t)
	e1 := buildEcho(first)
	created := postAuth(t, e1, `{}`)
	if created.Mnemonic == "" {
		t.Fatalf("create reply carries no phrase: %+v", created)
	}
	var rec *httptest.ResponseRecorder
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(time.Second) {
		rec = doJSON(t, e1, http.MethodPost, "/v1/account/deletion", "")
		if rec.Code == http.StatusOK {
			break
		}
		code := errCode(t, rec.Body.Bytes())
		if (code != api.ErrAccountNotOnNetwork && code != api.ErrAccountNetworkUnavailable) || time.Now().After(deadline) {
			t.Fatalf("POST /v1/account/deletion: %d %s", rec.Code, rec.Body.String())
		}
	}
	pending := decodeAccountDeletion(t, rec)
	first.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)

	// The device that holds only the key. The first sync is held back,
	// so the login waits whatever the nodes answer.
	d := newUnauthorizedDeps(t)
	synced := make(chan struct{})
	d.firstSync = func(ctx context.Context, _ *anysyncsdk.SDK) error {
		select {
		case <-synced:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	e := buildEcho(d)
	login := postAuth(t, e, `{"mnemonic":`+strconv.Quote(created.Mnemonic)+`}`)
	if login.AccountId != created.AccountId || login.Login == nil {
		t.Fatalf("login reply: %+v", login)
	}
	loginDeletion := func(state string) *api.AccountDeletion {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			st := authStatusOf(t, e)
			if st.Login == nil {
				t.Fatalf("the login ended: %+v", st)
			}
			if st.Login.Deletion != nil && st.Login.Deletion.State == state {
				return st.Login.Deletion
			}
			if time.Now().After(deadline) {
				t.Fatalf("login.deletion = %+v, want state %q", st.Login.Deletion, state)
			}
		}
	}
	if got := loginDeletion("pending"); got.DeleteAt == nil || !got.DeleteAt.Equal(*pending.DeleteAt) {
		t.Errorf("login.deletion = %+v, want the date %s", got, pending.DeleteAt)
	}
	for _, req := range [][2]string{{http.MethodGet, "/v1/account"}, {http.MethodPost, "/v1/account/deletion"}} {
		rec = doJSON(t, e, req[0], req[1], "")
		if rec.Code != http.StatusServiceUnavailable || errCode(t, rec.Body.Bytes()) != "auth.login_pending" {
			t.Errorf("%s %s while the login waits: %d %s", req[0], req[1], rec.Code, rec.Body.String())
		}
	}

	rec = doJSON(t, e, http.MethodDelete, "/v1/account/deletion", "")
	if active := decodeAccountDeletion(t, rec); active.State != "active" || active.DeleteAt != nil {
		t.Fatalf("revert from the waiting login = %s", rec.Body.String())
	}
	loginDeletion("active")
	rec = doJSON(t, e, http.MethodDelete, "/v1/account/deletion", "")
	if rec.Code != http.StatusConflict || errCode(t, rec.Body.Bytes()) != api.ErrAccountDeletionNotPending {
		t.Fatalf("second revert: %d %s", rec.Code, rec.Body.String())
	}

	// The login ends and the account reads the same state.
	close(synced)
	waitAuthorized(t, e, created.AccountId)
	if account, body := getAccount(t, e); account.Deletion == nil || account.Deletion.State != "active" {
		t.Fatalf("GET /v1/account after the login = %s", body)
	}
}

// TestServer_AccountDeletion_WaitingLoginGate: the revert a waiting
// login serves is gated like the open one — a managed server takes it
// only with the control token — and it is the only write let through.
func TestServer_AccountDeletion_WaitingLoginGate(t *testing.T) {
	phrase, _ := otherMnemonic(t)
	d, e := offlineDeps(t)
	d.cfg.Mode = config.ModeManaged
	d.controlToken = "tok"
	token := map[string]string{api.ControlTokenHeader: "tok"}
	if rec := doJSONH(t, e, http.MethodPost, "/v1/auth", `{"mnemonic":`+strconv.Quote(phrase)+`}`, token); rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/auth: %d %s", rec.Code, rec.Body.String())
	}
	if st := authStatusOf(t, e); st.Authorized || st.Login == nil {
		t.Fatalf("auth status: %+v, want a waiting login", st)
	}

	rec := doJSON(t, e, http.MethodDelete, "/v1/account/deletion", "")
	if rec.Code != http.StatusForbidden || errCode(t, rec.Body.Bytes()) != "control.forbidden" {
		t.Errorf("revert without the token: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSONH(t, e, http.MethodDelete, "/v1/account/deletion", "", token)
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec.Body.Bytes()) != api.ErrAccountNetworkUnavailable {
		t.Errorf("revert with no network: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSONH(t, e, http.MethodPost, "/v1/account/deletion", "", token)
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec.Body.Bytes()) != "auth.login_pending" {
		t.Errorf("request while the login waits: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_AccountDeletion_ColdLogin: a device that holds only the key
// of an account whose deletion is pending logs in against the network as
// it is. A sync node may still serve the account or refuse it, so the
// state shows on the waiting login or on the open account; the revert
// answers in both, and the account ends up open and active.
func TestServer_AccountDeletion_ColdLogin(t *testing.T) {
	if _, err := os.ReadFile(stagingPath); err != nil {
		t.Skipf("staging config not available: %v", err)
	}
	first := newUnauthorizedDeps(t)
	e1 := buildEcho(first)
	created := postAuth(t, e1, `{}`)
	var rec *httptest.ResponseRecorder
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(time.Second) {
		rec = doJSON(t, e1, http.MethodPost, "/v1/account/deletion", "")
		if rec.Code == http.StatusOK {
			break
		}
		code := errCode(t, rec.Body.Bytes())
		if (code != api.ErrAccountNotOnNetwork && code != api.ErrAccountNetworkUnavailable) || time.Now().After(deadline) {
			t.Fatalf("POST /v1/account/deletion: %d %s", rec.Code, rec.Body.String())
		}
	}
	pending := decodeAccountDeletion(t, rec)
	first.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedServerShutdown)

	d := newUnauthorizedDeps(t)
	e := buildEcho(d)
	if login := postAuth(t, e, `{"mnemonic":`+strconv.Quote(created.Mnemonic)+`}`); login.AccountId != created.AccountId {
		t.Fatalf("login reply: %+v", login)
	}
	// The state, wherever the login stands.
	var seen *api.AccountDeletion
	for deadline := time.Now().Add(90 * time.Second); seen == nil; time.Sleep(200 * time.Millisecond) {
		st := authStatusOf(t, e)
		switch {
		case st.Login != nil:
			seen = st.Login.Deletion
		case st.Authorized:
			account, _ := getAccount(t, e)
			seen = account.Deletion
		}
		if seen == nil && time.Now().After(deadline) {
			t.Fatalf("the device never read the deletion state: %+v", st)
		}
	}
	if seen.State != "pending" || seen.DeleteAt == nil || !seen.DeleteAt.Equal(*pending.DeleteAt) {
		t.Fatalf("deletion state on the device = %+v, want pending until %s", seen, pending.DeleteAt)
	}

	// The revert answers 503 only when the login ends under it.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		rec = doJSON(t, e, http.MethodDelete, "/v1/account/deletion", "")
		if rec.Code != http.StatusServiceUnavailable || time.Now().After(deadline) {
			break
		}
	}
	if active := decodeAccountDeletion(t, rec); active.State != "active" {
		t.Fatalf("revert = %s", rec.Body.String())
	}
	for deadline := time.Now().Add(3 * time.Minute); ; time.Sleep(200 * time.Millisecond) {
		if st := authStatusOf(t, e); st.Authorized && st.Login == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("the account never opened after the revert: %+v", st)
		}
	}
	if account, body := getAccount(t, e); account.Deletion == nil || account.Deletion.State != "active" {
		t.Fatalf("GET /v1/account after the revert = %s", body)
	}
}
