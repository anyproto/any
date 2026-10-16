package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any-sync/app/logger"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// A handler parked inside the gate outlives the teardown deadline, so
// the engine's resources (SDK DB, instance lock) stay open behind the
// cleared deps. A boot of the same account is refused as
// account_in_use for as long as the straggler holds them — through
// the same deps and through a fresh one on the same root (the host
// stopped and started the server), on every build — and succeeds once
// it has left.
func TestAuth_BootAfterStragglerTeardown(t *testing.T) {
	if _, err := config.LoadNodeconf(config.Network{NodeconfPath: stagingPath}); err != nil {
		t.Skipf("staging config not available: %v", err)
	}
	d := newUnauthorizedDeps(t)
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body.String())
	}
	var first api.AuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}

	leave := enterStraggler(t, d)
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedDeauthorized)

	// A second server on the same root: the host stopped and started
	// the embedded server while the straggler is still inside.
	d2 := newUnauthorizedDeps(t)
	d2.root, d2.cfg.DataDir = d.root, d.root
	e2 := buildEcho(d2)

	reboot := `{"accountId":` + strconv.Quote(first.AccountId) + `}`
	for name, h := range map[string]http.Handler{"same deps": e, "new deps": e2} {
		rec = doJSON(t, h, http.MethodPost, "/v1/auth", reboot)
		// The held refusal names the straggler, never another process:
		// no details.pid, which only the OS lock's refusal carries.
		body := rec.Body.String()
		if rec.Code != http.StatusConflict || !strings.Contains(body, "auth.account_in_use") ||
			!strings.Contains(body, "previous session") || strings.Contains(body, "pid") {
			t.Fatalf("%s: boot while the straggler holds the engine: want 409 auth.account_in_use (held), got %d %s", name, rec.Code, body)
		}
	}

	// The straggler leaves: resources close and the account boots again.
	leave()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rec = doJSON(t, e2, http.MethodPost, "/v1/auth", reboot)
		if rec.Code == http.StatusOK {
			break
		}
		if rec.Code != http.StatusConflict || time.Now().After(deadline) {
			t.Fatalf("boot after the straggler left: %d %s", rec.Code, rec.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	var again api.AuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.AccountId != first.AccountId || again.Created {
		t.Fatalf("reboot reply: %+v", again)
	}
}

// enterStraggler parks a request inside d's gate and returns the one
// call that lets it leave; the test's cleanup makes that call if the
// test does not.
func enterStraggler(t *testing.T, d *deps) func() {
	t.Helper()
	h := d.gate.enter()
	if h == nil {
		t.Fatal("gate closed after boot")
	}
	var once sync.Once
	leave := func() { once.Do(func() { d.gate.leave(h) }) }
	t.Cleanup(leave)
	return leave
}

// A straggler that outlives its teardown and an in-place boot of
// another account still releases its engine when it leaves, without
// touching the second engine's pub/sub interests; once the second
// account is torn down, the first boots again.
func TestAuth_StragglerOutlivesSwitch(t *testing.T) {
	if _, err := config.LoadNodeconf(config.Network{NodeconfPath: stagingPath}); err != nil {
		t.Skipf("staging config not available: %v", err)
	}
	d := newUnauthorizedDeps(t)
	e := buildEcho(d)

	rec := doJSON(t, e, http.MethodPost, "/v1/auth", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body.String())
	}
	var first api.AuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	engA := d.eng
	leave := enterStraggler(t, d)
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedDeauthorized)

	// Another account boots in place while the straggler is inside.
	rec = doJSON(t, e, http.MethodPost, "/v1/auth", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate second: %d %s", rec.Code, rec.Body.String())
	}
	var second api.AuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.AccountId == first.AccountId {
		t.Fatalf("second generate returned the first account %s", first.AccountId)
	}
	waitProcessInterest(t, d.eng)

	// The straggler leaves: the first engine closes, and the second
	// keeps its standing process interest.
	leave()
	deadline := time.Now().Add(30 * time.Second)
	for dirHeld(engA.dir) {
		if time.Now().After(deadline) {
			t.Fatal("first engine not released after the straggler left")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := processInterest(d); n != 1 {
		t.Fatalf("second engine's process interest after the first closed: %d, want 1", n)
	}
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedDeauthorized)

	reboot := `{"accountId":` + strconv.Quote(first.AccountId) + `}`
	deadline = time.Now().Add(30 * time.Second)
	for {
		rec = doJSON(t, e, http.MethodPost, "/v1/auth", reboot)
		if rec.Code == http.StatusOK {
			break
		}
		if rec.Code != http.StatusConflict || time.Now().After(deadline) {
			t.Fatalf("boot of the first account after the straggler left: %d %s", rec.Code, rec.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// processInterest is the account-scope refcount of the standing
// process.* interest (holdProcessInterest; no SSE subscriber in these
// tests).
func processInterest(d *deps) int {
	b := d.eventsNet()
	b.mu.Lock()
	defer b.mu.Unlock()
	sc := b.scopes[""]
	if sc == nil {
		return 0
	}
	return sc.desired[eventTopicPrefix+"process/>"]
}

// waitProcessInterest waits until eng's holdProcessInterest acquired.
func waitProcessInterest(t *testing.T, eng *engine) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		eng.mu.Lock()
		ok := eng.procRelease != nil
		eng.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("process interest never acquired")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
