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

	// The straggler: a request that entered the gate and never leaves.
	if !d.gate.enter() {
		t.Fatal("gate closed after boot")
	}
	var leaveOnce sync.Once
	leave := func() { leaveOnce.Do(d.gate.leave) }
	t.Cleanup(leave)
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
